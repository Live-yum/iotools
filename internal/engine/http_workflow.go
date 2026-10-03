package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	codec "github.com/Live-yum/iotools/internal/crypto"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// HTTPWorkflowOptions controls only explicit workflow capabilities. History is
// opt-in for CLI/TUI; Flutter opts in by default while honoring saved opt-outs.
// Response bodies and headers may contain credentials or private data.
type HTTPWorkflowOptions struct {
	// Optional host sandbox hooks. Desktop callers leave these nil.
	ValidateFilePath      func(string) error
	ValidateRequest       func(config.Request) error
	NoNetwork             bool
	AllowInsecureTLS      bool
	AuthorizeInsecureTLS  func(context.Context, config.Request) (bool, error)
	HistoryPath           string
	HistoryReadOnly       bool
	AllowChainWrites      bool
	AuthorizeChainWrite   func(context.Context, config.Request) (bool, error)
	AuthorizeRequestWrite func(context.Context, config.Request) (bool, error)
	Prompt                func(context.Context, string, string, bool) (string, error)
	Select                func(context.Context, string, []any) (any, error)
}
type httpWorkflowKey struct{}

func WithHTTPWorkflowOptions(ctx context.Context, options HTTPWorkflowOptions) context.Context {
	return context.WithValue(ctx, httpWorkflowKey{}, options)
}

type httpWorkflow struct {
	ctx                             context.Context
	collection                      *config.Collection
	profile, rootDir, collectionKey string
	vars                            map[string]string
	varsCache                       map[string]any
	varsActive, active, executed    map[string]bool
	responses                       map[string]*HTTPHistoryEntry
	allowWrites                     bool
	options                         HTTPWorkflowOptions
	history                         *HTTPHistory
	current                         config.Request
	steps, requests                 int
}

