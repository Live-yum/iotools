package engine

import (
	"context"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func TestModbusExplicitMockReadWrite(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 244, "address": 120, "count": 2}}
	read := func(want uint16) {
		t.Helper()
		simulated, seen := false, false
		if e := Run(context.Background(), r, false, func(e Event) {
			if e.Kind == "simulation" {
				simulated = true
			}
			if e.Kind == "registers" {
				rows := e.Data.([]map[string]any)
				seen = rows[0]["u16"] == want && len(rows) == 2
			}
		}); e != nil {
			t.Fatal(e)
		}
		if !seen || !simulated {
			t.Fatal("missing explicit simulation label or value")
		}
	}
	// Clean this test's dedicated unit so count=2/race reruns are deterministic.
	localModbusMock.Lock()
	delete(localModbusMock.registers, uint32(244)<<16|120)
	localModbusMock.Unlock()
	read(120)
	r.Action = "write-register"
	r.Params["value"] = 900
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("simulator bypassed write confirmation")
	}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "read-holding"
	read(900)
	r.Action = "read-input"
	read(120)
	r.Endpoint = "mock://remote"
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("unknown mock endpoint accepted")
	}
}
func TestModbusMockCoilWritesAndUnits(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "write-coils", Endpoint: "mock://local", Params: map[string]any{"unit": 243, "address": 200, "values": []any{true, false, true}, "count": 3}}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "read-coils"
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "bits" {
			b := e.Data.(map[string]any)["values"].([]bool)
			seen = len(b) == 3 && b[0] && !b[1] && b[2]
		}
	}); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("coil round trip")
	}
	r.Params["unit"] = 242
	seen = false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "bits" {
			b := e.Data.(map[string]any)["values"].([]bool)
			seen = !b[0] && b[1] && !b[2]
		}
	}); e != nil {
		t.Fatal(e)
	}
	if !seen {
		t.Fatal("unit state leaked")
	}
	r.Action = "write-coils"
	r.Params["values"] = []any{"false"}
	if e := Run(context.Background(), r, true, nil); e == nil {
		t.Fatal("string boolean accepted")
	}
}
