package main

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"testing"
)

func TestModbusFixtureRealWireReadWriteAndCounter(t *testing.T) {
	m, err := startModbusFixture("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	request := config.Request{Protocol: "modbus", Endpoint: "tcp://" + m.endpoint(), Action: "read-holding", Timeout: "2s", Params: map[string]any{"unit": 1, "address": 0, "count": 2}}
	var event engine.Event
	if err = engine.Run(context.Background(), request, false, func(e engine.Event) {
		if e.Kind == "registers" {
			event = e
		}
	}); err != nil {
		t.Fatal(err)
	}
	if event.Kind == "" || m.reads.Load() != 1 || m.writes.Load() != 0 {
		t.Fatal("missing real initial read")
	}
	request.Action = "write-register"
	request.Params = map[string]any{"unit": 1, "address": 0, "value": 99}
	if err = engine.Run(context.Background(), request, true, nil); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	value := m.words[0]
	m.mu.Unlock()
	if value != 99 || m.writes.Load() != 1 {
		t.Fatal("write did not reach fixture")
	}
	request.Action = "write-registers"
	request.Params = map[string]any{"unit": 1, "address": 2, "values": []any{100, 101}}
	if err = engine.Run(context.Background(), request, true, nil); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	a, b := m.words[2], m.words[3]
	m.mu.Unlock()
	if a != 100 || b != 101 || m.writes.Load() != 2 {
		t.Fatal("multiwrite mismatch")
	}
}

func TestDisposableMQTTFixtureRetainsExactTopicLevels(t *testing.T) {
	meta, stop, err := startMobileNetworkFixtures()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	request := config.Request{Protocol: "mqtt", Endpoint: meta["mqtt_endpoint"].(string), Action: "read-one", Timeout: "3s", Params: map[string]any{"topic": "/sensors//temp/", "qos": 1}}
	seen := false
	if err := engine.Run(context.Background(), request, false, func(event engine.Event) {
		if event.Kind == "message" {
			data := event.Data.(map[string]any)
			if data["topic"] == "/sensors//temp/" && data["retained"] == true {
				seen = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("retained wire message did not preserve empty topic levels")
	}
}
