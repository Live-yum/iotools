package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestModbusWriteAuditPairsAndGates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writes.jsonl")
	r := config.Request{Protocol: "modbus", Action: "write-register", Endpoint: "mock://local", Params: map[string]any{"unit": 240, "address": 23, "value": 44, "write_log_file": path}}
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("write gate bypassed")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("rejected write created log")
	}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	entries, e := ReadModbusWriteLog(path)
	if e != nil || len(entries) != 2 {
		t.Fatal(entries, e)
	}
	if entries[0].OperationID != entries[1].OperationID || entries[0].Phase != "attempt" || entries[1].Status != "success" || entries[0].Previous != nil {
		t.Fatal(entries)
	}
	r.Params["write_log_file"] = filepath.Join(t.TempDir(), "absent", "write.jsonl")
	r.Params["value"] = 55
	if e := Run(context.Background(), r, true, nil); e == nil {
		t.Fatal("unaudited write executed")
	}
	r.Action = "read-holding"
	r.Params["count"] = 1
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "registers" {
			if e.Data.([]map[string]any)[0]["u16"] != uint16(44) {
				t.Error("failed log still changed target")
			}
		}
	}); e != nil {
		t.Fatal(e)
	}
}

func TestWriteLogRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if e := os.WriteFile(path, []byte("{}{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadModbusWriteLog(path); e == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestFC23RequiresExplicitValidatedReadWriteScope(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "read-write-registers", Endpoint: "mock://local", Params: map[string]any{"unit": 1, "address": 0, "read_address": 0, "read_count": 2, "values": []any{1, 2}}}
	if !r.Mutates() {
		t.Fatal("FC23 misclassified as read")
	}
	if err := validateParams(r); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), r, false, func(Event) {}); err == nil {
		t.Fatal("FC23 bypassed write gate")
	}
	for key, value := range map[string]any{"read_address": 65536, "read_count": 1.5, "values": []any{-1}} {
		copy := r
		copy.Params = cloneHTTPValue(r.Params).(map[string]any)
		copy.Params[key] = value
		if validateParams(copy) == nil {
			t.Fatal("invalid FC23 accepted", key)
		}
	}
}
