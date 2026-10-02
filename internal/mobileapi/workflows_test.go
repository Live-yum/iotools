package mobileapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
)

func TestHistoryCollectionPreviewMigrationRequiresExactBackup(t *testing.T) {
	s := testSession(t)
	h, e := engine.OpenHTTPHistory(filepath.Join(s.root, "history.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	for _, collection := range []string{s.path, "synthetic-old.yml"} {
		if e = h.Add(context.Background(), engine.HTTPHistoryEntry{Collection: collection, Recipe: "r", Method: "GET", Time: time.Now().UTC(), Status: 200, Body: []byte("synthetic")}); e != nil {
			t.Fatal(e)
		}
	}
	h.Close()
	preview := mustOK(t, s, map[string]any{"op": "history.collection.preview", "kind": "merge", "source": "synthetic-old.yml", "target": s.path}).(map[string]any)
	command := map[string]any{"op": "history.execute", "sql": preview["sql"], "token": preview["token"], "backup": "history-before-merge.sqlite", "confirmed": true}
	mustOK(t, s, command)
	rows := mustOK(t, s, map[string]any{"op": "history.collections"}).([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["requests"].(float64) != 2 {
		t.Fatal(rows)
	}
	if cmd(t, s, command)["ok"] != false {
		t.Fatal("stale SQL token replayed")
	}
	if _, e = os.Stat(filepath.Join(s.root, "history-before-merge.sqlite")); e != nil {
		t.Fatal("missing backup", e)
	}
}
func TestCollectionSwitchBindsExactPreviewAndReturnsPath(t *testing.T) {
	s := testSession(t)
	target := filepath.Join(s.root, "another.yaml")
	source := []byte("version: 1\nrequests: []\n")
	if e := os.WriteFile(target, source, 0600); e != nil {
		t.Fatal(e)
	}
	preview := mustOK(t, s, map[string]any{"op": "config.switch", "path": "another.yaml"}).(map[string]any)
	if e := os.WriteFile(target, append(source, []byte("# changed\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	if cmd(t, s, map[string]any{"op": "config.switch", "path": "another.yaml", "token": preview["token"], "confirmed": true})["ok"] != false {
		t.Fatal("changed preview accepted")
	}
	preview = mustOK(t, s, map[string]any{"op": "config.switch", "path": "another.yaml"}).(map[string]any)
	state := mustOK(t, s, map[string]any{"op": "config.switch", "path": "another.yaml", "token": preview["token"], "confirmed": true}).(map[string]any)
	if state["path"] != target {
		t.Fatal("active path absent", state)
	}
	sourceView := mustOK(t, s, map[string]any{"op": "config.get"}).(map[string]any)
	if sourceView["path"] != target {
		t.Fatal("source getter path incorrect")
	}
}
func TestCurlTriggersAreExplicitAndNeverExecuteRoot(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); fmt.Fprint(w, `{"token":"synthetic"}`) }))
	defer server.Close()
	s := testSession(t,
		config.Request{ID: "dependency", Protocol: "http", Action: "GET", Endpoint: server.URL + "/dependency"},
		config.Request{ID: "root", Protocol: "http", Action: "POST", Endpoint: server.URL + "/root", Params: map[string]any{"body": "{{ response('dependency', trigger='always') }}"}},
	)
	if cmd(t, s, map[string]any{"op": "http.curl", "request_id": "root"})["ok"] != false || hits.Load() != 0 {
		t.Fatal("default curl executed dependency")
	}
	if cmd(t, s, map[string]any{"op": "http.curl", "request_id": "root", "execute_triggers": true})["ok"] != false || hits.Load() != 0 {
		t.Fatal("trigger execution lacked confirmation")
	}
	mustOK(t, s, map[string]any{"op": "http.curl", "request_id": "root", "execute_triggers": true, "confirmed": true})
	events := awaitDone(t, s)
	if hits.Load() != 1 {
		t.Fatalf("curl root unexpectedly executed: %d", hits.Load())
	}
	found := false
	for _, ev := range events {
		if ev["kind"] == "curl" {
			found = true
		}
	}
	if !found {
		b, _ := json.Marshal(events)
		t.Fatalf("curl output missing %s", b)
	}
}
func TestDialogSurvivesEventOverflow(t *testing.T) {
	s := testSession(t)
	s.emit("foreground", "interaction", map[string]any{"interaction_id": "synthetic", "type": "confirm"})
	for i := 0; i < 1000; i++ {
		s.emit("sub", "notification", i)
	}
	data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	found := false
	for _, item := range data["events"].([]any) {
		if item.(map[string]any)["kind"] == "interaction" {
			found = true
		}
	}
	if !found {
		t.Fatal("required confirmation was dropped by subscription traffic")
	}
}
func TestCatalogAllActionsAndFieldsHaveChineseLabels(t *testing.T) {
	catalog := Catalog()
	for _, p := range catalog["protocols"].([]Protocol) {
		for _, action := range p.Actions {
			if actionLabels[action.ID] == "" {
				t.Fatalf("missing action label %s/%s", p.ID, action.ID)
			}
		}
		for _, field := range p.Fields {
			if fieldLabels[field.Key] == "" {
				t.Fatalf("missing field label %s/%s", p.ID, field.Key)
			}
		}
	}
}
