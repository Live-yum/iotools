package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdvancedRegisterCodecs(t *testing.T) {
	for _, tc := range []struct {
		order string
		data  []byte
	}{{"ABCD", []byte{0x3f, 0xf0, 0, 0, 0, 0, 0, 0}}, {"BADC", []byte{0xf0, 0x3f, 0, 0, 0, 0, 0, 0}}, {"CDAB", []byte{0, 0, 0, 0, 0, 0, 0x3f, 0xf0}}, {"DCBA", []byte{0, 0, 0, 0, 0, 0, 0xf0, 0x3f}}} {
		rows := decodeRegisters(tc.data, 0, tc.order)
		if rows[0]["f64"] != "1" {
			t.Fatalf("%s: %v", tc.order, rows[0])
		}
		if _, ok := rows[1]["f64"]; ok {
			t.Fatal("short f64 decoded")
		}
	}
	rows := decodeRegisters([]byte{0x12, 0x34, 0x56, 0x78}, 0, "ABCD")
	if rows[0]["bcd"] != uint64(1234) || rows[0]["bcd32"] != uint64(12345678) || rows[0]["u32_m10k"] != "4660/22136" {
		t.Fatal(rows)
	}
	rows = decodeRegisters([]byte{0xff, 0xff, 0x80, 0}, 0, "ABCD")
	if rows[0]["i32_m10k"] != "-1/-32768" {
		t.Fatal(rows)
	}
	if _, ok := rows[0]["bcd"]; ok {
		t.Fatal("invalid BCD accepted")
	}
	if halfFloat(0x3c00) != 1 || halfFloat(1) != math.Ldexp(1, -24) || !math.IsInf(halfFloat(0x7c00), 1) || !math.IsNaN(halfFloat(0x7e00)) {
		t.Fatal("half float vectors")
	}
}
func TestRegisterRulesAndAnnotations(t *testing.T) {
	collection, err := config.Parse([]byte(`version: 1
requests:
- id: test
  protocol: modbus
  action: read-holding
  endpoint: tcp://127.0.0.1:1
  params:
    pins: [10]
    labels: {10: 电压}
    rules:
    - address: 10
      repr: u16
      ops: ["/10", "+2"]
      decimals: 1
      suffix: ' V'
`))
	if err != nil {
		t.Fatal(err)
	}
	a, err := parseRegisterAnnotations(collection.Requests[0])
	if err != nil {
		t.Fatal(err)
	}
	rows := decodeRegisters([]byte{0x08, 0xfc}, 10, "ABCD")
	if err = annotateRegisters(rows, a, "ABCD"); err != nil {
		t.Fatal(err)
	}
	if rows[0]["custom"] != "232.0 V" || rows[0]["pinned"] != true || rows[0]["label"] != "电压" {
		t.Fatal(rows)
	}
	address := 10
	decimals := 2
	r := RegisterRule{Address: &address, Repr: "f64", Next: []int{14, 18, 22}, Decimals: &decimals}
	s, n, e := r.evaluate(map[int]uint16{10: 0x3ff0, 14: 0, 18: 0, 22: 0}, "ABCD")
	if e != nil || s != "1.00" || n == nil || *n != 1 {
		t.Fatal(s, n, e)
	}
	r = RegisterRule{Address: &address, Repr: "u16", Enum: map[string]string{"0": "停止"}, Bits: map[int]string{0: "运行", 1: "就绪"}}
	s, _, _ = r.evaluate(map[int]uint16{10: 3}, "ABCD")
	if s != "运行|就绪" {
		t.Fatal(s)
	}
	s, _, _ = r.evaluate(map[int]uint16{10: 0}, "ABCD")
	if s != "停止" {
		t.Fatal(s)
	}
	for _, params := range []map[string]any{{"pins": []any{-1}}, {"rules": []any{map[string]any{"repr": "u16"}}}, {"rules": []any{map[string]any{"address": 65535, "repr": "f64"}}}, {"rules": []any{map[string]any{"address": 0, "repr": "u16", "ops": []string{"/0"}}}}, {"rules": []any{map[string]any{"address": 0, "repr": "u16", "typo": true}}}} {
		if _, e := parseRegisterAnnotations(config.Request{Params: params}); e == nil {
			t.Fatalf("invalid annotations accepted: %v", params)
		}
	}
}
func TestModbusAPIFailClosed(t *testing.T) {
	base := config.Request{Protocol: "modbus", Endpoint: "tcp://127.0.0.1:1", Action: "read-holding", Params: map[string]any{"unit": 1}}
	readonly, err := newModbusAPIHandler(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := &ModbusWriteScope{Unit: 1, Address: 10, Count: 2, Type: "holding"}
	writable, err := newModbusAPIHandler(base, scope)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		h                        http.Handler
		path, body, host, origin string
		status                   int
	}{{readonly, "/write", `{"type":"holding","address":10,"values":[1]}`, "127.0.0.1", "", 403}, {writable, "/write", `{"type":"holding","address":9,"values":[1]}`, "127.0.0.1", "", 403}, {writable, "/write", `{"type":"holding","address":11,"values":[1,2]}`, "127.0.0.1", "", 403}, {writable, "/read", `{"type":"holding","address":0,"count":1,"unit_id":2}`, "127.0.0.1", "", 403}, {readonly, "/read", `{"type":"holding","address":0.5,"count":1}`, "127.0.0.1", "", 400}, {readonly, "/read", `{"type":"holding","count":1}`, "127.0.0.1", "", 400}, {readonly, "/read", `{"type":"holding","address":65535,"count":2}`, "127.0.0.1", "", 400}, {readonly, "/read", `{"type":"holding","address":0,"count":1} {}`, "127.0.0.1", "", 400}, {readonly, "/read", `{}`, "evil.test", "", 403}, {readonly, "/read", `{}`, "127.0.0.1", "https://evil.test", 403}}
	for _, c := range cases {
		req := httptest.NewRequest("POST", c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rr := httptest.NewRecorder()
		c.h.ServeHTTP(rr, req)
		if rr.Code != c.status {
			t.Fatalf("%s got %d: %s", c.body, rr.Code, rr.Body.String())
		}
	}
	for _, addr := range []string{"0.0.0.0:0", "localhost:0", "[::]:0", "192.0.2.1:502"} {
		if e := ServeModbusAPI(context.Background(), addr, base, nil); e == nil {
			t.Fatalf("public bind accepted: %s", addr)
		}
	}
}
func TestModbusAPIRealLocalReadAndScopedWrite(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Second))
				header := make([]byte, 7)
				if _, e = io.ReadFull(c, header); e != nil {
					return
				}
				pdu := make([]byte, int(binary.BigEndian.Uint16(header[4:6]))-1)
				if _, e = io.ReadFull(c, pdu); e != nil {
					return
				}
				response := []byte{3, 2, 0, 42}
				if pdu[0] == 16 {
					response = append([]byte{16}, pdu[1:5]...)
				}
				binary.BigEndian.PutUint16(header[4:6], uint16(len(response)+1))
				_, _ = c.Write(append(header, response...))
			}()
		}
	}()
	base := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://" + l.Addr().String(), Params: map[string]any{"unit": 1}}
	h, e := newModbusAPIHandler(base, &ModbusWriteScope{Unit: 1, Address: 10, Count: 1, Type: "holding"})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{{"/read", `{"type":"holding","address":10,"count":1}`, 200}, {"/write", `{"type":"holding","address":10,"values":[99]}`, 204}} {
		req := httptest.NewRequest("POST", "http://127.0.0.1"+tc.path, bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Fatal(rr.Code, rr.Body.String())
		}
		if tc.status == 200 {
			var body struct{ Values []int }
			if e = json.Unmarshal(rr.Body.Bytes(), &body); e != nil || len(body.Values) != 1 || body.Values[0] != 42 {
				t.Fatal(rr.Body.String(), e)
			}
		}
	}
	l.Close()
	<-done
}
func TestModbusTCPCancellation(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(io.Discard, c)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	e = Run(ctx, config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://" + l.Addr().String(), Timeout: "5s", Params: map[string]any{"unit": 1}}, false, nil)
	if e == nil || time.Since(start) > time.Second {
		t.Fatalf("cancel not bounded: %v", e)
	}
	<-done
}

func TestRegisterSnapshotRoundtripAndDiff(t *testing.T) {
	rows := decodeRegisters([]byte{0, 42, 0, 43}, 10, "ABCD")
	before, e := NewRegisterSnapshot("tcp://127.0.0.1:502", 1, "read-holding", rows)
	if e != nil {
		t.Fatal(e)
	}
	path := t.TempDir() + "/snapshot.json"
	if e = SaveRegisterSnapshot(path, before); e != nil {
		t.Fatal(e)
	}
	if e = SaveRegisterSnapshot(path, before); e == nil {
		t.Fatal("snapshot overwritten")
	}
	loaded, e := LoadRegisterSnapshot(path)
	if e != nil {
		t.Fatal(e)
	}
	after, e := NewRegisterSnapshot(before.Endpoint, 1, "read-holding", decodeRegisters([]byte{0, 44, 0, 45}, 11, "ABCD"))
	if e != nil {
		t.Fatal(e)
	}
	changes, e := DiffRegisterSnapshots(loaded, after)
	if e != nil || len(changes) != 3 || changes[0].After != nil || changes[2].Before != nil || *changes[1].After != 44 {
		t.Fatal(changes, e)
	}
	after.Unit = 2
	if _, e = DiffRegisterSnapshots(before, after); e == nil {
		t.Fatal("cross-unit comparison accepted")
	}
}

func TestModbusAPIRejectsBrowserSimplePost(t *testing.T) {
	base := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://127.0.0.1:1", Params: map[string]any{"unit": 1}}
	h, e := newModbusAPIHandler(base, &ModbusWriteScope{Unit: 1, Address: 0, Count: 1, Type: "holding"})
	if e != nil {
		t.Fatal(e)
	}
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1/write", strings.NewReader(`{"type":"holding","address":0,"values":[1]}`))
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("accepted simple POST %q: %d", contentType, w.Code)
		}
	}
}
