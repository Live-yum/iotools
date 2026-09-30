package engine

import (
	"context"
	"encoding/binary"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net"
	"sync"
	"testing"
)

func TestModbusRealTCPReadWrite(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	value := uint16(123)
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
				for {
					header := make([]byte, 7)
					if _, e := io.ReadFull(c, header); e != nil {
						return
					}
					n := binary.BigEndian.Uint16(header[4:6])
					if n < 2 || n > 254 {
						return
					}
					pdu := make([]byte, int(n)-1)
					if _, e := io.ReadFull(c, pdu); e != nil {
						return
					}
					var out []byte
					mu.Lock()
					switch pdu[0] {
					case 3:
						out = []byte{3, 2, byte(value >> 8), byte(value)}
					case 6:
						value = binary.BigEndian.Uint16(pdu[3:5])
						out = pdu
					default:
						out = []byte{pdu[0] | 0x80, 1}
					}
					mu.Unlock()
					binary.BigEndian.PutUint16(header[4:6], uint16(len(out)+1))
					if _, e := c.Write(append(header, out...)); e != nil {
						return
					}
				}
			}()
		}
	}()
	defer func() { l.Close(); <-done; wg.Wait() }()
	r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://" + l.Addr().String(), Params: map[string]any{"address": 0, "count": 1, "unit": 1}, Timeout: "2s"}
	check := func(want uint16) {
		t.Helper()
		seen := false
		if e := Run(context.Background(), r, false, func(e Event) { rows := e.Data.([]map[string]any); seen = rows[0]["u16"] == want }); e != nil {
			t.Fatal(e)
		}
		if !seen {
			t.Fatal("register mismatch")
		}
	}
	check(123)
	r.Action = "write-register"
	r.Params["value"] = 456
	if e := Run(context.Background(), r, true, nil); e != nil {
		t.Fatal(e)
	}
	r.Action = "read-holding"
	check(456)
}
func TestRegisterInterpretation(t *testing.T) {
	rows := decodeRegisters([]byte{0x3f, 0x80, 0, 0}, 10, "ABCD")
	if rows[0]["f32"] != "1" || rows[0]["address"] != 10 {
		t.Fatalf("bad interpretation: %v", rows)
	}
	if len(decodeRegisters([]byte{1}, 0, "ABCD")) != 0 {
		t.Fatal("odd byte accepted")
	}
}

func TestModbusRTUOverTCP(t *testing.T) {
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
		request := make([]byte, 8)
		if _, e = io.ReadFull(c, request); e != nil {
			return
		}
		response := []byte{1, 3, 2, 0, 42}
		crc := uint16(0xffff)
		for _, b := range response {
			crc ^= uint16(b)
			for i := 0; i < 8; i++ {
				if crc&1 != 0 {
					crc = (crc >> 1) ^ 0xa001
				} else {
					crc >>= 1
				}
			}
		}
		response = append(response, byte(crc), byte(crc>>8))
		c.Write(response)
	}()
	seen := false
	r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "rtu+tcp://" + l.Addr().String(), Timeout: "2s", Params: map[string]any{"unit": 1, "address": 0, "count": 1}}
	if e := Run(context.Background(), r, false, func(e Event) { seen = e.Data.([]map[string]any)[0]["u16"] == uint16(42) }); e != nil {
		t.Fatal(e)
	}
	<-done
	if !seen {
		t.Fatal("RTU/TCP register value missing")
	}
}
