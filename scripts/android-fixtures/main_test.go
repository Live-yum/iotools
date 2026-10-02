package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
func TestDisposableOPCFixtureRealProtocol(t *testing.T) {
	opcPort, metricsPort := freePort(t), freePort(t)
	for opcPort == metricsPort {
		metricsPort = freePort(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := filepath.Join(t.TempDir(), "ready.json")
	done := make(chan error, 1)
	go func() { done <- run(ctx, ready, opcPort, metricsPort) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, e := os.Stat(ready); e == nil {
			break
		}
		select {
		case e := <-done:
			t.Fatal(e)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	endpoint := fmt.Sprintf("opc.tcp://127.0.0.1:%d", opcPort)
	invoke := func(action string, extra map[string]any) []engine.Event {
		t.Helper()
		params := map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true}
		for k, v := range extra {
			params[k] = v
		}
		var events []engine.Event
		e := engine.Run(ctx, config.Request{ID: "fixture", Protocol: "opcua", Action: action, Endpoint: endpoint, Timeout: "5s", Params: params}, true, func(e engine.Event) { events = append(events, e) })
		if e != nil {
			t.Fatalf("%s: %v", action, e)
		}
		return events
	}
	invoke("discover", nil)
	references := invoke("browse", map[string]any{"node_id": "ns=1;i=85"})
	found := 0
	for _, e := range references {
		if e.Kind == "reference" {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("want 3 fixture references, got %d", found)
	}
	invoke("attributes", map[string]any{"node_id": "ns=1;s=Temperature"})
	signature := invoke("method-arguments", map[string]any{"method_id": "ns=1;s=Double"})
	foundSignature := false
	for _, e := range signature {
		if e.Kind == "method-arguments" {
			body := e.Data.(map[string]any)
			if len(body["inputs"].([]engine.MethodArgument)) != 1 {
				t.Fatal(body)
			}
			foundSignature = true
		}
	}
	if !foundSignature {
		t.Fatal("missing signature")
	}
	metrics := func() map[string]int64 {
		t.Helper()
		r, e := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", metricsPort))
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		var result map[string]int64
		if e = json.NewDecoder(r.Body).Decode(&result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	if before := metrics(); before["writes"] != 0 || before["calls"] != 0 {
		t.Fatal("read-only discovery invoked mutation", before)
	}
	beforeInvalid := metrics()
	invalid := config.Request{ID: "invalid-typed", Protocol: "opcua", Action: "write", Endpoint: endpoint, Timeout: "5s", Params: map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true, "node_id": "ns=1;s=Temperature", "value_type": "Int32", "value": "2147483648"}}
	if err := engine.Run(ctx, invalid, true, nil); err == nil {
		t.Fatal("out-of-range Int32 accepted")
	}
	invalid.Action = "call"
	invalid.Params = map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true, "object_id": "ns=1;i=85", "method_id": "ns=1;s=Double", "arguments": []any{map[string]any{"type": "Int32", "value": "2147483648"}}}
	if err := engine.Run(ctx, invalid, true, nil); err == nil {
		t.Fatal("out-of-range typed method argument accepted")
	}
	for key, before := range beforeInvalid {
		if after := metrics()[key]; after != before {
			t.Fatalf("invalid typed value accessed protocol: %s %d -> %d", key, before, after)
		}
	}
	invoke("write", map[string]any{"node_id": "ns=1;s=Temperature", "value_type": "Int32", "value": "42"})
	readback := invoke("read", map[string]any{"node_id": "ns=1;s=Temperature"})
	foundReadback := false
	for _, event := range readback {
		if event.Kind == "value" {
			body := event.Data.(map[string]any)
			if body["value"] != int32(42) {
				t.Fatalf("write did not read back: %#v", body)
			}
			foundReadback = true
		}
	}
	if !foundReadback {
		t.Fatal("no value read back after confirmed write")
	}
	called := invoke("call", map[string]any{"object_id": "ns=1;i=85", "method_id": "ns=1;s=Double", "arguments": []any{map[string]any{"type": "Int32", "value": "6"}}})
	foundOutput := false
	for _, event := range called {
		if event.Kind == "method" {
			outputs := event.Data.(map[string]any)["outputs"].([]any)
			if len(outputs) != 1 || outputs[0] != int32(12) {
				t.Fatalf("Double(6) output: %#v", outputs)
			}
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatal("no method output returned")
	}
	invoke("subscribe", map[string]any{"node_id": "ns=1;s=Pressure", "interval_ms": 100, "max_events": 1})
	if after := metrics(); after["writes"] != 1 || after["calls"] != 1 {
		t.Fatal("real write/call counters", after)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not stop")
	}
	if _, e := os.Stat(ready); !os.IsNotExist(e) {
		t.Fatal("ready manifest was not removed")
	}
}