// RunCollection resolves legacy and Slumber templates lazily, then executes a
// request. Every mutating dependency has a separate authorization boundary.
func RunCollection(ctx context.Context, c *config.Collection, r config.Request, profile string, allowWrites bool, emit Emit) error {
	if c == nil {
		return fmt.Errorf("collection is nil")
	}
	if profile == "" {
		profile = c.DefaultProfile
	}
	if r.Protocol != "http" {
		resolved, e := c.Resolve(r, profile)
		if e != nil {
			return e
		}
		return Run(ctx, resolved, allowWrites, emit)
	}
	options, _ := ctx.Value(httpWorkflowKey{}).(HTTPWorkflowOptions)
	if r.Mutates() && !allowWrites && options.AuthorizeRequestWrite == nil {
		return fmt.Errorf("%s is a write operation; explicit confirmation or --allow-writes is required", r.Action)
	}
	vars := map[string]string{}
	if profile != "" {
		var ok bool
		vars, ok = c.Profiles[profile]
		if !ok {
			return fmt.Errorf("unknown profile %q", profile)
		}
	}
	rootDir := "."
	collectionKey := c.SourcePath
	if collectionKey != "" {
		rootDir = filepath.Dir(collectionKey)
	} else {
		encoded, _ := json.Marshal(c)
		collectionKey = fmt.Sprintf("memory:%x", sha256.Sum256(encoded))
	}
	w := &httpWorkflow{ctx: ctx, collection: c, profile: profile, rootDir: rootDir, collectionKey: collectionKey, vars: vars, varsCache: map[string]any{}, varsActive: map[string]bool{}, active: map[string]bool{}, executed: map[string]bool{}, responses: map[string]*HTTPHistoryEntry{}, allowWrites: allowWrites, options: options}
	if options.HistoryPath != "" {
		history, e := openWorkflowHistory(options.HistoryPath, options.HistoryReadOnly)
		if e != nil {
			return e
		}
		if history != nil {
			w.history = history
			defer history.Close()
		}
	}
	return w.run(r, false, emit)
}
func (w *httpWorkflow) legacyString(s string) (string, error) {
	r := config.Request{Endpoint: s}
	r, e := w.collection.Resolve(r, w.profile)
	return r.Endpoint, e
}
func (w *httpWorkflow) renderRequest(r config.Request) (config.Request, error) {
	// The existing resolver intentionally selects only codecs referenced by
	// transformations; template codec calls resolve their definitions on demand.
	resolved, e := w.collection.Resolve(r, w.profile)
	if e != nil {
		return r, e
	}
	endpoint, e := w.render(resolved.Endpoint, false)
	if e != nil {
		return r, e
	}
	resolved.Endpoint = endpoint.(string)
	params := map[string]any{}
	for _, key := range sortedKeys(resolved.Params) {
		if key == "body_stream" {
			path, stream, err := w.streamFileTemplate(resolved.Params[key])
			if err != nil {
				return r, err
			}
			if stream {
				params["body_file"] = path
				continue
			}
			value, err := w.walk(resolved.Params[key], true, 0)
			if err != nil {
				return r, err
			}
			b, err := templateBytes(value)
			if err != nil {
				return r, err
			}
			params["body"] = string(b)
			continue
		}
		typed := key == "json" || key == "body" || key == "form_multipart"
		value, e := w.walk(resolved.Params[key], typed, 0)
		if e != nil {
			return r, fmt.Errorf("parameter %s: %w", key, e)
		}
		if key == "body" {
			b, e := templateBytes(value)
			if e != nil {
				return r, e
			}
			value = string(b)
		}
		if key == "json" {
			value, e = normalizeJSONBytes(value)
			if e != nil {
				return r, e
			}
		}
		if key == "body_file" || key == "response_file" {
			name, err := templateString(value)
			if err != nil {
				return r, err
			}
			value, err = w.absoluteBodyFile(name)
			if err != nil {
				return r, err
			}
		}
		params[key] = value
	}
	resolved.Params = params
	return resolved, nil
}
func normalizeJSONBytes(v any) (any, error) {
	switch x := v.(type) {
	case []byte:
		return templateString(x)
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			a, e := normalizeJSONBytes(v)
			if e != nil {
				return nil, e
			}
			out[i] = a
		}
		return out, nil
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			a, e := normalizeJSONBytes(v)
			if e != nil {
				return nil, e
			}
			out[k] = a
		}
		return out, nil
	}
	return v, nil
}
func (w *httpWorkflow) codec(id string) (codec.Config, error) {
	var c codec.Config
	defs, ok := w.current.Params["crypto"].(map[string]any)
	if !ok {
		return c, fmt.Errorf("crypto definitions missing")
	}
	raw, ok := defs[id]
	if !ok {
		return c, fmt.Errorf("unknown codec %q", id)
	}
	// Resolve ${...} only in this selected definition, then Slumber templates.
	dummy := config.Request{Protocol: "http", Params: map[string]any{"crypto": map[string]any{id: raw}, "request_crypto": id}}
	dummy, e := w.collection.Resolve(dummy, w.profile)
	if e != nil {
		return c, e
	}
	value, e := w.walk(dummy.Params["crypto"].(map[string]any)[id], false, 0)
	if e != nil {
		return c, e
	}
	e = codec.Parse(value, &c)
	return c, e
}
func (w *httpWorkflow) run(r config.Request, chained bool, emit Emit) error {
	if w.options.NoNetwork {
		return fmt.Errorf("生成期间禁止触发网络请求；需要时明确启用 execute-triggers")
	}
	if e := w.ctx.Err(); e != nil {
		return e
	}
	if r.Protocol != "http" {
		return fmt.Errorf("response chains require an HTTP recipe")
	}
	if w.active[r.ID] {
		return fmt.Errorf("cyclic request dependency %q", r.ID)
	}
	if len(w.active) >= 32 || w.requests >= 128 {
		return fmt.Errorf("request chain exceeds execution limit")
	}
	w.active[r.ID] = true
	defer delete(w.active, r.ID)
	previous := w.current
	w.current = r
	defer func() { w.current = previous }()
	d, e := r.Duration()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(w.ctx, d)
	defer cancel()
	previousCtx := w.ctx
	w.ctx = ctx
	defer func() { w.ctx = previousCtx }()
	resolved, e := w.renderRequest(r)
	if e != nil {
		return fmt.Errorf("render %s: %w", r.ID, e)
	}
	if w.options.ValidateRequest != nil {
		if e = w.options.ValidateRequest(resolved); e != nil {
			return e
		}
	}
	allowed := w.allowWrites
	if chained && resolved.Mutates() {
		allowed = w.allowWrites && w.options.AllowChainWrites
		if !allowed && w.options.AuthorizeChainWrite != nil {
			allowed, e = w.options.AuthorizeChainWrite(w.ctx, resolved)
			if e != nil {
				return e
			}
		}
		if !allowed {
			return fmt.Errorf("chained %s %q requires separate write approval or --allow-writes --allow-chain-writes", resolved.Action, resolved.ID)
		}
	}
	if !chained && resolved.Mutates() && w.options.AuthorizeRequestWrite != nil {
		allowed, e = w.options.AuthorizeRequestWrite(w.ctx, resolved)
		if e != nil {
			return e
		}
		if !allowed {
			return fmt.Errorf("request write approval declined")
		}
	}
	w.requests++
	entry := &HTTPHistoryEntry{Collection: w.collectionKey, Profile: w.profile, Recipe: r.ID, Method: resolved.Action, Time: time.Now().UTC()}
	hasResponse := false
	e = Run(w.ctx, resolved, allowed, func(event Event) {
		if data, ok := event.Data.(map[string]any); ok {
			switch event.Kind {
			case "response":
				hasResponse = true
				entry.Status, _ = data["status"].(int)
				entry.Headers, _ = data["headers"].(http.Header)
				if b64, ok := data["raw_body_base64"].(string); ok {
					entry.Body, _ = base64.StdEncoding.DecodeString(b64)
				}
			case "transformed":
				entry.Transformed, _ = json.Marshal(data["body"])
			}
		}
		if chained {
			if emit != nil {
				send(emit, "chain", map[string]any{"request_id": r.ID, "event": event})
			}
		} else if emit != nil {
			emit(event)
		}
	})
	if hasResponse {
		w.responses[r.ID] = entry
		w.executed[r.ID] = true
		if w.options.HistoryPath != "" && r.Params["persist"] != false {
			if w.options.HistoryReadOnly {
				send(emit, "history_status", map[string]any{"recorded": false, "reason": "read_only", "message": "只读保护，本次未记录", "request_id": r.ID})
			} else if w.history != nil {
				if err := w.history.Add(w.ctx, *entry); errors.Is(err, ErrHTTPHistoryCapacity) {
					send(emit, "history_status", map[string]any{"recorded": false, "reason": "capacity", "message": err.Error(), "request_id": r.ID})
				} else if err != nil {
					return errors.Join(e, fmt.Errorf("HTTP completed but history save failed: %w", err))
				}
			}
		}
	}
	return e
}
func (w *httpWorkflow) response(id, trigger string) (*HTTPHistoryEntry, error) {
	for _, candidate := range w.collection.Requests {
		if candidate.ID == id && candidate.Params["response_file"] != nil {
			return nil, fmt.Errorf("response()不能读取流式文件输出请求，请选择内存响应或明确file()模板")
		}
	}
	var r config.Request
	found := false
	for _, candidate := range w.collection.Requests {
		if candidate.ID == id {
			r = candidate
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("unknown response recipe %q", id)
	}
	entry := w.responses[id]
	if entry == nil && w.history != nil {
		var e error
		entry, e = w.history.Latest(w.ctx, w.collectionKey, w.profile, id)
		if e != nil {
			return nil, e
		}
		if entry != nil {
			w.responses[id] = entry
		}
	}
	execute := false
	switch trigger {
	case "never":
	case "always":
		execute = !w.executed[id]
	case "no_history":
		execute = entry == nil
	default:
		d, e := parseTriggerDuration(trigger)
		if e != nil {
			return nil, e
		}
		execute = entry == nil || time.Since(entry.Time) > d
	}
	if execute {
		if e := w.run(r, true, nil); e != nil {
			return nil, e
		}
		entry = w.responses[id]
	}
	if entry == nil {
		return nil, fmt.Errorf("no response history for %q; set trigger='no_history' or enable history", id)
	}
	return entry, nil
}
func parseTriggerDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d") + "h"
		d, e := time.ParseDuration(s)
		if e != nil || d <= 0 || d > 3650*time.Hour {
			return 0, fmt.Errorf("invalid response trigger duration")
		}
		return d * 24, nil
	}
	d, e := time.ParseDuration(s)
	if e != nil || d <= 0 {
		return 0, fmt.Errorf("response trigger must be never, no_history, always or positive duration")
	}
	return d, nil
}

