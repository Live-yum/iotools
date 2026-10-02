package engine

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func TestModbusDeviceIdentificationMockAndTCP(t *testing.T) {
	for _, tcp := range []bool{false, true} {
		endpoint := "mock://local"
		var done chan struct{}
		var listener net.Listener
		if tcp {
			var e error
			listener, e = net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			endpoint = "tcp://" + listener.Addr().String()
			done = make(chan struct{})
			go func() {
				defer close(done)
				c, e := listener.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(time.Second))
				head := make([]byte, 7)
				if _, e = io.ReadFull(c, head); e != nil {
					return
				}
				body := make([]byte, int(binary.BigEndian.Uint16(head[4:6]))-1)
				if _, e = io.ReadFull(c, body); e != nil {
					return
				}
				h := mockModbusHandler{}
				reply, e := h.Send(append(head, body...))
				if e == nil {
					c.Write(reply)
				}
			}()
		}
		r := config.Request{Protocol: "modbus", Action: "read-device-id", Endpoint: endpoint, Params: map[string]any{"unit": 1, "read_code": 1, "object_id": 0}}
		seen := false
		e := Run(context.Background(), r, false, func(e Event) {
			if e.Kind == "device-identification" {
				objects := e.Data.(map[string]any)["objects"].(map[int]string)
				seen = objects[0] == "iotools" && objects[1] == "Explicit local simulator"
			}
		})
		if tcp {
			listener.Close()
			<-done
		}
		if e != nil || !seen {
			t.Fatal(e, seen)
		}
	}
}
func TestModbusBoundedSweepSearchAndUnitProbe(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "sweep-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 241, "address": 300, "end_address": 430, "count": 50}}
	rows := 0
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "registers" {
			rows += len(e.Data.([]map[string]any))
		}
	}); e != nil || rows != 131 {
		t.Fatal(e, rows)
	}
	r.Action = "search-holding"
	r.Params["match_value"] = 345
	matches := 0
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "register-match" {
			matches++
			if e.Data.(map[string]any)["address"] != 345 {
				t.Error("wrong match")
			}
		}
	}); e != nil || matches != 1 {
		t.Fatal(e, matches)
	}
	r.Action = "scan-units"
	r.Params["units"] = []any{240, 241}
	probes := 0
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "unit-probe" {
			probes++
			if e.Data.(map[string]any)["responsive"] != true {
				t.Error("mock not responsive")
			}
		}
	}); e != nil || probes != 2 {
		t.Fatal(e, probes)
	}
	r.Params["units"] = []any{0}
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("broadcast scan accepted")
	}
	r.Params["units"] = []any{1, 1}
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("duplicate scan accepted")
	}
	r.Action = "sweep-holding"
	r.Params["end_address"] = 50000
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("unbounded sweep accepted")
	}
}
func TestModbusRawClassificationAndEcho(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "read-raw", Endpoint: "mock://local", Params: map[string]any{"unit": 239, "address": 10, "count": 1, "pdu_hex": "03000a0001"}}
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "raw-pdu" {
			seen = e.Data.(map[string]any)["response_hex"] == "0302000a"
		}
	}); e != nil || !seen {
		t.Fatal(e, seen)
	}
	for _, p := range []string{"06000a0001", "0800000000", "2b0e0500", "03000affff", "03000a000100"} {
		r.Params["pdu_hex"] = p
		if e := Run(context.Background(), r, false, nil); e == nil {
			t.Fatalf("unsafe raw read %s accepted", p)
		}
	}
	r.Action = "write-raw"
	r.Params["pdu_hex"] = "06000a002a"
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("raw write bypassed gate")
	}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Params["address"] = 11
	if e := Run(context.Background(), r, true, nil); e == nil {
		t.Fatal("raw write scope mismatch accepted")
	}
}
func TestModbusTypedWriteVectors(t *testing.T) {
	for _, kind := range []string{"u16", "i16", "f16", "u32", "i32", "f32", "u64", "i64", "f64"} {
		value := "42"
		if strings.HasPrefix(kind, "i") {
			value = "-42"
		}
		for _, order := range []string{"ABCD", "BADC", "CDAB", "DCBA"} {
			v, e := encodeTypedRegisters(kind, value, order)
			if e != nil {
				t.Fatal(e)
			}
			data := make([]byte, len(v)*2)
			for i, n := range v {
				binary.BigEndian.PutUint16(data[i*2:], uint16(n.(int)))
			}
			rows := decodeRegisters(data, 0, order)
			if rows[0][kind] == nil {
				t.Fatal(kind, rows)
			}
			address := 0
			rule := RegisterRule{Address: &address, Repr: kind}
			values := map[int]uint16{}
			for i, n := range v {
				values[i] = uint16(n.(int))
			}
			text, _, e := rule.evaluate(values, order)
			if e != nil || text != value {
				t.Fatal(kind, order, text, e)
			}
		}
	}
	for _, value := range []float64{0, math.Copysign(0, -1), 1, -2, math.Ldexp(1, -24), 65504} {
		b, e := encodeHalf(value)
		if e != nil || halfFloat(b) != value || math.Signbit(halfFloat(b)) != math.Signbit(value) {
			t.Fatal(value, b, e)
		}
	}
	for _, tc := range []struct{ kind, value string }{{"u16", "65536"}, {"i16", "32768"}, {"f16", "65536"}, {"f32", "1e99"}, {"f64", "NaN"}, {"u64", "18446744073709551616"}} {
		if _, e := encodeTypedRegisters(tc.kind, tc.value, "ABCD"); e == nil {
			t.Fatal("overflow accepted", tc)
		}
	}
	r := config.Request{Protocol: "modbus", Action: "write-typed", Endpoint: "mock://local", Params: map[string]any{"unit": 238, "address": 20, "value_type": "f64", "value": "1.25", "word_order": "CDAB"}}
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("typed write gate")
	}
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "read-holding"
	r.Params["count"] = 4
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "registers" {
			seen = e.Data.([]map[string]any)[0]["f64"] == "1.25"
		}
	}); e != nil || !seen {
		t.Fatal(e, seen)
	}
}
