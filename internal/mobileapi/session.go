// Package mobileapi provides the bounded JSON interface used by the native
// Android application. It deliberately has no dependency on the terminal UI.
package mobileapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/Live-yum/iotools/internal/sample"
)

const (
	maxCommandBytes = 8 << 20
	maxReplyBytes   = 16 << 20
	maxEventBytes   = 64 << 10
	maxQueueBytes   = 1 << 20
	maxQueueEvents  = 256
)

type Options struct {
	ReadOnly     bool                      `json:"read_only"`
	History      bool                      `json:"history"`
	RTUTransport engine.ModbusRTUTransport `json:"-"`
	// PrivateRoot is supplied only by the trusted platform host at Open. It
	// keeps recovery of a nested collection inside the original private sandbox.
	// JSON commands cannot set it or change a session's root after opening.
	PrivateRoot string `json:"-"`
	// RequireExisting distinguishes recovery from first-launch initialization.
	// Only the trusted platform host can select this behavior.
	RequireExisting bool `json:"-"`
}

type queuedEvent struct {
	Seq   uint64    `json:"seq"`
	RunID string    `json:"run_id,omitempty"`
	Time  time.Time `json:"time"`
	Kind  string    `json:"kind"`
	Data  any       `json:"data"`
}

type preview struct {
	Digest     string
	Resolved   config.Request
	Utility    *command
	Token      string
	Request    config.Request
	Collection *config.Collection
	Profile    string
	Revision   uint64
	Expires    time.Time
}

type interactionReply struct {
	SelectionIndex *int
	Confirmed      bool
	Value          any
}

type modbusTotals struct {
	Reads      int     `json:"reads"`
	Writes     int     `json:"writes"`
	Errors     int     `json:"errors"`
	Cancelled  int     `json:"cancelled"`
	Success    int     `json:"success"`
	DurationMS float64 `json:"duration_ms"`
}
type Session struct {
	modbusTotals                 modbusTotals
	results                      map[string]cachedResult
	resultOrder                  []string
	resultBytes                  int
	evictedResults               []string
	evictedResultCount           uint64
	modbusPause                  *engine.ModbusPauseController
	modbusStats                  map[string]any
	subscriptions                map[string]*subscription
	connections                  []connectionRecord
	metadataMu                   sync.Mutex
	commands                     sync.Mutex
	mu                           sync.Mutex
	path, root, version, profile string
	options                      Options
	collection                   *config.Collection
	source                       []byte
	revision                     uint64
	paused, closed               bool
	cancel                       context.CancelFunc
	runID                        string
	runDone                      chan struct{}
	previews                     map[string]preview
	interactions                 map[string]chan interactionReply
	events                       []json.RawMessage
	eventBytes                   int
	sequence                     uint64
	dropped                      uint64
}

// Open loads only local configuration. No protocol connection or template is
// evaluated until an explicit preview/run command. path must be app-private.
func Open(path, version string, options Options) (*Session, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("configuration path must be absolute and app-private")
	}
	path = filepath.Clean(path)
	root := filepath.Dir(path)
	if options.PrivateRoot != "" {
		if !filepath.IsAbs(options.PrivateRoot) {
			return nil, errors.New("app-private root must be absolute")
		}
		root = filepath.Clean(options.PrivateRoot)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("app-private configuration directory does not exist")
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	outside := func(name string, err error) bool {
		return err != nil || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator))
	}
	if outside(rel, err) && options.PrivateRoot != "" {
		// The platform may restore the canonical path while supplying its usual
		// root alias (for example Android's /data/user/0 app-private directory).
		rel, err = filepath.Rel(real, path)
	}
	if outside(rel, err) {
		return nil, errors.New("configuration path is outside the app-private directory")
	}
	s := &Session{path: filepath.Join(real, rel), root: real, version: version, options: options, previews: map[string]preview{}, subscriptions: map[string]*subscription{}, results: map[string]cachedResult{}, interactions: map[string]chan interactionReply{}, events: []json.RawMessage{}}
	if err = s.privatePath(s.path); err != nil {
		return nil, err
	}
	if _, err = os.Lstat(s.path); os.IsNotExist(err) {
		if options.RequireExisting {
			return nil, fmt.Errorf("recovery configuration does not exist: %w", err)
		}
		f, e := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(sample.Collection)
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
	} else if err != nil {
		return nil, err
	}
	data, err := readBounded(s.path, 4<<20)
	if err != nil {
		return nil, err
	}
	c, err := s.parseCollection(data)
	if err != nil {
		return nil, err
	}
	s.collection, s.source, s.profile, s.revision = c, data, defaultProfile(c), 1
	s.loadConnections()
	return s, nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("expected a bounded regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if len(b) > int(limit) {
		return nil, errors.New("file exceeds size limit")
	}
	return b, e
}

