package engine

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func TestModbusToolsAllSpacesCyclesAndStopFirst(t *testing.T) {
	for _, space := range []string{"holding", "input", "coils", "discrete"} {
		r := config.Request{Protocol: "modbus", Action: "sweep-" + space, Endpoint: "mock://local", Params: map[string]any{"unit": 230, "address": 400, "end_address": 404, "count": 2, "sweep_cycles": 2, "interval_ms": 10}}
		rows, progress := 0, 0
		if err := Run(context.Background(), r, false, func(e Event) {
			if e.Kind == "registers" {
				rows += len(e.Data.([]map[string]any))
			}
			if e.Kind == "bits" {
				rows += len(e.Data.(map[string]any)["values"].([]bool))
			}
			if e.Kind == "sweep-progress" {
				progress++
			}
		}); err != nil || rows != 10 || progress != 6 {
			t.Fatalf("%s rows%d progress%d err%v", space, rows, progress, err)
		}
		r.Action = "scan-units"
		r.Params["scan_type"] = "read-" + space
		r.Params["units"] = []any{230, 231, 232}
		r.Params["stop_first"] = true
		hits := 0
		if err := Run(context.Background(), r, false, func(e Event) {
			if e.Kind == "unit-probe" {
				hits++
				m := e.Data.(map[string]any)
				if m["type"] != "read-"+space || len(m["values"].([]any)) != 2 {
					t.Error("wrong probe scope")
				}
			}
		}); err != nil || hits != 1 {
			t.Fatalf("stopfirst %d %v", hits, err)
		}
	}
	r := config.Request{Protocol: "modbus", Action: "sweep-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 1, "address": 0, "end_address": 15999, "count": 1, "sweep_cycles": 1000}}
	if err := Run(context.Background(), r, false, nil); err == nil {
		t.Fatal("transaction bound absent")
	}
	r.Params["sweep_cycles"] = 0
	if err := Run(context.Background(), r, false, nil); err == nil {
		t.Fatal("cycles invalid")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.Params["end_address"] = 0
	r.Params["sweep_cycles"] = 1000
	r.Params["interval_ms"] = 86400000
	progress := 0
	start := time.Now()
	err := Run(ctx, r, false, func(e Event) {
		if e.Kind == "sweep-progress" {
			progress++
			cancel()
		}
	})
	if err == nil || progress != 1 || time.Since(start) > time.Second {
		t.Fatal("cycle wait not cancellable")
	}
}
func TestModbusToolsUnitExceptionsWireAndNoExtraProbe(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var wg sync.WaitGroup
	var lock sync.Mutex
	seen := [][]byte{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Second))
				head := make([]byte, 7)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				pdu := make([]byte, int(binary.BigEndian.Uint16(head[4:6]))-1)
				if _, err := io.ReadFull(c, pdu); err != nil {
					return
				}
				lock.Lock()
				seen = append(seen, append([]byte{head[6]}, pdu...))
				lock.Unlock()
				var reply []byte
				if head[6] == 1 {
					reply = append(append([]byte{}, head...), byte(pdu[0]|0x80), 2)
					binary.BigEndian.PutUint16(reply[4:6], 3)
				} else {
					h := mockModbusHandler{}
					reply, _ = h.Send(append(head, pdu...))
				}
				_, _ = c.Write(reply)
			}()
		}
	}()
	defer func() { l.Close(); <-done; wg.Wait() }()
	r := config.Request{Protocol: "modbus", Action: "scan-units", Endpoint: "tcp://" + l.Addr().String(), Params: map[string]any{"address": 10, "count": 3, "units": []any{1, 2, 3}, "scan_type": "read-discrete", "stop_first": true}}
	results := []map[string]any{}
	if err = Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "unit-probe" {
			results = append(results, e.Data.(map[string]any))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0]["exception"] != true || results[1]["responsive"] != true {
		t.Fatalf("wrong results %+v", results)
	}
	lock.Lock()
	defer lock.Unlock()
	if len(seen) != 2 {
		t.Fatal("extra unit probed")
	}
	for _, p := range seen {
		if len(p) != 6 || p[1] != 2 || binary.BigEndian.Uint16(p[2:4]) != 10 || binary.BigEndian.Uint16(p[4:6]) != 3 {
			t.Fatalf("wrong actual wire %x", p)
		}
	}
}
func TestModbusToolsRawAndRulesHelpersNoIO(t *testing.T) {
	r := config.Request{Protocol: "modbus", Action: "read-raw", Endpoint: "mock://local", Params: map[string]any{"unit": 1, "pdu_hex": "0300000001"}}
	if b, err := ValidateModbusRaw(r); err != nil || len(b) != 5 {
		t.Fatal(err)
	}
	for _, pdu := range []string{"9900000001", "0600000001", "030000ffff", "nothex"} {
		r.Params["pdu_hex"] = pdu
		if _, err := ValidateModbusRaw(r); err == nil {
			t.Fatal("read raw bypass " + pdu)
		}
	}
	r.Params = map[string]any{"rules": []any{map[string]any{"address": 0, "repr": "u16", "ops": []any{"/0"}}}}
	if _, err := ModbusRules(r); err == nil || !strings.Contains(err.Error(), "operand") {
		t.Fatal("invalid rule accepted")
	}
}
func TestModbusToolsRecoveryFailureScopeAndCancel(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var lock sync.Mutex
	seen := [][2]int{}
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Second))
				head := make([]byte, 7)
				if _, e := io.ReadFull(c, head); e != nil {
					return
				}
				body := make([]byte, int(binary.BigEndian.Uint16(head[4:6]))-1)
				if _, e := io.ReadFull(c, body); e != nil {
					return
				}
				if len(body) != 5 {
					return
				}
				address, count := int(binary.BigEndian.Uint16(body[1:3])), int(binary.BigEndian.Uint16(body[3:5]))
				lock.Lock()
				seen = append(seen, [2]int{address, count})
				lock.Unlock()
				var reply []byte
				if address == 100 {
					binary.BigEndian.PutUint16(head[4:6], 3)
					reply = append(head, body[0]|0x80, 2)
				} else {
					reply, _ = (&mockModbusHandler{}).Send(append(head, body...))
				}
				_, _ = c.Write(reply)
			}()
		}
	}()
	defer func() { l.Close(); <-done; wg.Wait() }()
	r := config.Request{Protocol: "modbus", Action: "sweep-holding", Endpoint: "tcp://" + l.Addr().String(), Timeout: "2s", Params: map[string]any{"unit": 1, "address": 100, "end_address": 103, "count": 2}}
	if err = Run(context.Background(), r, false, nil); err == nil {
		t.Fatal("default recovered without optin")
	}
	r.Params["sweep_recover"] = true
	errors, rows := 0, 0
	if err = Run(context.Background(), r, false, func(e Event) {
		if e.Kind == "sweep-error" {
			errors++
			m := e.Data.(map[string]any)
			if m["batch_address"] != 100 || m["batch_count"] != 2 || m["skipped_position"] != 100 {
				t.Error("failed batch scope misrepresented")
			}
		}
		if e.Kind == "registers" {
			rows += len(e.Data.([]map[string]any))
		}
	}); err != nil || errors != 1 || rows != 3 {
		t.Fatalf("recovery %v %d %d", err, errors, rows)
	}
	lock.Lock()
	want := [][2]int{{100, 2}, {100, 2}, {101, 1}, {102, 2}}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("wire scopes %+v", seen)
	}
	before := len(seen)
	lock.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = Run(ctx, r, false, func(e Event) {
		if e.Kind == "sweep-error" {
			cancel()
		}
	}); err == nil {
		t.Fatal("cancel swallowed by recovery")
	}
	lock.Lock()
	defer lock.Unlock()
	if len(seen) != before+1 {
		t.Fatal("cancel retried or advanced")
	}
}
