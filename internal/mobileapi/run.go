package mobileapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gopcua/opcua/ua"
)

func (s *Session) prepare(c command) (any, error) {
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	if paused {
		return nil, errors.New("resume the session before previewing")
	}
	collection, r, profile, e := s.selected(c)
	if e != nil {
		return nil, e
	}
	if !(r.Protocol == "opcua" && r.Action == "subscribe") {
		if e := s.idle(); e != nil {
			return nil, e
		}
	}
	if !knownAction(r.Protocol, r.Action) {
		return nil, errors.New("choose a supported protocol and operation")
	}
	if r.Endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if _, e = r.Duration(); e != nil {
		return nil, e
	}
	resolved, e := collection.Resolve(r, profile)
	if e != nil {
		return nil, e
	}
	if e = s.preparePreviewRequest(&resolved); e != nil {
		return nil, e
	}
	if r.Mutates() && s.options.ReadOnly {
		return nil, errors.New("read-only mode blocks write operations")
	}
	if resolved.Protocol == "modbus" && (resolved.Action == "read-raw" || resolved.Action == "write-raw") {
		if _, e = engine.ValidateModbusRaw(resolved); e != nil {
			return nil, e
		}
	}
	var protocolPreview map[string]any
	if resolved.Protocol == "modbus" {
		protocolPreview, e = engine.PreviewModbusOperation(resolved)
		if e != nil {
			return nil, e
		}
		protocolPreview["endpoint"] = displayRequest(resolved).Endpoint
	}
	token, e := newToken()
	if e != nil {
		return nil, e
	}
	warnings := []string{}
	if r.Protocol == "http" {
		warnings = append(warnings, "工作流依赖、动态输入和 TLS 例外将在执行时分别确认。")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.previews) >= 16 {
		s.previews = map[string]preview{}
	}
	s.previews[token] = preview{Token: token, Request: r, Resolved: resolved, Collection: collection, Profile: profile, Revision: s.revision, Expires: time.Now().Add(5 * time.Minute)}
	return map[string]any{"token": token, "request": displayRequest(resolved), "mutates": r.Mutates(), "confirmation_required": r.Mutates(), "summary": fmt.Sprintf("%s %s · %s", strings.ToUpper(r.Protocol), r.Action, displayRequest(resolved).Endpoint), "warnings": warnings, "protocol_preview": protocolPreview}, nil
}

