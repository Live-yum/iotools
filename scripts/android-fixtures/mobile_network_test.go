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
	meta, stop, err := startMobileNetworkFixturesOnPorts(0, 0, 0)
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

func TestModbusFixtureFC23DeviceIdentificationAndFullBitRead(t *testing.T) {
	m, err := startModbusFixture("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	request := config.Request{Protocol: "modbus", Endpoint: "tcp://" + m.endpoint(), Action: "read-write-registers", Timeout: "2s", Params: map[string]any{"unit": 1, "address": 0, "count": 2, "values": []any{88, 89}, "read_address": 0, "read_count": 2}}
	seen := false
	if err := engine.Run(context.Background(), request, true, func(event engine.Event) {
		if event.Kind == "registers" {
			rows := event.Data.([]map[string]any)
			seen = len(rows) == 2 && rows[0]["u16"] == uint16(88) && rows[1]["u16"] == uint16(89)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seen || m.reads.Load() != 1 || m.writes.Load() != 1 {
		t.Fatal("FC23 must write before read in one counted transaction")
	}
	request.Action = "read-device-id"
	request.Params = map[string]any{"unit": 1, "read_code": 1, "object_id": 0}
	seen = false
	if err := engine.Run(context.Background(), request, false, func(event engine.Event) {
		if event.Kind == "device-identification" {
			objects := event.Data.(map[string]any)["objects"].(map[int]string)
			seen = objects[0] == "iotools fixture" && objects[1] == "local-loopback" && objects[2] == "1.0"
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seen || m.reads.Load() != 2 || m.writes.Load() != 1 {
		t.Fatal("device identification mismatch")
	}
	request.Action = "read-coils"
	request.Params = map[string]any{"unit": 1, "address": 0, "count": 2000}
	seen = false
	if err := engine.Run(context.Background(), request, false, func(event engine.Event) {
		if event.Kind == "bits" {
			bits := event.Data.(map[string]any)["values"].([]bool)
			seen = len(bits) == 2000 && bits[0] && !bits[1999]
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seen || m.reads.Load() != 3 {
		t.Fatal("full bit window missing")
	}
	request.Action = "read-holding"
	request.Params = map[string]any{"unit": 2, "address": 0, "count": 1}
	if err := engine.Run(context.Background(), request, false, nil); err == nil {
		t.Fatal("unknown unit accepted")
	}
	if m.reads.Load() != 3 || m.writes.Load() != 1 {
		t.Fatal("unit mismatch changed counters")
	}
}

func TestDisposableNetworkFixtureInstancesKeepDistinctLivePorts(t *testing.T) {
	first, stopFirst, err := startMobileNetworkFixturesOnPorts(0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stopFirst()
	second, stopSecond, err := startMobileNetworkFixturesOnPorts(0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stopSecond()
	for _, key := range []string{"mqtt_endpoint", "modbus_endpoint", "network_metrics_url"} {
		if first[key] == second[key] {
			t.Fatalf("independent live %s collided: %v", key, first[key])
		}
	}
	for _, meta := range []map[string]any{first, second} {
		seen := false
		request := config.Request{Protocol: "mqtt", Endpoint: meta["mqtt_endpoint"].(string), Action: "read-one", Timeout: "3s", Params: map[string]any{"topic": "/sensors//temp/", "qos": 1}}
		if err := engine.Run(context.Background(), request, false, func(event engine.Event) {
			if event.Kind == "message" {
				data := event.Data.(map[string]any)
				seen = data["topic"] == "/sensors//temp/" && data["retained"] == true
			}
		}); err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatal("independent retained topic did not reach real wire client")
		}
	}
}
