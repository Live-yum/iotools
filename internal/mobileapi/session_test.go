package mobileapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
)

func testSession(t *testing.T, requests ...config.Request) *Session {
	t.Helper()
	root := t.TempDir()
	c := config.Collection{Version: 1, Requests: requests}
	b, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "collection.yaml")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(path, "test", Options{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func cmd(t *testing.T, s *Session, v any) map[string]any {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]any
	if e = json.Unmarshal([]byte(s.Command(string(b))), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func mustOK(t *testing.T, s *Session, v any) any {
	t.Helper()
	out := cmd(t, s, v)
	if out["ok"] != true {
		t.Fatalf("command failed: %v", out)
	}
	return out["data"]
}
func previewToken(t *testing.T, s *Session, id string) string {
	t.Helper()
	return mustOK(t, s, map[string]any{"op": "preview", "request_id": id}).(map[string]any)["token"].(string)
}
func awaitDone(t *testing.T, s *Session) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	events := []map[string]any{}
	for time.Now().Before(deadline) {
		data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
		for _, v := range data["events"].([]any) {
			ev := v.(map[string]any)
			events = append(events, ev)
			if ev["kind"] == "done" {
				return events
			}
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("run did not finish")
	return nil
}
func TestNoNetworkUntilExplicitRunAndSingleUseWrites(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer server.Close()
	s := testSession(t, config.Request{ID: "read", Protocol: "http", Action: "GET", Endpoint: server.URL}, config.Request{ID: "write", Protocol: "http", Action: "POST", Endpoint: server.URL, Params: map[string]any{"body": "hello"}})
	mustOK(t, s, map[string]any{"op": "state"})
	mustOK(t, s, map[string]any{"op": "catalog"})
	token := previewToken(t, s, "write")
	if requests.Load() != 0 {
		t.Fatal("startup/preview performed I/O")
	}
	if cmd(t, s, map[string]any{"op": "run", "token": token})["ok"] != false {
		t.Fatal("unconfirmed write permitted")
	}
	mustOK(t, s, map[string]any{"op": "run", "token": token, "confirmed": true})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "completed" {
		t.Fatalf("run failed: %v", events)
	}
	if requests.Load() != 1 {
		t.Fatal("wrong request count")
	}
	if cmd(t, s, map[string]any{"op": "run", "token": token, "confirmed": true})["ok"] != false {
		t.Fatal("write replay permitted")
	}
}
func TestPauseCancelsAndResumeDoesNotReplay(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); close(entered); <-r.Context().Done() }))
	defer server.Close()
	s := testSession(t, config.Request{ID: "read", Protocol: "http", Action: "GET", Endpoint: server.URL, Timeout: "1m"})
	token := previewToken(t, s, "read")
	mustOK(t, s, map[string]any{"op": "run", "token": token})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("server not reached")
	}
	s.Pause()
	awaitDone(t, s)
	s.Resume()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("resume replayed operation")
	}
	if cmd(t, s, map[string]any{"op": "run", "token": token})["ok"] != false {
		t.Fatal("old preview survived pause")
	}
}
func TestConfigEditsConflictAndPrivateFiles(t *testing.T) {
	s := testSession(t, config.Request{ID: "r", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1"})
	old := string(s.source)
	if e := os.WriteFile(s.path, []byte(old+"\n# external\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if cmd(t, s, map[string]any{"op": "config.save", "source": old})["ok"] != false {
		t.Fatal("stale overwrite accepted")
	}
	if cmd(t, s, map[string]any{"op": "file.read", "path": "../outside"})["ok"] != false {
		t.Fatal("file path escaped root")
	}
	if e := os.Symlink(os.TempDir(), filepath.Join(s.root, "linked")); e != nil {
		t.Fatal(e)
	}
	if cmd(t, s, map[string]any{"op": "file.read", "path": "linked/anything"})["ok"] != false {
		t.Fatal("symlink accepted")
	}
	if cmd(t, s, map[string]any{"op": "config.save", "source": "invalid:"})["ok"] != false {
		t.Fatal("invalid collection accepted")
	}
}
func TestReadonlyPrecisionAndMockModbus(t *testing.T) {
	s := testSession(t, config.Request{ID: "read", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 1, "address": 0, "count": 2}})
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "read")})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "completed" {
		t.Fatalf("mock operation failed: %v", events)
	}
	input := `{"op":"preview","request":{"id":"precision","protocol":"opcua","action":"write","endpoint":"opc.tcp://127.0.0.1:4840","params":{"value_type":"UInt64","value":18446744073709551615,"node_id":"ns=1;i=1"}}}`
	var out map[string]any
	if e := json.Unmarshal([]byte(s.Command(input)), &out); e != nil {
		t.Fatal(e)
	}
	if out["ok"] != true {
		t.Fatal(out)
	}
	value := out["data"].(map[string]any)["request"].(map[string]any)["params"].(map[string]any)["value"]
	if value != "18446744073709551615" {
		t.Fatalf("lost integer precision: %v", value)
	}
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"read_only": true}})
	if strings.Contains(s.Command(input), `"ok":true`) {
		t.Fatal("readonly preview permitted write")
	}
}
func TestEventBoundsAndUnknownCommands(t *testing.T) {
	s := testSession(t)
	for i := 0; i < 2000; i++ {
		s.emit("id", "value", map[string]any{"value": i})
	}
	s.emit("id", "large", strings.Repeat("x", 1<<20))
	s.mu.Lock()
	if len(s.events) > maxQueueEvents || s.eventBytes > maxQueueBytes {
		t.Fatal("event queue unbounded")
	}
	s.mu.Unlock()
	data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	if data["dropped"].(float64) == 0 {
		t.Fatal("missing overflow counter")
	}
	if strings.Contains(s.Command(`{"op":"state","mystery":1}`), `"ok":true`) {
		t.Fatal("unknown command field accepted")
	}
	if strings.Contains(s.Command(`{"op":"state"} {}`), `"ok":true`) {
		t.Fatal("trailing command accepted")
	}
}
func TestHTTPDynamicFileSandboxBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	s := testSession(t, config.Request{ID: "file", Protocol: "http", Action: "POST", Endpoint: server.URL, Params: map[string]any{"body": "{{ file('/etc/passwd') }}"}})
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "file"), "confirmed": true})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "failed" || calls.Load() != 0 {
		t.Fatalf("sandbox failed: %v", events)
	}
}