func (s *Session) run(c command) (any, error) {
	s.mu.Lock()
	p, ok := s.previews[c.Token]
	s.mu.Unlock()
	if ok && p.Request.Protocol == "opcua" && p.Request.Action == "subscribe" {
		return s.startSubscription(c, p)
	}
	s.mu.Lock()
	if s.closed || s.paused {
		s.mu.Unlock()
		return nil, errors.New("session is closed or paused")
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return nil, errors.New("an operation is already running")
	}
	p, ok = s.previews[c.Token]
	if !ok || p.Utility != nil || p.Revision != s.revision || time.Now().After(p.Expires) {
		s.mu.Unlock()
		return nil, errors.New("preview expired or configuration changed; preview again")
	}
	if p.Request.Mutates() && (!c.Confirmed || s.options.ReadOnly) {
		s.mu.Unlock()
		return nil, errors.New("write requires explicit confirmation and read-only mode disabled")
	}
	runID, e := newToken()
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	delete(s.previews, c.Token)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.runID, s.runDone = cancel, runID, done
	controller := engine.NewModbusPauseController()
	if p.Request.Protocol == "modbus" && !p.Request.Mutates() {
		s.modbusPause = controller
	}
	if p.Request.Protocol == "modbus" {
		s.modbusStats = map[string]any{"operations": 0, "success": 0, "failure": 0, "duration_ms": float64(0)}
	}
	s.mu.Unlock()
	started := map[string]any{"request_id": p.Request.ID, "protocol": p.Request.Protocol, "action": p.Request.Action}
	if p.Resolved.Protocol == "modbus" {
		started["source"] = map[string]any{"request_id": p.Resolved.ID, "endpoint": p.Resolved.Endpoint, "unit": p.Resolved.Int("unit", 1), "action": p.Resolved.Action}
	}
	s.emit(runID, "started", started)
	go func() {
		var runErr error
		defer func() {
			if recover() != nil {
				runErr = errors.New("engine stopped after an unexpected invalid result")
			}
			cancel()
			status := "completed"
			message := ""
			if runErr != nil {
				message = runErr.Error()
				status = "failed"
				if errors.Is(runErr, context.Canceled) {
					status = "cancelled"
				}
			}
			if p.Request.Protocol == "modbus" && status == "cancelled" {
				s.recordModbusCancellation()
			}
			s.emit(runID, "done", map[string]any{"status": status, "error": message, "request_id": p.Request.ID})
			s.mu.Lock()
			s.cancel = nil
			s.modbusPause = nil
			s.interactions = map[string]chan interactionReply{}
			s.mu.Unlock()
			close(done)
		}()
		options := s.workflowOptions(runID, true)
		// Confirm a changed runtime target separately, even after the source preview
		// was approved. Chain writes always receive their own dialog.
		expected := p.Resolved
		if e := s.preparePreviewRequest(&expected); e != nil {
			runErr = e
			return
		}
		options.AuthorizeRequestWrite = func(ctx context.Context, r config.Request) (bool, error) {
			if s.options.ReadOnly {
				return false, nil
			}
			if c.Confirmed && reflect.DeepEqual(expected, r) {
				return true, nil
			}
			return s.confirm(ctx, runID, "确认解析后的 HTTP 写入", r)
		}
		ctx = engine.WithHTTPWorkflowOptions(ctx, options)
		ctx = engine.WithModbusPause(ctx, controller)
		ctx = engine.WithModbusObserver(ctx, func(op engine.ModbusOperation) { s.observeModbus(runID, op) })
		if s.options.RTUTransport != nil {
			ctx = engine.WithModbusRTUTransport(ctx, s.options.RTUTransport)
		}
		// A non-HTTP request has no workflow file hook; resolve and scope it here.
		if p.Request.Protocol != "http" {
			runErr = engine.Run(ctx, expected, c.Confirmed, func(ev engine.Event) {
				s.recordConnection(expected, ev)
				s.emit(runID, ev.Kind, normalizeEvent(ev.Data))
			})
			return
		}
		runErr = engine.RunCollection(ctx, p.Collection, p.Request, p.Profile, c.Confirmed, func(ev engine.Event) { s.emit(runID, ev.Kind, normalizeEvent(ev.Data)) })
	}()
	return map[string]any{"run_id": runID}, nil
}

