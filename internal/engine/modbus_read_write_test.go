package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func fc23Request() config.Request {
	return config.Request{Protocol: "modbus", Action: "read-write-registers", Endpoint: "mock://local", Timeout: "1s", Params: map[string]any{"unit": 239, "address": 65534, "count": 2, "values": []any{65535, 1}, "read_address": 65534, "read_count": 2}}
}
func TestModbusFC23MockExactReadbackAndGate(t *testing.T) {
	r := fc23Request()
	if !r.Mutates() {
		t.Fatal("not write gated")
	}
	if err := Run(context.Background(), r, false, nil); err == nil {
		t.Fatal("ungated FC23")
	}
	seen, matched := false, false
	if err := Run(context.Background(), r, true, func(e Event) {
		if e.Kind == "readback" {
			matched = e.Data.(map[string]any)["matches_written"] == true
		}
		if e.Kind == "registers" {
			rows := e.Data.([]map[string]any)
			seen = len(rows) == 2 && rows[0]["u16"] == uint16(65535) && rows[1]["u16"] == uint16(1)
		}
	}); err != nil || !seen || !matched {
		t.Fatalf("readback %v %t %t", err, seen, matched)
	}
	for _, change := range []map[string]any{{"read_address": 65535}, {"read_count": 0}, {"read_count": 126}, {"read_address": -1}, {"address": 65535}, {"values": []any{65536}}, {"values": []any{1.5}}, {"count": 1}, {"unit": 0}} {
		bad := r
		bad.Params = map[string]any{}
		for k, v := range r.Params {
			bad.Params[k] = v
		}
		for k, v := range change {
			bad.Params[k] = v
		}
		if err := Run(context.Background(), bad, true, nil); err == nil {
			t.Fatalf("accepted %+v", change)
		}
	}
}
func TestModbusFC23LoopbackWireAndMalformedNoRetry(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "malformed"}[bad], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			requests := make(chan []byte, 2)
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := listener.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				header := make([]byte, 7)
				if _, err = io.ReadFull(c, header); err != nil {
					return
				}
				n := int(binary.BigEndian.Uint16(header[4:6])) - 1
				body := make([]byte, n)
				if _, err = io.ReadFull(c, body); err != nil {
					return
				}
				requests <- append(append([]byte{}, header...), body...)
				reply := []byte{23, 4, 0xff, 0xff, 0, 1}
				if bad {
					reply = []byte{23}
				}
				binary.BigEndian.PutUint16(header[4:6], uint16(len(reply)+1))
				_, _ = c.Write(append(header, reply...))
			}()
			r := fc23Request()
			r.Endpoint = "tcp://" + listener.Addr().String()
			err = Run(context.Background(), r, true, nil)
			if bad && err == nil || !bad && err != nil {
				t.Fatalf("bad=%t err=%v", bad, err)
			}
			<-done
			select {
			case wire := <-requests:
				expected := []byte{23, 0xff, 0xfe, 0, 2, 0xff, 0xfe, 0, 2, 4, 0xff, 0xff, 0, 1}
				if wire[6] != 239 || !bytes.Equal(wire[7:], expected) {
					t.Fatalf("wire %x", wire)
				}
			default:
				t.Fatal("missing wire")
			}
			// The transport performs one exchange. No retry appears on the listener.
			_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(20 * time.Millisecond))
			if c, e := listener.Accept(); e == nil {
				c.Close()
				t.Fatal("write retried")
			}
		})
	}
}
