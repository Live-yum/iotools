package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func TestModbusObserverPerSampleWithoutOutputChanges(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Timeout: "2s", Params: map[string]any{"unit": 8, "address": 2, "count": 3, "samples": 3, "interval_ms": 40}}
	var operations []ModbusOperation
	var kinds []string
	ctx := WithModbusObserver(context.Background(), func(op ModbusOperation) { operations = append(operations, op) })
	start := time.Now()
	err := Run(ctx, r, false, func(e Event) { kinds = append(kinds, e.Kind) })
	elapsed := time.Since(start)
	if err != nil || len(operations) != 3 {
		t.Fatalf("%v %#v", err, operations)
	}
	var measured time.Duration
	for _, op := range operations {
		if !op.Success || op.Write || op.Count != 3 || op.Unit != 8 || op.Address != 2 || op.Duration < 0 {
			t.Fatalf("bad observation %#v", op)
		}
		measured += op.Duration
	}
	if elapsed-measured < 70*time.Millisecond {
		t.Fatal("sample waits counted as communication latency")
	}
	if len(kinds) != 4 || kinds[0] != "simulation" {
		t.Fatal("ordinary event stream changed", kinds)
	}
	for _, kind := range kinds[1:] {
		if kind != "registers" {
			t.Fatal("metrics injected into CLI stream")
		}
	}
}
func TestModbusObserverWriteGateAndLogicalOperations(t *testing.T) {
	var got []ModbusOperation
	ctx := WithModbusObserver(context.Background(), func(op ModbusOperation) { got = append(got, op) })
	r := config.Request{Protocol: "modbus", Action: "write-register", Endpoint: "mock://local", Params: map[string]any{"unit": 8, "address": 5, "value": 12, "samples": 5}}
	if err := Run(ctx, r, false, nil); err == nil || len(got) != 0 {
		t.Fatal("denied write reached observation/device")
	}
	if err := Run(ctx, r, true, nil); err != nil || len(got) != 1 || !got[0].Write || !got[0].Success {
		t.Fatal("write observation incorrect", err, got)
	}
	r.Action = "read-device-id"
	if err := Run(ctx, r, false, nil); err != nil || len(got) != 2 {
		t.Fatal("device ID not observed", err)
	}
	r.Action = "read-raw"
	r.Params["count"] = 1
	r.Params["pdu_hex"] = "0300050001"
	if err := Run(ctx, r, false, nil); err != nil || len(got) != 3 {
		t.Fatal("raw operation not observed", err)
	}
}
func TestModbusObserverSanitizedFailureAndCancellation(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "read-input", Endpoint: "tcp://sensitive.invalid:502", Params: map[string]any{"unit": 1, "address": 0, "count": 1}}
	var got ModbusOperation
	ctx := WithModbusObserver(context.Background(), func(op ModbusOperation) { got = op })
	observeModbusOperation(ctx, r, time.Now(), errors.New("secret username/token device body"))
	if got.Success || got.ErrorClass != "通信或响应校验失败" {
		t.Fatal(got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	observeModbusOperation(cancelled, r, time.Now(), errors.New("connection closed"))
	if !got.Cancelled || got.ErrorClass != "已取消" {
		t.Fatal("cancel not classified", got)
	}
	if ModbusErrorClass(context.DeadlineExceeded) != "超时" || ModbusErrorClass(nil) != "" {
		t.Fatal("error classification")
	}
}
