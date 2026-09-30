package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"testing"
)

func TestRejectUnsafeParametersBeforeConnecting(t *testing.T) {
	for _, p := range []map[string]any{{"address": 1.9, "value": 1}, {"address": "oops", "value": 1}, {"adress": 5, "value": 1}, {"value": 1.5}, {"value": -1}} {
		r := config.Request{Protocol: "modbus", Action: "write-register", Endpoint: "tcp://127.0.0.1:1", Params: p}
		if e := validateParams(r); e == nil {
			t.Errorf("accepted malformed write: %v", p)
		}
	}
	for _, v := range []any{nil, "true", 1} {
		r := config.Request{Protocol: "modbus", Action: "write-coil", Endpoint: "tcp://127.0.0.1:1", Params: map[string]any{"value": v}}
		if e := validateParams(r); e == nil {
			t.Errorf("accepted malformed coil: %v", v)
		}
	}
	r := config.Request{Protocol: "modbus", Action: "write-register", Params: map[string]any{"address": "123", "value": "42"}}
	if e := validateParams(r); e != nil {
		t.Fatal(e)
	}
	if r.Int("address", 0) != 123 || r.Int("value", 0) != 42 {
		t.Fatal("exact decimal profile strings not resolved")
	}
	r.Params["count"] = 1.2
	if e := Run(context.Background(), r, true, nil); e == nil {
		t.Fatal("Run skipped validation")
	}
}