func (s *Session) stateLocked() map[string]any {
	profiles := make([]string, 0, len(s.collection.Profiles))
	for name := range s.collection.Profiles {
		profiles = append(profiles, name)
	}
	sort.Strings(profiles)
	return map[string]any{"path": s.path, "version": s.version, "profile": s.profile, "profiles": profiles, "requests": s.collection.Requests, "options": s.options, "running": s.cancel != nil, "run_id": s.runID, "paused": s.paused, "closed": s.closed, "revision": s.revision}
}

func reply(data any, err error) string {
	var v any = map[string]any{"ok": true, "data": data}
	if err != nil {
		v = map[string]any{"ok": false, "error": err.Error()}
	}
	b, e := wireMarshal(v)
	if e != nil {
		return `{"ok":false,"error":"result is not JSON serializable"}`
	}
	if len(b) > maxReplyBytes {
		return `{"ok":false,"error":"result exceeds 16 MiB; narrow the query"}`
	}
	return string(b)
}

func newToken() (string, error) {
	var b [24]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}

// Command is synchronous for local editing/query utilities; protocol execution
// is asynchronous and its results are consumed using Poll or op=events.
func (s *Session) Command(input string) (output string) {
	defer func() {
		if recover() != nil {
			output = reply(nil, errors.New("invalid command or engine result"))
		}
	}()
	if len(input) > maxCommandBytes {
		return reply(nil, errors.New("command exceeds 8 MiB"))
	}
	if err := validateJSONStructure(input); err != nil {
		return reply(nil, err)
	}
	var c command
	dec := json.NewDecoder(strings.NewReader(input))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&c); err != nil {
		return reply(nil, fmt.Errorf("invalid command: %w", err))
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return reply(nil, errors.New("expected exactly one JSON command"))
	}
	if c.Request != nil {
		if err := normalizeRequest(c.Request); err != nil {
			return reply(nil, err)
		}
	}
	if c.Op == "events" {
		return s.Poll()
	}
	if c.Op == "cancel" {
		s.mu.Lock()
		current := s.runID
		cancel := s.cancel
		if c.RunID != "" && c.RunID != current {
			s.mu.Unlock()
			return reply(nil, errors.New("operation no longer matches run_id"))
		}
		s.previews = map[string]preview{}
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return reply(map[string]any{"cancelled": true}, nil)
	}
	if c.Op == "pause" {
		s.Pause()
		return reply(map[string]any{"paused": true}, nil)
	}
	if c.Op == "resume" {
		s.Resume()
		return reply(map[string]any{"paused": false}, nil)
	}
	if c.Op == "respond" {
		return s.respond(c)
	}
	s.commands.Lock()
	defer s.commands.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return reply(nil, errors.New("session is closed"))
	}
	data, err := s.command(c)
	return reply(data, err)
}

func (s *Session) Poll() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.events
	if events == nil {
		events = []json.RawMessage{}
	}
	s.events = nil
	s.eventBytes = 0
	data := map[string]any{"events": events, "running": s.cancel != nil, "run_id": s.runID, "paused": s.paused, "dropped": s.dropped, "evicted_result_ids": s.evictedResults, "evicted_result_count": s.evictedResultCount}
	s.dropped = 0
	s.evictedResults = nil
	s.evictedResultCount = 0
	return reply(data, nil)
}

