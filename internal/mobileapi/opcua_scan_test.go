package mobileapi

import (
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func TestCatalogOPCUAScan(t *testing.T) {
	for _, protocol := range Catalog()["protocols"].([]Protocol) {
		if protocol.ID != "opcua" {
			continue
		}
		var scan *Action
		for i := range protocol.Actions {
			if protocol.Actions[i].ID == "scan" {
				scan = &protocol.Actions[i]
			}
		}
		if scan == nil {
			t.Fatal("OPC UA scan missing from catalog")
		}
		if scan.Mutates {
			t.Fatal("scan must remain read-only")
		}
		for key, want := range map[string]any{"node_id": "i=85", "max_depth": 0, "max_nodes": 10000, "max_references": 1000} {
			if got := scan.Defaults[key]; got != want {
				t.Errorf("scan default %s = %v, want %v", key, got, want)
			}
		}
		fields := map[string]Field{}
		for _, field := range protocol.Fields {
			fields[field.Key] = field
		}
		for _, key := range []string{"max_depth", "max_nodes", "max_references"} {
			if field, ok := fields[key]; !ok || field.Type != "number" {
				t.Errorf("scan limit %s must have a numeric catalog field, got %+v", key, field)
			}
		}
		return
	}
	t.Fatal("OPC UA protocol missing from catalog")
}

func TestOPCUAScanCollectionAndPreview(t *testing.T) {
	r := config.Request{ID: "scan", Protocol: "opcua", Action: "scan", Endpoint: "opc.tcp://127.0.0.1:4840", Params: map[string]any{"node_id": "i=85", "max_depth": 0, "max_nodes": 200, "max_references": 100}}
	s := testSession(t, r)
	s.options.ReadOnly = true
	source := string(s.source)
	mustOK(t, s, map[string]any{"op": "config.validate", "source": source})
	mustOK(t, s, map[string]any{"op": "config.import", "format": "iotools", "source": source})
	mustOK(t, s, map[string]any{"op": "config.save", "source": source})
	preview := mustOK(t, s, map[string]any{"op": "preview", "request_id": r.ID}).(map[string]any)
	if preview["mutates"] != false || preview["confirmation_required"] != false {
		t.Fatalf("scan preview must be read-only: %v", preview)
	}
	request := preview["request"].(map[string]any)
	params := request["params"].(map[string]any)
	for key, want := range map[string]float64{"max_depth": 0, "max_nodes": 200, "max_references": 100} {
		if got := params[key]; got != want {
			t.Errorf("preview lost scan limit %s: got %v, want %v", key, got, want)
		}
	}
}