// A transformed history view is derived from raw bytes using the current
// collection. Cached plaintext is never substituted for newly changed rules.
func (w *httpWorkflow) transformResponse(id string, raw []byte) ([]byte, error) {
	var recipe config.Request
	for _, r := range w.collection.Requests {
		if r.ID == id {
			recipe = r
			break
		}
	}
	if recipe.ID == "" {
		return nil, fmt.Errorf("unknown recipe %q", id)
	}
	if value, ok := recipe.Params["response_transform"]; !ok || value == nil {
		return raw, nil
	}
	previous := w.current
	w.current = recipe
	defer func() { w.current = previous }()
	dummy := config.Request{Protocol: "http", Params: map[string]any{"crypto": recipe.Params["crypto"], "response_transform": recipe.Params["response_transform"]}}
	if dummy.Params["crypto"] == nil {
		delete(dummy.Params, "crypto")
	}
	resolved, e := w.collection.Resolve(dummy, w.profile)
	if e != nil {
		return nil, e
	}
	value, e := w.walk(resolved.Params, false, 0)
	if e != nil {
		return nil, e
	}
	resolved.Params = value.(map[string]any)
	definitions, e := httpCodecs(resolved)
	if e != nil {
		return nil, e
	}
	var rules []codec.Rule
	if e = codec.Parse(resolved.Params["response_transform"], &rules); e != nil {
		return nil, e
	}
	if len(rules) == 0 {
		return raw, nil
	}
	return codec.Transform(raw, definitions, rules)
}
