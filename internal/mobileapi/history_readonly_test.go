package mobileapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func runHistoryReadOnlyFixture(t *testing.T, s *Session, id string) []map[string]any {
	t.Helper()
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, id)})
	s.mu.Lock()
	done := s.runDone
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("HTTP history fixture did not finish")
	}
	events := awaitDone(t, s)
	last := events[len(events)-1]
	if last["kind"] != "done" || last["data"].(map[string]any)["status"] != "completed" {
		t.Fatalf("read-only GET must still complete: %v", events)
	}
	return events
}

func checkHistoryReadOnlyStatus(t *testing.T, events []map[string]any, want bool) {
	t.Helper()
	count := 0
	for _, event := range events {
		if event["kind"] != "history_status" {
			continue
		}
		count++
		data := event["data"].(map[string]any)
		if data["recorded"] != false || data["reason"] != "read_only" || data["message"] != "只读保护，本次未记录" {
			t.Fatalf("incorrect read-only history status: %v", data)
		}
	}
	if (want && count != 1) || (!want && count != 0) {
		t.Fatalf("history status count=%d, want read-only status=%v", count, want)
	}
}

func TestReadOnlyHTTPHistorySkipsNewDatabaseAndResumesFutureRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("synthetic read-only response"))
	}))
	defer server.Close()
	s := testSession(t, config.Request{ID: "get", Protocol: "http", Action: "GET", Endpoint: server.URL})
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true, "read_only": true}})
	events := runHistoryReadOnlyFixture(t, s, "get")
	checkHistoryReadOnlyStatus(t, events, true)
	if _, err := os.Stat(filepath.Join(s.root, "history.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("read-only GET created history database: %v", err)
	}
	status := mustOK(t, s, map[string]any{"op": "history.status"}).(map[string]any)
	if status["enabled"] != true || status["exists"] != false {
		t.Fatalf("read-only must preserve history preference: %v", status)
	}
	if rows := mustOK(t, s, map[string]any{"op": "history.list"}).([]any); len(rows) != 0 {
		t.Fatalf("unexpected read-only history: %v", rows)
	}
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true, "read_only": false}})
	events = runHistoryReadOnlyFixture(t, s, "get")
	checkHistoryReadOnlyStatus(t, events, false)
	if rows := mustOK(t, s, map[string]any{"op": "history.list"}).([]any); len(rows) != 1 {
		t.Fatalf("expected only the later response, no backfill: %v", rows)
	}
}

func TestReadOnlyHTTPHistoryKeepsExistingRowsReadableWithoutWriting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("existing synthetic response"))
	}))
	defer server.Close()
	s := testSession(t, config.Request{ID: "get", Protocol: "http", Action: "GET", Endpoint: server.URL})
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true}})
	runHistoryReadOnlyFixture(t, s, "get")
	rows := mustOK(t, s, map[string]any{"op": "history.list"}).([]any)
	if len(rows) != 1 {
		t.Fatalf("fixture did not persist first response: %v", rows)
	}
	path := filepath.Join(s.root, "history.sqlite")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true, "read_only": true}})
	checkHistoryReadOnlyStatus(t, runHistoryReadOnlyFixture(t, s, "get"), true)
	if after := mustOK(t, s, map[string]any{"op": "history.list"}).([]any); len(after) != 1 {
		t.Fatalf("read-only run changed history rows: %v", after)
	}
	detail := mustOK(t, s, map[string]any{"op": "history.get", "history_id": rows[0].(map[string]any)["id"]}).(map[string]any)
	if detail["body"] != "existing synthetic response" {
		t.Fatalf("existing response unreadable under read-only: %v", detail)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only run changed database bytes: %v", err)
	}
}

func TestHTTPHistoryExplicitOffAndPersistFalseKeepPrecedence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("synthetic excluded response"))
	}))
	defer server.Close()
	cases := []struct {
		name     string
		history  bool
		readOnly bool
		params   map[string]any
	}{
		{name: "history-off"},
		{name: "history-off-readonly", readOnly: true},
		{name: "persist-false", history: true, params: map[string]any{"persist": false}},
		{name: "persist-false-readonly", history: true, readOnly: true, params: map[string]any{"persist": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testSession(t, config.Request{ID: "skip", Protocol: "http", Action: "GET", Endpoint: server.URL, Params: tc.params})
			mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": tc.history, "read_only": tc.readOnly}})
			checkHistoryReadOnlyStatus(t, runHistoryReadOnlyFixture(t, s, "skip"), false)
			if rows := mustOK(t, s, map[string]any{"op": "history.list"}).([]any); len(rows) != 0 {
				t.Fatalf("excluded response recorded a row: %v", rows)
			}
			status := mustOK(t, s, map[string]any{"op": "history.status"}).(map[string]any)
			if status["enabled"] != tc.history {
				t.Fatalf("saved history preference changed: %v", status)
			}
		})
	}
}

func TestReadOnlyHTTPWorkflowReusesCachedResponseWithoutWritingOrRetriggering(t *testing.T) {
	var upstreamHits, consumerHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upstream" {
			upstreamHits.Add(1)
			_, _ = w.Write([]byte(`{"token":"cached-fixture-token"}`))
			return
		}
		consumerHits.Add(1)
		if r.URL.Query().Get("token") != "cached-fixture-token" {
			t.Error("cached response token was not resolved")
		}
		_, _ = w.Write([]byte("consumer response must not be recorded"))
	}))
	defer server.Close()
	s := testSession(t,
		config.Request{ID: "upstream", Protocol: "http", Action: "GET", Endpoint: server.URL + "/upstream"},
		config.Request{ID: "consumer", Protocol: "http", Action: "GET", Endpoint: server.URL + `/consumer?token={{ response('upstream', trigger='never') | jq('.token') }}`},
	)
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true}})
	runHistoryReadOnlyFixture(t, s, "upstream")
	path := filepath.Join(s.root, "history.sqlite")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true, "read_only": true}})
	curl := mustOK(t, s, map[string]any{"op": "http.curl", "request_id": "consumer"}).(map[string]any)
	if !strings.Contains(curl["curl"].(string), "cached-fixture-token") || consumerHits.Load() != 0 || upstreamHits.Load() != 1 {
		t.Fatalf("read-only curl must only read cached data: %v", curl)
	}
	checkHistoryReadOnlyStatus(t, runHistoryReadOnlyFixture(t, s, "consumer"), true)
	if upstreamHits.Load() != 1 || consumerHits.Load() != 1 {
		t.Fatal("cached upstream unexpectedly retriggered")
	}
	if rows := mustOK(t, s, map[string]any{"op": "history.list"}).([]any); len(rows) != 1 {
		t.Fatalf("read-only consumer wrote history: %v", rows)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("cached read changed database: %v", err)
	}
	afterMode, err := os.Stat(path)
	if err != nil || mode.Mode() != afterMode.Mode() {
		t.Fatalf("cached read changed file mode: %v", err)
	}
}

func TestReadOnlyNoticeRequiresActualResponseCandidate(t *testing.T) {
	// Invalid local URL is rejected before a response exists; no misleading notice.
	s := testSession(t, config.Request{ID: "bad", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:1", Timeout: "100ms"})
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true, "read_only": true}})
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "bad")})
	s.mu.Lock()
	done := s.runDone
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("fixture timed out")
	}
	events := awaitDone(t, s)
	checkHistoryReadOnlyStatus(t, events, false)
}
