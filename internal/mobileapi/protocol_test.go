package mobileapi

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/twmb/franz-go/pkg/kfake"
)

func TestMQTTNativeCommandsRealLoopback(t *testing.T) {
	broker := mqtt.New(&mqtt.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if e := broker.AddHook(new(auth.AllowHook), nil); e != nil {
		t.Fatal(e)
	}
	listener := listeners.NewTCP(listeners.Config{ID: "mobile-test", Address: "127.0.0.1:0"})
	if e := broker.AddListener(listener); e != nil {
		t.Fatal(e)
	}
	if e := broker.Serve(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		listener.Close(func(string) {
			for _, client := range broker.Clients.GetAll() {
				client.Stop(nil)
			}
		})
		_ = broker.Close()
	}()
	endpoint := "mqtt://" + listener.Address()
	s := testSession(t,
		config.Request{ID: "publish", Protocol: "mqtt", Action: "publish", Endpoint: endpoint, Timeout: "2s", Params: map[string]any{"topic": "mobile/value", "payload": "AP+A", "payload_encoding": "base64", "qos": 1, "retain": true}},
		config.Request{ID: "read", Protocol: "mqtt", Action: "read-one", Endpoint: endpoint, Timeout: "2s", Params: map[string]any{"topic": "mobile/value", "qos": 1}},
	)
	token := previewToken(t, s, "publish")
	if cmd(t, s, map[string]any{"op": "run", "token": token})["ok"] != false {
		t.Fatal("unconfirmed MQTT publish permitted")
	}
	mustOK(t, s, map[string]any{"op": "run", "token": token, "confirmed": true})
	awaitDone(t, s)
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "read")})
	events := awaitDone(t, s)
	found := false
	for _, ev := range events {
		if ev["kind"] == "message" {
			data := ev["data"].(map[string]any)
			if data["topic"] == "mobile/value" && data["payload_base64"] == "AP+A" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("binary MQTT shape did not survive bridge: %v", events)
	}
}
func TestKafkaNativeCommandsRealLoopback(t *testing.T) {
	cluster, e := kfake.NewCluster(kfake.NumBrokers(1))
	if e != nil {
		t.Fatal(e)
	}
	defer cluster.Close()
	endpoint := cluster.ListenAddrs()[0]
	s := testSession(t,
		config.Request{ID: "create", Protocol: "kafka", Action: "create-topic", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topic": "mobile-test", "partitions": 1, "replication_factor": 1}},
		config.Request{ID: "produce", Protocol: "kafka", Action: "produce", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topic": "mobile-test", "key": "sensor", "value": "mobile-value"}},
		config.Request{ID: "consume", Protocol: "kafka", Action: "consume", Endpoint: endpoint, Timeout: "3s", Params: map[string]any{"topic": "mobile-test", "offset": "earliest", "limit": 1}},
	)
	for _, id := range []string{"create", "produce"} {
		token := previewToken(t, s, id)
		mustOK(t, s, map[string]any{"op": "run", "token": token, "confirmed": true})
		events := awaitDone(t, s)
		if events[len(events)-1]["data"].(map[string]any)["status"] != "completed" {
			t.Fatalf("%s failed: %v", id, events)
		}
	}
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "consume")})
	events := awaitDone(t, s)
	found := false
	for _, ev := range events {
		if ev["kind"] == "record" {
			data := ev["data"].(map[string]any)
			if data["key"] == "sensor" && data["value"] == "mobile-value" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("Kafka record did not survive bridge: %v", events)
	}
}
func TestLargeResultsRemainRetrievableAndEvictionExplicit(t *testing.T) {
	s := testSession(t)
	payload := strings.Repeat("x", 100<<10)
	s.emit("read", "response", map[string]any{"body": payload, "raw_body_base64": "AP+A"})
	data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	preview := data["events"].([]any)[0].(map[string]any)["data"].(map[string]any)
	id := preview["result_id"].(string)
	full := mustOK(t, s, map[string]any{"op": "result.get", "result_id": id}).(map[string]any)["data"].(map[string]any)
	if full["body"] != payload || full["raw_body_base64"] != "AP+A" {
		t.Fatal("large result lost raw data")
	}
	for i := 0; i < maxCachedResults; i++ {
		s.emit("read", "response", map[string]any{"body": payload})
	}
	if cmd(t, s, map[string]any{"op": "result.get", "result_id": id})["ok"] != false {
		t.Fatal("old cache entry not evicted")
	}
	data = mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	if data["evicted_result_count"].(float64) < 1 || len(data["evicted_result_ids"].([]any)) < 1 {
		t.Fatal("eviction was silent")
	}
	s.mu.Lock()
	if len(s.results) > maxCachedResults || s.resultBytes > maxResultCacheBytes {
		t.Fatal("result cache unbounded")
	}
	s.mu.Unlock()
}
