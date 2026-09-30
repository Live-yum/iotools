package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
)

func runModbus(ctx context.Context, r config.Request, emit Emit) error {
	addr, count, unit := r.Int("address", 0), r.Int("count", 1), r.Int("unit", 1)
	if addr < 0 || addr > 65535 || count < 1 || count > 125 || addr+count > 65536 || unit < 1 || unit > 247 {
		return fmt.Errorf("invalid address/count/unit (count 1..125, unit 1..247; broadcast disabled)")
	}
	order := r.String("word_order", "ABCD")
	switch order {
	case "ABCD", "CDAB", "BADC", "DCBA":
	default:
		return fmt.Errorf("word_order must be ABCD, CDAB, BADC or DCBA")
	}
	timeout, _ := r.Duration()
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	var handler modbus.ClientHandler
	var close func() error
	switch {
	case strings.HasPrefix(r.Endpoint, "tcp://"):
		h := modbus.NewTCPClientHandler(strings.TrimPrefix(r.Endpoint, "tcp://"))
		h.Timeout = timeout
		h.SlaveId = byte(unit)
		handler = h
		close = h.Close
	case strings.HasPrefix(r.Endpoint, "rtu+tcp://"):
		h := modbus.NewRTUClientHandler("")
		h.SlaveId = byte(unit)
		handler = &rtuTCPHandler{packager: h, address: strings.TrimPrefix(r.Endpoint, "rtu+tcp://"), ctx: ctx, timeout: timeout}
		close = func() error { return nil }
	case strings.HasPrefix(r.Endpoint, "rtu://"):
		h := modbus.NewRTUClientHandler(strings.TrimPrefix(r.Endpoint, "rtu://"))
		h.BaudRate = r.Int("baud", 9600)
		h.DataBits = r.Int("data_bits", 8)
		h.Parity = r.String("parity", "N")
		h.StopBits = r.Int("stop_bits", 1)
		h.SlaveId = byte(unit)
		h.Timeout = timeout
		handler = h
		close = h.Close
	default:
		return fmt.Errorf("Modbus endpoint must use tcp://host:port, rtu+tcp://host:port or rtu://device")
	}
	defer close()
	client := modbus.NewClient(handler)
	iterations := r.Int("samples", 1)
	interval := r.Int("interval_ms", 1000)
	if iterations < 1 || iterations > 100000 || interval < 10 {
		return fmt.Errorf("samples must be 1..100000, interval_ms >=10")
	}
	if r.Mutates() {
		iterations = 1
	}
	for n := 0; n < iterations; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var data []byte
		var e error
		switch r.Action {
		case "read-holding":
			data, e = client.ReadHoldingRegisters(uint16(addr), uint16(count))
		case "read-input":
			data, e = client.ReadInputRegisters(uint16(addr), uint16(count))
		case "read-coils":
			data, e = client.ReadCoils(uint16(addr), uint16(count))
		case "read-discrete":
			data, e = client.ReadDiscreteInputs(uint16(addr), uint16(count))
		case "write-register":
			v := r.Int("value", -1)
			if v < 0 || v > 65535 {
				return fmt.Errorf("register value must be 0..65535")
			}
			data, e = client.WriteSingleRegister(uint16(addr), uint16(v))
		case "write-coil":
			v := uint16(0)
			if r.Bool("value") {
				v = 0xff00
			}
			data, e = client.WriteSingleCoil(uint16(addr), v)
		case "write-registers":
			values, ok := r.Params["values"].([]any)
			if !ok || len(values) < 1 || len(values) > 123 || addr+len(values) > 65536 {
				return fmt.Errorf("values must contain 1..123 register integers")
			}
			b := make([]byte, len(values)*2)
			for i, v := range values {
				x, ok := v.(int)
				if !ok || x < 0 || x > 65535 {
					return fmt.Errorf("register values must be integers 0..65535")
				}
				binary.BigEndian.PutUint16(b[i*2:], uint16(x))
			}
			data, e = client.WriteMultipleRegisters(uint16(addr), uint16(len(values)), b)
		default:
			return unsupported(r, "read-holding", "read-input", "read-coils", "read-discrete", "write-register", "write-registers", "write-coil")
		}
		if e != nil {
			return e
		}
		if r.Action == "read-holding" || r.Action == "read-input" {
			rows := decodeRegisters(data, addr, r.String("word_order", "ABCD"))
			send(emit, "registers", rows)
		} else if r.Action == "read-coils" || r.Action == "read-discrete" {
			bits := make([]bool, count)
			for i := range bits {
				bits[i] = data[i/8]&(1<<uint(i%8)) != 0
			}
			send(emit, "bits", map[string]any{"address": addr, "values": bits})
		} else {
			send(emit, "written", map[string]any{"address": addr, "reply_hex": fmt.Sprintf("%x", data)})
		}
		if n+1 < iterations {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(interval) * time.Millisecond):
			}
		}
	}
	return nil
}
func decodeRegisters(data []byte, address int, order string) []map[string]any {
	rows := make([]map[string]any, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		v := binary.BigEndian.Uint16(data[i : i+2])
		row := map[string]any{"address": address + i/2, "u16": v, "i16": int16(v), "hex": fmt.Sprintf("0x%04X", v), "binary": fmt.Sprintf("%016b", v), "ascii": string([]byte{byte(v >> 8), byte(v)})}
		if i+3 < len(data) {
			b := [4]byte{data[i], data[i+1], data[i+2], data[i+3]}
			switch order {
			case "CDAB":
				b = [4]byte{b[2], b[3], b[0], b[1]}
			case "BADC":
				b = [4]byte{b[1], b[0], b[3], b[2]}
			case "DCBA":
				b = [4]byte{b[3], b[2], b[1], b[0]}
			}
			x := binary.BigEndian.Uint32(b[:])
			row["u32"] = x
			row["i32"] = int32(x)
			row["f32"] = fmt.Sprint(math.Float32frombits(x))
		}
		rows = append(rows, row)
	}
	return rows
}