func (s *Session) workflowOptions(runID string, interactive bool) engine.HTTPWorkflowOptions {
	options := engine.HTTPWorkflowOptions{
		ValidateFilePath: s.privatePath,
		ValidateRequest:  func(r config.Request) error { return s.prepareRequest(&r) },
	}
	// Read-only protection covers automatic history writes as well as commands.
	// Keep the preference intact so disabling read-only resumes future recording.
	if s.options.History {
		options.HistoryPath = filepath.Join(s.root, "history.sqlite")
		options.HistoryReadOnly = s.options.ReadOnly
	}
	if !interactive {
		return options
	}
	options.AuthorizeChainWrite = func(ctx context.Context, r config.Request) (bool, error) {
		if s.options.ReadOnly {
			return false, nil
		}
		return s.confirm(ctx, runID, "确认依赖请求的 HTTP 写入", r)
	}
	options.AuthorizeInsecureTLS = func(ctx context.Context, r config.Request) (bool, error) {
		return s.confirm(ctx, runID, "此请求将跳过目标服务器的 TLS 证书验证。确认继续？", r)
	}
	options.Prompt = func(ctx context.Context, title, def string, sensitive bool) (string, error) {
		v, e := s.interact(ctx, runID, map[string]any{"type": "prompt", "title": title, "default": def, "sensitive": sensitive})
		if e != nil {
			return "", e
		}
		if !v.Confirmed {
			return "", errors.New("prompt cancelled")
		}
		text, ok := v.Value.(string)
		if !ok {
			return "", errors.New("prompt reply must be text")
		}
		return text, nil
	}
	options.Select = func(ctx context.Context, title string, choices []any) (any, error) {
		v, e := s.interact(ctx, runID, map[string]any{"type": "select", "title": title, "options": choices})
		if e != nil {
			return nil, e
		}
		if !v.Confirmed {
			return nil, errors.New("selection cancelled")
		}
		if v.SelectionIndex != nil {
			if *v.SelectionIndex < 0 || *v.SelectionIndex >= len(choices) {
				return nil, errors.New("选择索引超出当前选项范围")
			}
			return choices[*v.SelectionIndex], nil
		}
		a, _ := json.Marshal(v.Value)
		for _, choice := range choices {
			b, _ := json.Marshal(choice)
			if bytes.Equal(a, b) {
				return choice, nil
			}
		}
		return nil, errors.New("selection does not match an offered option")
	}
	return options
}
func (s *Session) confirm(ctx context.Context, runID, title string, r config.Request) (bool, error) {
	v, e := s.interact(ctx, runID, map[string]any{"type": "confirm", "title": title, "request": displayRequest(r)})
	return v.Confirmed, e
}
func (s *Session) interact(ctx context.Context, runID string, data map[string]any) (interactionReply, error) {
	encoded, e := wireMarshal(data)
	if e != nil || len(encoded) > maxEventBytes-1024 {
		return interactionReply{}, errors.New("交互预览超过64KiB，请缩小请求或选择列表后重试")
	}
	id, e := newToken()
	if e != nil {
		return interactionReply{}, e
	}
	ch := make(chan interactionReply, 1)
	s.mu.Lock()
	if s.closed || s.paused {
		s.mu.Unlock()
		return interactionReply{}, context.Canceled
	}
	s.interactions[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.interactions, id); s.mu.Unlock() }()
	data["interaction_id"] = id
	s.emit(runID, "interaction", data)
	select {
	case <-ctx.Done():
		return interactionReply{}, ctx.Err()
	case value := <-ch:
		return value, nil
	}
}
func (s *Session) respond(c command) string {
	s.mu.Lock()
	ch, ok := s.interactions[c.InteractionID]
	if ok {
		delete(s.interactions, c.InteractionID)
	}
	s.mu.Unlock()
	if !ok {
		return reply(nil, errors.New("interaction expired or already answered"))
	}
	ch <- interactionReply{Confirmed: c.Confirmed, Value: c.Value, SelectionIndex: c.SelectionIndex}
	return reply(map[string]any{"accepted": true}, nil)
}
func binaryResult(b []byte) map[string]any {
	out := map[string]any{"base64": base64.StdEncoding.EncodeToString(b), "bytes": len(b)}
	if utf8.Valid(b) {
		out["text"] = string(b)
	}
	return out
}

// Flatten browsing nodes into explicit identifiers usable by native controls.
func normalizeEvent(value any) any {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) {
			return "NaN"
		}
		if math.IsInf(v, 1) {
			return "+Infinity"
		}
		if math.IsInf(v, -1) {
			return "-Infinity"
		}
		return v
	case float32:
		if math.IsNaN(float64(v)) {
			return "NaN"
		}
		if math.IsInf(float64(v), 1) {
			return "+Infinity"
		}
		if math.IsInf(float64(v), -1) {
			return "-Infinity"
		}
		return v
	case []float64:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalizeEvent(x)
		}
		return out
	case []float32:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalizeEvent(x)
		}
		return out
	case *ua.ReferenceDescription:
		if v == nil {
			return nil
		}
		out := map[string]any{"node_class": v.NodeClass.String(), "is_forward": v.IsForward}
		if v.NodeID != nil {
			out["node_id"] = v.NodeID.String()
		}
		if v.DisplayName != nil {
			out["display_name"] = v.DisplayName.Text
		}
		if v.BrowseName != nil {
			out["browse_name"] = v.BrowseName.Name
			out["namespace"] = v.BrowseName.NamespaceIndex
		}
		return out
	case []*ua.ReferenceDescription:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalizeEvent(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalizeEvent(x)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			out[k] = normalizeEvent(x)
		}
		return out
	}
	return value
}
