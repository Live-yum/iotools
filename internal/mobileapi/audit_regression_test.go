package mobileapi

import (
	"context"
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMTUISelectedImportIsAtomicBoundAndPreservesRequests(t *testing.T) {
	s := testSession(t, config.Request{ID: "existing", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:1"})
	preview := mustOK(t, s, map[string]any{"op": "modbus.import", "source": "{}", "prefix": "device"}).(map[string]any)
	requests := preview["requests"].([]any)
	id := requests[0].(map[string]any)["id"].(string)
	if cmd(t, s, map[string]any{"op": "modbus.import.apply", "token": preview["token"], "request_ids": []string{id}, "confirmed": false})["ok"] != false {
		t.Fatal("unconfirmed import saved")
	}
	before := append([]byte(nil), s.source...)
	saved := mustOK(t, s, map[string]any{"op": "modbus.import.apply", "token": preview["token"], "request_ids": []string{id}, "confirmed": true}).(map[string]any)
	backup, err := os.ReadFile(saved["backup"].(string))
	if err != nil || string(backup) != string(before) {
		t.Fatal("import backup did not preserve exact original")
	}

	if len(s.collection.Requests) != 2 || s.collection.Requests[0].ID != "existing" || s.collection.Requests[1].ID != id {
		t.Fatal("import replaced existing collection or ignored selection")
	}
	if cmd(t, s, map[string]any{"op": "modbus.import.apply", "token": preview["token"], "request_ids": []string{id}, "confirmed": true})["ok"] != false {
		t.Fatal("replayed import token")
	}
	if cmd(t, s, map[string]any{"op": "modbus.import", "source": "{}", "prefix": "device"})["ok"] != false {
		t.Fatal("preview ignored ID collision")
	}
}
func TestSelectionIndexPreservesOriginalTypesWithoutWireAmbiguity(t *testing.T) {
	s := testSession(t, config.Request{ID: "r", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:1"})
	choices := []any{int64(2), true, map[string]any{"x": int64(3)}, uint64(18446744073709551615), "18446744073709551615", nil}
	for index := range choices {
		result := make(chan any, 1)
		errors := make(chan error, 1)
		go func() {
			value, err := s.workflowOptions("test", true).Select(context.Background(), "选择", choices)
			result <- value
			errors <- err
		}()
		var id any
		until := time.Now().Add(time.Second)
		for id == nil && time.Now().Before(until) {
			events := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)["events"].([]any)
			for _, item := range events {
				e := item.(map[string]any)
				if e["kind"] == "interaction" {
					id = e["data"].(map[string]any)["interaction_id"]
				}
			}
			if id == nil {
				time.Sleep(time.Millisecond)
			}
		}
		if id == nil {
			t.Fatal("no interaction")
		}
		mustOK(t, s, map[string]any{"op": "respond", "interaction_id": id, "confirmed": true, "selection_index": index})
		value := <-result
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		expected, _ := json.Marshal(choices[index])
		got, _ := json.Marshal(value)
		if string(got) != string(expected) {
			t.Fatalf("typed selection changed: %s != %s", got, expected)
		}
	}
}
func TestModbusProvenancePreviewAndSessionCounters(t *testing.T) {
	r := config.Request{ID: "a", Protocol: "modbus", Action: "write-typed", Endpoint: "mock://local", Params: map[string]any{"unit": 9, "address": 3, "value_type": "u32", "word_order": "ABCD", "value": "65538"}}
	s := testSession(t, r)
	p := mustOK(t, s, map[string]any{"op": "preview", "request_id": "a"}).(map[string]any)
	encoded := p["protocol_preview"].(map[string]any)["registers"].([]any)
	if len(encoded) != 2 || encoded[0].(float64) != 1 || encoded[1].(float64) != 2 {
		t.Fatalf("wrong encoded confirmation %v", encoded)
	}
	mustOK(t, s, map[string]any{"op": "run", "token": p["token"], "confirmed": true})
	events := awaitDone(t, s)
	b, _ := json.Marshal(events)
	if !strings.Contains(string(b), `"source":{"action":"write-typed","endpoint":"mock://local","request_id":"a","unit":9}`) {
		t.Fatalf("missing resolved source %s", b)
	}
	s.observeModbus("synthetic", engine.ModbusOperation{Success: true})
	s.observeModbus("synthetic", engine.ModbusOperation{Cancelled: true})
	totals := mustOK(t, s, map[string]any{"op": "modbus.stats"}).(map[string]any)["session"].(map[string]any)
	if totals["writes"].(float64) != 1 || totals["reads"].(float64) != 2 || totals["cancelled"].(float64) != 1 {
		t.Fatalf("session counters %v", totals)
	}
}

func TestMTUIBackupFailureLeavesSourceUntouched(t *testing.T) {
	s := testSession(t, config.Request{ID: "existing", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:1"})
	preview := mustOK(t, s, map[string]any{"op": "modbus.import", "source": "{}", "prefix": "device"}).(map[string]any)
	token := preview["token"].(string)
	id := preview["requests"].([]any)[0].(map[string]any)["id"].(string)
	before := string(s.source)
	if err := os.WriteFile(s.path+".before-import-"+token[:12]+".bak", []byte("existing backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if cmd(t, s, map[string]any{"op": "modbus.import.apply", "token": token, "request_ids": []string{id}, "confirmed": true})["ok"] != false {
		t.Fatal("overwrote backup")
	}
	after, err := os.ReadFile(s.path)
	if err != nil || string(after) != before {
		t.Fatal("backup failure modified source")
	}
}

func TestLocalProfileFallbackAfterSaveAndRotation(t *testing.T) {
	s := testSession(t, config.Request{ID: "r", Protocol: "http", Action: "GET", Endpoint: "http://127.0.0.1:1"})
	source := "version: 1\nprofiles:\n  local: {base: 'http://127.0.0.1:2'}\nrequests:\n  - {id: r, protocol: http, action: GET, endpoint: '${base}/saved'}\n"
	mustOK(t, s, map[string]any{"op": "config.save", "source": source})
	if s.profile != "local" {
		t.Fatal("save lost local default")
	}
	preview := mustOK(t, s, map[string]any{"op": "preview", "request_id": "r"}).(map[string]any)
	if preview["request"].(map[string]any)["endpoint"] != "http://127.0.0.1:2/saved" {
		t.Fatal("save profile unresolved")
	}
	next := s.root + "/next.yaml"
	if e := os.WriteFile(next, []byte(strings.ReplaceAll(source, "saved", "rotated")), 0600); e != nil {
		t.Fatal(e)
	}
	plan := mustOK(t, s, map[string]any{"op": "config.switch", "path": next}).(map[string]any)
	mustOK(t, s, map[string]any{"op": "config.switch", "path": next, "token": plan["token"], "confirmed": true})
	if s.profile != "local" {
		t.Fatal("rotation lost local default")
	}
	preview = mustOK(t, s, map[string]any{"op": "preview", "request_id": "r"}).(map[string]any)
	if preview["request"].(map[string]any)["endpoint"] != "http://127.0.0.1:2/rotated" {
		t.Fatal("rotated profile unresolved")
	}
}

func TestNativeJSONFilterCanBeCancelled(t *testing.T) {
	s := testSession(t)
	started := mustOK(t, s, map[string]any{"op": "http.filter.start", "query": "range(0; 1000000000) | select(. == -1)", "data": "{}"}).(map[string]any)
	time.Sleep(10 * time.Millisecond)
	mustOK(t, s, map[string]any{"op": "cancel", "run_id": started["run_id"]})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "cancelled" {
		t.Fatal("query cancellation did not reach engine")
	}
}

func TestConnectionMetadataOmitsURIUserinfoAndQuerySecrets(t *testing.T) {
	s := testSession(t)
	s.recordConnection(config.Request{Protocol: "opcua", Endpoint: "opc.tcp://user:PRIVATE_PASSWORD@127.0.0.1:4840/UA?token=PRIVATE_QUERY", Params: map[string]any{"password": "PRIVATE_PARAM"}}, engine.Event{Kind: "connected"})
	data, err := os.ReadFile(s.connectionPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_") || strings.Contains(string(data), "user:") {
		t.Fatal("URI credentials persisted")
	}
	if !strings.Contains(string(data), "opc.tcp://127.0.0.1:4840/UA") {
		t.Fatal("server path lost")
	}
}
