package mobileapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

func TestIndependentOPCUASubscriptionAndRead(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	srv := server.New(server.EndPoint("127.0.0.1", port), server.EnableAuthMode(ua.UserTokenTypeAnonymous), server.EnableSecurity("None", ua.MessageSecurityModeNone))
	ns := server.NewMapNamespace(srv, "mobileapi-test")
	ns.Data["Value"] = int32(7)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e = srv.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	endpoint := fmt.Sprintf("opc.tcp://127.0.0.1:%d", port)
	node := ua.NewStringNodeID(ns.ID(), "Value").String()
	params := map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true, "node_id": node, "interval_ms": 100, "max_events": 1000}
	sub := config.Request{ID: "sub", Protocol: "opcua", Action: "subscribe", Endpoint: endpoint, Timeout: "20s", Params: params}
	read := config.Request{ID: "read", Protocol: "opcua", Action: "read", Endpoint: endpoint, Timeout: "5s", Params: map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true, "node_id": node}}
	s := testSession(t, sub, read)
	data := mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "sub")}).(map[string]any)
	id := data["subscription_id"].(string)
	if data["background"] != true {
		t.Fatal("subscription held foreground")
	}
	deadline := time.Now().Add(8 * time.Second)
	connected := false
	for time.Now().Before(deadline) {
		s.mu.Lock()
		last := s.subscriptions[id].LastEvent
		status := s.subscriptions[id].Status
		s.mu.Unlock()
		if last != nil {
			connected = true
			break
		}
		if status != "running" {
			t.Fatalf("subscription failed: %v", s.listSubscriptions())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !connected {
		t.Fatal("subscription never received a value")
	}
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "read")})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "completed" {
		t.Fatalf("concurrent read failed: %v", events)
	}
	if len(s.listConnections()) != 1 {
		t.Fatal("successful endpoint not recorded")
	}
	s.Pause()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.listSubscriptions()[0].Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.listSubscriptions()[0].Status == "running" {
		t.Fatal("background did not cancel subscription")
	}
	s.Resume()
	if s.listSubscriptions()[0].Status == "running" {
		t.Fatal("resume restarted subscription")
	}
}
func TestDiscoveryTokenAndSnapshotUtilities(t *testing.T) {
	s := testSession(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	data := mustOK(t, s, map[string]any{"op": "modbus.discovery.preview", "target": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port, "timeout_ms": 100, "concurrency": 1}).(map[string]any)
	token := data["token"].(string)
	mustOK(t, s, map[string]any{"op": "modbus.discovery.run", "token": token, "confirmed": true})
	events := awaitDone(t, s)
	found := false
	for _, ev := range events {
		if ev["kind"] == "discovery" && ev["data"].(map[string]any)["open"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("loopback discovery missing: %v", events)
	}
	if cmd(t, s, map[string]any{"op": "modbus.discovery.run", "token": token, "confirmed": true})["ok"] != false {
		t.Fatal("discovery replay accepted")
	}
	before := engine.RegisterSnapshot{Version: 1, Endpoint: "mock://local", Unit: 1, Action: "read-holding", Time: time.Now().UTC(), Values: map[int]uint16{0: 7}}
	mustOK(t, s, map[string]any{"op": "modbus.snapshot.save", "path": "snapshot.json", "snapshot": before, "confirmed": true})
	if cmd(t, s, map[string]any{"op": "modbus.snapshot.save", "path": "snapshot.json", "snapshot": before, "confirmed": true})["ok"] != false {
		t.Fatal("snapshot overwritten")
	}
	mustOK(t, s, map[string]any{"op": "modbus.snapshot.load", "path": "snapshot.json"})
	after := before
	after.Values = map[int]uint16{0: 8}
	changes := mustOK(t, s, map[string]any{"op": "modbus.snapshot.diff", "before": before, "after": after}).([]any)
	if len(changes) != 1 {
		t.Fatal(changes)
	}
}
func TestWirePreservesNestedIntegerPrecisionAndMasksCredentials(t *testing.T) {
	s := testSession(t, config.Request{ID: "precise", Protocol: "opcua", Action: "read", Endpoint: "opc.tcp://127.0.0.1", Params: map[string]any{"value": uint64(math.MaxUint64)}})
	s.emit("r", "typed", struct {
		Value any `json:"value"`
	}{map[string]any{"unsigned": uint64(math.MaxUint64), "signed": int64(math.MinInt64), "values": []uint64{9007199254740993}}})
	data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	event := data["events"].([]any)[0].(map[string]any)["data"].(map[string]any)["value"].(map[string]any)
	if event["unsigned"] != "18446744073709551615" || event["signed"] != "-9223372036854775808" || event["values"].([]any)[0] != "9007199254740993" {
		t.Fatal(event)
	}
	out := s.Command(`{"op":"preview","request":{"id":"secret","protocol":"http","action":"POST","endpoint":"https://example.invalid?access_token=never-display-query","params":{"password":"never-display-password","bearer":"never-display-bearer","headers":{"Authorization":"never-display-auth","Cookie":"never-display-cookie"},"crypto":{"key":{"value":"never-display-key"}}}}}`)
	for _, secret := range []string{"never-display-password", "never-display-bearer", "never-display-auth", "never-display-cookie", "never-display-key", "never-display-query"} {
		if strings.Contains(out, secret) {
			t.Fatalf("preview exposed %s", secret)
		}
	}
}
func TestConnectionHistoryNoSecretsAndHistoryReadonly(t *testing.T) {
	s := testSession(t)
	r := config.Request{Protocol: "opcua", Endpoint: "opc.tcp://127.0.0.1:4840", Params: map[string]any{"node_id": "ns=1;i=1", "username": "PRIVATE_USER", "password": "PRIVATE_PASSWORD", "bearer": "PRIVATE_TOKEN"}}
	s.recordConnection(r, engine.Event{Kind: "connected"})
	b, e := os.ReadFile(filepath.Join(s.root, "opcua-connections.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"PRIVATE_USER", "PRIVATE_PASSWORD", "PRIVATE_TOKEN"} {
		if strings.Contains(string(b), value) {
			t.Fatal("connection metadata leaked secrets")
		}
	}
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"read_only": true}})
	if cmd(t, s, map[string]any{"op": "history.delete", "ids": []int{1}, "confirmed": true})["ok"] != false {
		t.Fatal("readonly allowed delete")
	}
	if cmd(t, s, map[string]any{"op": "history.execute", "sql": "DELETE FROM http_history", "confirmed": true})["ok"] != false {
		t.Fatal("readonly allowed SQL writes")
	}
}
func TestSandboxHooksCoverCurlAndDynamicFile(t *testing.T) {
	s := testSession(t, config.Request{ID: "r", Protocol: "http", Action: "POST", Endpoint: "http://127.0.0.1:1", Params: map[string]any{"body": "{{ file('/etc/passwd') }}"}})
	if cmd(t, s, map[string]any{"op": "http.curl", "request_id": "r"})["ok"] != false {
		t.Fatal("curl read external file")
	}
	var parsed map[string]any
	if e := json.Unmarshal([]byte(s.Command(`{"op":"config.validate","source":"requests:\n  r:\n    $ref: /etc/passwd#/r\n"}`)), &parsed); e != nil {
		t.Fatal(e)
	}
	if parsed["ok"] != false {
		t.Fatal("external reference read accepted")
	}
}
