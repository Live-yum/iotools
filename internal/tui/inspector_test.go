package tui

import (
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/twmb/franz-go/pkg/kadm"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMQTTTopicTreeUpdatesInPlace(t *testing.T) {
	u, s := newTestUI(t)
	v := u.inspector
	v.reset(config.Request{Protocol: "mqtt", Action: "subscribe", Endpoint: "mqtt://localhost"})
	for _, payload := range []string{"20", "21"} {
		v.add(engine.Event{Kind: "message", Data: map[string]any{"topic": "factory/room/temperature", "payload": payload, "qos": byte(1), "retained": true}})
	}
	if len(v.topics) != 3 {
		t.Fatalf("unexpected nodes %d", len(v.topics))
	}
	if !strings.Contains(v.topics["/factory/room/temperature"].GetText(), "21") {
		t.Fatal("latest value missing")
	}
	u.App.ForceDraw()
	if !strings.Contains(snapshot(s), "MQTT topic tree") {
		t.Fatal("topic tree not rendered")
	}
}
func TestOPCUANodeReferencesRemainNavigable(t *testing.T) {
	u, _ := newTestUI(t)
	v := u.inspector
	v.reset(config.Request{Protocol: "opcua", Action: "browse", Endpoint: "opc.tcp://localhost", Params: map[string]any{"node_id": "i=85"}})
	v.add(engine.Event{Kind: "reference", Data: &ua.ReferenceDescription{NodeID: ua.NewExpandedNodeID(ua.NewNumericNodeID(2, 123), "", 0), DisplayName: ua.NewLocalizedText("Temperature")}})
	children := v.root.GetChildren()
	if len(children) != 1 || children[0].GetReference() != "ns=2;i=123" {
		t.Fatalf("node reference lost: %v", children)
	}
}
func TestModbusRegisterPinsTrendsAndSnapshot(t *testing.T) {
	u, s := newTestUI(t)
	v := u.inspector
	v.reset(config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://localhost:502"})
	for _, value := range []uint16{10, 15, 20} {
		v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 0, "u16": value, "i16": int16(value), "hex": "0x0014", "f32": "1"}}})
	}
	if v.table.GetCell(1, 7).Text != "+10" || spark(v.history[0]) != "▁▄█" {
		t.Fatal("trend/snapshot incorrect")
	}
	v.pins[0] = true
	v.labels[0] = "Temp"
	v.filtered = true
	v.renderRegisters()
	u.App.ForceDraw()
	if !strings.Contains(snapshot(s), "Temp") {
		t.Fatal("label not visible")
	}
	v.reset(config.Request{Protocol: "modbus", Endpoint: "tcp://other:502"})
	if len(v.labels) != 0 || len(v.pins) != 0 {
		t.Fatal("labels leaked across devices")
	}
}
func TestKafkaTopicTableHasRawTopicReference(t *testing.T) {
	u, _ := newTestUI(t)
	v := u.inspector
	v.reset(config.Request{Protocol: "kafka", Action: "topics"})
	v.add(engine.Event{Kind: "topics", Data: kadm.TopicDetails{"events[blue]": {Topic: "events[blue]", Partitions: map[int32]kadm.PartitionDetail{0: {Partition: 0}}}}})
	if v.table.GetCell(1, 0).GetReference() != "events[blue]" {
		t.Fatal("topic reference altered by markup escaping")
	}
}
func TestTUIRepeatedRunAndQuitCancelsActiveRequest(t *testing.T) {
	u, _ := newTestUI(t)
	var hits atomic.Int32
	arrived := make(chan struct{})
	var arriving sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		arriving.Do(func() { close(arrived) })
		<-r.Context().Done()
	}))
	defer server.Close()
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(_ tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("TUI did not start")
	}
	r := config.Request{ID: "cancel-test", Protocol: "http", Action: "GET", Endpoint: server.URL, Timeout: "5s"}
	u.App.QueueUpdateDraw(func() {
		u.collection.Requests = []config.Request{r}
		u.populate("")
		u.selected = 0
		u.execute()
		u.execute()
	})
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	u.App.QueueUpdateDraw(func() { u.quit() })
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quit did not cancel active request")
	}
	if hits.Load() != 1 {
		t.Fatalf("repeated run started %d requests", hits.Load())
	}
}
