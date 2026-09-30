package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSlumberCollectionRealChain(t *testing.T) {
	var loginHits, dataHits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			loginHits.Add(1)
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			if e := r.ParseForm(); e != nil || r.Form.Get("username") != "tester" {
				t.Error("form", e)
			}
			w.Header().Set("X-Session", "demo-session")
			io.WriteString(w, `{"token":"public-test-token"}`)
		case "/data":
			dataHits.Add(1)
			if r.Header.Get("Authorization") != "Bearer public-test-token" {
				t.Error("auth")
			}
			if r.URL.Query().Get("q") != "a+b/c=" || len(r.URL.Query()["many"]) != 2 {
				t.Error("query escaping")
			}
			io.WriteString(w, `{"id":9007199254740993}`)
		}
	}))
	defer s.Close()
	source := fmt.Sprintf(`profiles:
  local:
    default: true
    data:
      host: %s
      token: "{{ response('login', trigger='no_history') | jsonpath('$.token') | sensitive() }}"
requests:
  login:
    method: POST
    url: '{{ host }}/login'
    persist: false
    body:
      type: form_urlencoded
      data: {username: tester}
  data:
    method: GET
    url: '{{ host }}/data'
    authentication: {type: bearer, token: '{{ token }}'}
    query: {q: 'a+b/c=', many: [one, two]}
`, s.URL)
	c, e := ParseCollection([]byte(source))
	if e != nil {
		t.Fatal(e)
	}
	r := c.Requests[0]
	for _, x := range c.Requests {
		if x.ID == "data" {
			r = x
		}
	}
	if e = RunCollection(context.Background(), c, r, "", false, nil); e == nil || loginHits.Load() != 0 {
		t.Fatal("chain write escaped gate", e)
	}
	var approvals atomic.Int32
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{AuthorizeChainWrite: func(_ context.Context, request config.Request) (bool, error) {
		approvals.Add(1)
		if request.Endpoint != s.URL+"/login" || request.Action != "POST" {
			t.Error("confirmation did not receive exact resolved request")
		}
		return true, nil
	}})
	var events []Event
	if e = RunCollection(ctx, c, r, "", false, func(event Event) { events = append(events, event) }); e != nil {
		t.Fatal(e)
	}
	if loginHits.Load() != 1 || dataHits.Load() != 1 || approvals.Load() != 1 {
		t.Fatalf("counts %d %d %d", loginHits.Load(), dataHits.Load(), approvals.Load())
	}
	if len(events) != 1 {
		t.Fatal(events)
	}
	got := events[0].Data.(map[string]any)["body"].(map[string]any)["id"]
	if got != json.Number("9007199254740993") {
		t.Fatal(got)
	}
}
func TestSlumberResponseHistoryAndPersist(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("X-Demo", "header-value")
		io.WriteString(w, `{"token":"local-only"}`)
	}))
	defer s.Close()
	source := fmt.Sprintf(`version: 1
requests:
- id: upstream
  protocol: http
  action: GET
  endpoint: %s
- id: consumer
  protocol: http
  action: GET
  endpoint: "%s?token={{ response('upstream') | jq('.token') }}"
  params:
    headers: {X-Demo: "{{ response_header('upstream', 'x-demo') }}"}
`, s.URL, s.URL)
	c, e := ParseCollection([]byte(source))
	if e != nil {
		t.Fatal(e)
	}
	c.SourcePath = filepath.Join(t.TempDir(), "iotools.yaml")
	db := filepath.Join(t.TempDir(), "history.sqlite")
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{HistoryPath: db})
	if e = RunCollection(ctx, c, c.Requests[1], "", false, nil); e == nil || hits.Load() != 0 {
		t.Fatal("trigger never sent upstream")
	}
	if e = RunCollection(ctx, c, c.Requests[0], "", false, nil); e != nil {
		t.Fatal(e)
	}
	if e = RunCollection(ctx, c, c.Requests[1], "", false, nil); e != nil {
		t.Fatal(e)
	}
	if hits.Load() != 2 {
		t.Fatal("cached history sent extra requests")
	}
	rows, e := QueryHTTPHistory(context.Background(), db, "SELECT recipe, status FROM http_history ORDER BY id")
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	for _, sql := range []string{"DELETE FROM http_history", "WITH x AS (SELECT 1) DELETE FROM http_history", "SELECT 1; DELETE FROM http_history", "PRAGMA query_only=0"} {
		if _, e = QueryHTTPHistory(context.Background(), db, sql); e == nil {
			t.Fatalf("writable history console: %s", sql)
		}
	}
	c.Requests[0].Params = map[string]any{"persist": false}
	if e = RunCollection(ctx, c, c.Requests[0], "", false, nil); e != nil {
		t.Fatal(e)
	}
	rows, e = QueryHTTPHistory(context.Background(), db, "SELECT recipe FROM http_history")
	if e != nil || len(rows) != 2 {
		t.Fatal("persist false ignored", rows, e)
	}
	c.SourcePath += "other"
	if e = RunCollection(ctx, c, c.Requests[1], "", false, nil); e == nil {
		t.Fatal("history leaked across collections")
	}
}
func TestSlumberTypedBodyBinaryMultipartAndCrypto(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "sample.bin"), []byte{0, 255, 1}, 0600); e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			b, _ := io.ReadAll(r.Body)
			v, e := decodeJSON(b)
			if e != nil {
				t.Error(e)
			}
			m := v.(map[string]any)
			if m["n"] != json.Number("42") || m["encrypted"] != "ZGVtbw==" || len(m["array"].([]any)) != 2 {
				t.Error(m)
			}
			io.WriteString(w, base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)))
		case "/binary":
			b, _ := io.ReadAll(r.Body)
			if string(b) != string([]byte{0, 255, 1}) {
				t.Error(b)
			}
			io.WriteString(w, "ok")
		case "/multipart":
			if e := r.ParseMultipartForm(maxBody); e != nil {
				t.Error(e)
			}
			if r.FormValue("file") != string([]byte{0, 255, 1}) {
				t.Error("binary multipart")
			}
			io.WriteString(w, "ok")
		}
	}))
	defer s.Close()
	source := fmt.Sprintf(`crypto:
  basic: {algorithm: base64}
  unused: {algorithm: aes-128-cbc, key: '${env:IOTOOLS_MISSING_UNUSED}', iv: '${env:IOTOOLS_MISSING_UNUSED}'}
requests:
  json:
    method: POST
    url: %s/json
    body:
      type: json
      data: {n: '{{ integer("42") }}', array: '{{ [1, 2] }}', encrypted: "{{ 'demo' | encode('basic') }}"}
    response_transform: [{type: decode, crypto: basic, scope: body, parse: json}]
  binary:
    method: POST
    url: %s/binary
    body: "{{ file('sample.bin') }}"
  multipart:
    method: POST
    url: %s/multipart
    body: {type: form_multipart, data: {file: "{{ file('sample.bin') }}"}}
`, s.URL, s.URL, s.URL)
	c, e := ParseCollection([]byte(source))
	if e != nil {
		t.Fatal(e)
	}
	c.SourcePath = filepath.Join(root, "source.yaml")
	for _, r := range c.Requests {
		if e = RunCollection(context.Background(), c, r, "", true, nil); e != nil {
			t.Fatalf("%s: %v", r.ID, e)
		}
	}
}
func TestSlumberCyclesDefaultsAndBlanketChainPermission(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); io.WriteString(w, "upstream") }))
	defer s.Close()
	c := &config.Collection{Version: 1, Requests: []config.Request{{ID: "parent", Protocol: "http", Action: "GET", Endpoint: s.URL, Params: map[string]any{"headers": map[string]any{"X-Value": `{{ response('upstream', trigger='always') }}`}}}, {ID: "upstream", Protocol: "http", Action: "POST", Endpoint: s.URL}}}
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{AllowChainWrites: true})
	if e := RunCollection(ctx, c, c.Requests[0], "", false, nil); e == nil || hits.Load() != 0 {
		t.Fatal("allow chain alone authorized write")
	}
	if e := RunCollection(ctx, c, c.Requests[0], "", true, nil); e != nil || hits.Load() != 2 {
		t.Fatal(e, hits.Load())
	}
	c.Requests[1].Params = map[string]any{"body": `{{ response('parent', trigger='always') }}`}
	if e := RunCollection(ctx, c, c.Requests[0], "", true, nil); e == nil || !strings.Contains(e.Error(), "cyclic") {
		t.Fatal("cycle not detected", e)
	}
}

