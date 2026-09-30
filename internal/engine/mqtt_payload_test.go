package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"reflect"
	"strings"
	"testing"
)

func TestMQTTMessagePackCanonicalVectors(t *testing.T) {
	cases := []struct {
		wire string
		want any
	}{
		{"c0", nil}, {"c2", false}, {"c3", true}, {"2a", json.Number("42")}, {"ff", json.Number("-1")},
		{"ccff", json.Number("255")}, {"cdffff", json.Number("65535")}, {"ceffffffff", json.Number("4294967295")}, {"cfffffffffffffffff", json.Number("18446744073709551615")},
		{"d080", json.Number("-128")}, {"d18000", json.Number("-32768")}, {"d280000000", json.Number("-2147483648")}, {"d38000000000000000", json.Number("-9223372036854775808")},
		{"ca3fc00000", json.Number("1.5")}, {"cb3ff8000000000000", json.Number("1.5")},
		{"a26f6b", "ok"}, {"d9026f6b", "ok"}, {"da00026f6b", "ok"}, {"db000000026f6b", "ok"},
		{"9201c3", []any{json.Number("1"), true}}, {"dc000201c3", []any{json.Number("1"), true}}, {"dd0000000201c3", []any{json.Number("1"), true}},
		{"81a1782a", map[string]any{"x": json.Number("42")}}, {"de0001a1782a", map[string]any{"x": json.Number("42")}}, {"df00000001a1782a", map[string]any{"x": json.Number("42")}},
	}
	for _, tc := range cases {
		wire, err := hex.DecodeString(tc.wire)
		if err != nil {
			t.Fatal(err)
		}
		got, err := mqttDecodeMessagePack(wire)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %#v != %#v; %v", tc.wire, got, tc.want, err)
		}
	}
}
func TestMQTTMessagePackBinaryExtensionsAndNonfiniteRemainExplicit(t *testing.T) {
	for _, wire := range []string{"c40200ff", "c5000200ff", "c60000000200ff", "d4ff01", "d5ff0001", "d6ff00000001", "d7ff0000000000000001", "d8ff00000000000000000000000000000001", "c701ff01", "c80001ff01", "c900000001ff01", "ca7f800000", "cb7ff8000000000000", "a1ff"} {
		raw, err := hex.DecodeString(wire)
		if err != nil {
			t.Fatal(err)
		}
		v, err := mqttDecodeMessagePack(raw)
		if err != nil {
			t.Fatalf("%s: %v", wire, err)
		}
		typed, ok := v.(map[string]any)
		if !ok || typed["messagepack_type"] == nil {
			t.Fatalf("binary/extension/nonfinite silently lost type: %s %#v", wire, v)
		}
		if _, err := json.Marshal(v); err != nil {
			t.Fatal("result not JSON-safe", err)
		}
	}
}
func TestMQTTMessagePackMalformedBoundsAndDuplicateKeys(t *testing.T) {
	cases := [][]byte{{}, {0xc1}, {0xc3, 0xc2}, {0xa2, 'x'}, {0xc6, 0xff, 0xff, 0xff, 0xff}, {0xdd, 0xff, 0xff, 0xff, 0xff}, {0xdf, 0xff, 0xff, 0xff, 0xff}, {0xd8, 0x00}, append([]byte(strings.Repeat("\x91", 33)), 0xc0)}
	for _, wire := range []string{"82a17801a17802", "822a01a2343202", "82ca4228000001cb404500000000000002"} {
		b, _ := hex.DecodeString(wire)
		cases = append(cases, b)
	}
	cases = append(cases, []byte(strings.Repeat("x", maxBody+1)))
	for _, wire := range cases {
		if _, err := mqttDecodeMessagePack(wire); err == nil {
			t.Fatalf("accepted malformed payload %x", wire[:min(len(wire), 30)])
		}
	}
}
func TestMQTTMessagePackMetadataPreservesRawAndDetectionOrder(t *testing.T) {
	wire, _ := hex.DecodeString("81ab74656d70657261747572652a")
	m := mqttMessage("sensor", wire, 1, false)
	if m["payload_format"] != "messagepack" || m["payload_messagepack"].(map[string]any)["temperature"] != json.Number("42") {
		t.Fatalf("messagepack event missing %#v", m)
	}
	if m["payload_hex"] != hex.EncodeToString(wire) || m["received_at"] == nil {
		t.Fatal("lost raw payload or receive time")
	}
	for _, tc := range []struct{ payload, format string }{{"42", "json"}, {"hello", "text"}, {"\xc1", "binary"}, {"\xc3", "messagepack"}} {
		got := mqttMessage("test", []byte(tc.payload), 0, true)
		if got["payload_format"] != tc.format {
			t.Fatalf("detection precedence %#v", got)
		}
	}
}
func FuzzMQTTMessagePackBounded(f *testing.F) {
	for _, seed := range [][]byte{{0xc3}, {0x81, 0xa1, 'x', 0x01}, {0xc6, 0xff, 0xff, 0xff, 0xff}, {0x91, 0xc0}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			t.Skip()
		}
		v, err := mqttDecodeMessagePack(data)
		if err == nil {
			if _, err = json.Marshal(v); err != nil {
				t.Fatal("non-JSON-safe result", err)
			}
		}
	})
}

func TestMQTTMessagePackCompositeMapKeysFailBoundedly(t *testing.T) {
	// Composite map keys can amplify escaping exponentially when coerced to JSON
	// object names. Keep them binary instead of permitting that amplification.
	for _, raw := range [][]byte{{0x81, 0x91, 0x01, 0x02}, {0x81, 0x81, 0xa1, 'x', 0x01, 0x02}} {
		if _, err := mqttDecodeMessagePack(raw); err == nil {
			t.Fatal("composite key accepted")
		}
	}
}

func TestMQTTMessagePackLoopbackReadOne(t *testing.T) {
	server, endpoint := newMQTTFixture(t)
	wire, _ := hex.DecodeString("81ab74656d70657261747572652a")
	if err := server.Publish("payload/packed", wire, true, 1); err != nil {
		t.Fatal(err)
	}
	r := config.Request{Protocol: "mqtt", Action: "read-one", Endpoint: endpoint, Timeout: "2s", Params: map[string]any{"topic": "payload/packed"}}
	seen := false
	err := Run(context.Background(), r, false, func(event Event) {
		if event.Kind == "message" {
			m := event.Data.(map[string]any)
			seen = m["payload_format"] == "messagepack" && m["received_at"] != nil && m["retained"] == true
		}
	})
	if err != nil || !seen {
		t.Fatalf("loopback packed read: %v seen=%t", err, seen)
	}
}
