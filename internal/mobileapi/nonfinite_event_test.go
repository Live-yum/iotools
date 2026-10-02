package mobileapi

import (
	"math"
	"testing"
)

func TestOPCNonFiniteReadValuesPreserveTypeAndStatus(t *testing.T) {
	s := testSession(t)
	s.emit("fixture", "value", normalizeEvent(map[string]any{"node_id": "ns=1;s=Quality", "value_type_name": "Double[]", "status": "The operation succeeded. StatusGood (0x0)", "value": []float64{1.25, math.NaN(), math.Inf(1), math.Inf(-1)}, "method_outputs": []any{float32(math.NaN()), []float32{2.5, float32(math.Inf(-1))}}}))
	data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	events := data["events"].([]any)
	if len(events) != 1 {
		t.Fatal("missing event")
	}
	e := events[0].(map[string]any)
	if e["kind"] != "value" {
		t.Fatalf("legitimate read was dropped: %v", e["kind"])
	}
	value := e["data"].(map[string]any)
	if value["node_id"] != "ns=1;s=Quality" || value["value_type_name"] != "Double[]" || value["status"] == nil {
		t.Fatal("typed metadata lost")
	}
	array := value["value"].([]any)
	if len(array) != 4 || array[0] != 1.25 || array[1] != "NaN" || array[2] != "+Infinity" || array[3] != "-Infinity" {
		t.Fatalf("float array positions changed: %v", array)
	}
	outputs := value["method_outputs"].([]any)
	if outputs[0] != "NaN" || outputs[1].([]any)[0] != 2.5 || outputs[1].([]any)[1] != "-Infinity" {
		t.Fatal("nested Float values lost")
	}
}