func (s *Session) emit(runID, kind string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.sequence++
	ev := queuedEvent{Seq: s.sequence, RunID: runID, Time: time.Now().UTC(), Kind: kind, Data: data}
	b, e := wireMarshal(ev)
	if e != nil {
		ev.Kind = "result-error"
		ev.Data = map[string]any{"error": "event contains an unsupported value"}
		b, _ = wireMarshal(ev)
	}
	if len(b) > maxEventBytes {
		// Keep a truthful, bounded preview, while explicitly identifying omitted data.
		raw, _ := wireMarshal(data)
		n := maxEventBytes / 4
		if len(raw) < n {
			n = len(raw)
		}
		detail := map[string]any{"truncated": true, "original_bytes": len(raw), "preview": string(bytes.ToValidUTF8(raw[:n], []byte("�")))}
		if len(raw) <= maxCachedResultBytes {
			resultID := fmt.Sprintf("%s:%d", runID, s.sequence)
			s.cacheResultLocked(resultID, runID, kind, raw)
			detail["result_id"] = resultID
		} else {
			detail["result_unavailable_reason"] = "结果超过单份16MiB内存上限，请缩小查询范围"
		}
		ev.Data = detail
		b, _ = wireMarshal(ev)
	}
	for len(s.events) > 0 && (len(s.events) >= maxQueueEvents || s.eventBytes+len(b) > maxQueueBytes) {
		index := 0
		for index < len(s.events)-1 && bytes.Contains(s.events[index], []byte(`"kind":"interaction"`)) {
			index++
		}
		s.eventBytes -= len(s.events[index])
		copy(s.events[index:], s.events[index+1:])
		s.events[len(s.events)-1] = nil
		s.events = s.events[:len(s.events)-1]
		s.dropped++
	}
	s.events = append(s.events, b)
	s.eventBytes += len(b)
}

func (s *Session) cancelRun() {
	s.mu.Lock()
	cancel := s.cancel
	s.previews = map[string]preview{}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Pause cancels active I/O and pending dialogs. Resume never replays a request.
func (s *Session) Pause() {
	s.mu.Lock()
	s.paused = true
	transport := s.options.RTUTransport
	s.mu.Unlock()
	s.cancelRun()
	s.stopSubscriptions("")
	if transport != nil {
		_ = transport.Close()
	}
}
func (s *Session) Resume() {
	s.mu.Lock()
	if !s.closed {
		s.paused = false
	}
	s.mu.Unlock()
}
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for _, sub := range s.subscriptions {
		if sub.cancel != nil {
			sub.cancel()
		}
	}
	transport := s.options.RTUTransport
	cancel := s.cancel
	s.previews = nil
	s.events = nil
	s.eventBytes = 0
	s.results = nil
	s.resultOrder = nil
	s.resultBytes = 0
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if transport != nil {
		_ = transport.Close()
	}
}
func (s *Session) Stop() { s.Close() }

func (s *Session) idle() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session is closed")
	}
	if s.cancel != nil {
		return errors.New("cancel the active operation before editing")
	}
	return nil
}

func normalizeRequest(r *config.Request) error {
	v, e := normalizeJSON(r.Params, 0)
	if e != nil {
		return e
	}
	if v != nil {
		r.Params = v.(map[string]any)
	}
	return nil
}
func normalizeJSON(v any, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("JSON exceeds nesting limit")
	}
	switch x := v.(type) {
	case json.Number:
		if n, e := x.Int64(); e == nil {
			return n, nil
		}
		if !strings.ContainsAny(string(x), ".eE") {
			return string(x), nil
		}
		n, e := x.Float64()
		return n, e
	case map[string]any:
		for k, v := range x {
			n, e := normalizeJSON(v, depth+1)
			if e != nil {
				return nil, e
			}
			x[k] = n
		}
		return x, nil
	case []any:
		for i, v := range x {
			n, e := normalizeJSON(v, depth+1)
			if e != nil {
				return nil, e
			}
			x[i] = n
		}
		return x, nil
	}
	return v, nil
}

func defaultProfile(c *config.Collection) string {
	if c.DefaultProfile != "" {
		return c.DefaultProfile
	}
	if _, ok := c.Profiles["local"]; ok {
		return "local"
	}
	return ""
}