func TestSlumberRootWriteExactAuthorization(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); io.WriteString(w, "ok") }))
	defer s.Close()
	c := &config.Collection{Version: 1, DefaultProfile: "local", Profiles: map[string]map[string]string{"local": {"host": s.URL}}, Requests: []config.Request{{ID: "write", Protocol: "http", Action: "POST", Endpoint: "{{ host }}/exact", Params: map[string]any{"json": map[string]any{"n": "{{ integer('42') }}"}}}}}
	approved := false
	calls := 0
	opts := HTTPWorkflowOptions{AuthorizeRequestWrite: func(_ context.Context, r config.Request) (bool, error) {
		calls++
		if r.Endpoint != s.URL+"/exact" || r.Params["json"].(map[string]any)["n"] != json.Number("42") {
			t.Error("confirmation not fully resolved", r)
		}
		return approved, nil
	}}
	ctx := WithHTTPWorkflowOptions(context.Background(), opts)
	if e := RunCollection(ctx, c, c.Requests[0], "", true, nil); e == nil || hits.Load() != 0 || calls != 1 {
		t.Fatal("callback denial escaped", e, hits.Load(), calls)
	}
	approved = true
	if e := RunCollection(ctx, c, c.Requests[0], "", false, nil); e != nil || hits.Load() != 1 || calls != 2 {
		t.Fatal("exact callback did not authorize", e, hits.Load(), calls)
	}
}

func TestSlumberTransformedHistoryUsesCurrentRules(t *testing.T) {
	c := &config.Collection{Version: 1, Requests: []config.Request{{ID: "upstream", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:1", Params: map[string]any{"crypto": map[string]any{"b64": map[string]any{"algorithm": "base64"}}, "response_transform": []any{map[string]any{"type": "decode", "crypto": "b64", "scope": "body", "parse": "json"}}}}}}
	w := testWorkflow()
	w.collection = c
	w.responses = map[string]*HTTPHistoryEntry{"upstream": {Body: []byte("eyJvayI6dHJ1ZX0="), Transformed: []byte(`{"wrong":"stale"}`)}}
	got, e := w.render(`{{ response('upstream', view='transformed') | jq('.ok') }}`, true)
	if e != nil || got != true {
		t.Fatal("stale transformed cache used", got, e)
	}
	delete(c.Requests[0].Params, "response_transform")
	got, e = w.render(`{{ response('upstream', view='transformed') }}`, true)
	if e != nil || string(got.([]byte)) != "eyJvayI6dHJ1ZX0=" {
		t.Fatal("no transforms should return original bytes", got, e)
	}
}
