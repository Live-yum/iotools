package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
)

func runModbus(ctx context.Context, r config.Request, emit Emit) error {
	if r.Mutates() {
		ctx = WithModbusPause(ctx, nil)
	}
	if !r.Mutates() {
		if err := waitModbusPause(ctx); err != nil {
			return err
		}
	}
	ctx = withModbusGap(ctx)
	if r.Action == "read-write-registers" {
		if _, _, _, err := validateModbusReadWrite(r); err != nil {
			return err
		}
	}
	if r.Action == "scan-units" || r.Action == "sweep-holding" || r.Action == "sweep-input" || r.Action == "sweep-coils" || r.Action == "sweep-discrete" || r.Action == "search-holding" {
		return runModbusRange(ctx, r, emit)
	}
	if r.Action == "read-raw" || r.Action == "write-raw" {
		if _, err := validateRawPDU(r); err != nil {
			return err
		}
	}
	if r.Action == "write-typed" {
		var err error
		r, err = prepareTypedWrite(r)
		if err != nil {
			return err
		}
	}

	annotations, err := parseRegisterAnnotations(r)
	if err != nil {
		return err
	}
	addr, count, unit := r.Int("address", 0), r.Int("count", 1), r.Int("unit", 1)
	maxCount := 125
	if r.Action == "read-coils" || r.Action == "read-discrete" {
		maxCount = 2000
	}
	if r.Action == "write-coils" {
		maxCount = 1968
	}
	if addr < 0 || addr > 65535 || count < 1 || count > maxCount || addr+count > 65536 || unit < 1 || unit > 247 {
		return fmt.Errorf("invalid address/count/unit (count 1..%d, unit 1..247; broadcast disabled)", maxCount)
	}
	order := r.String("word_order", "ABCD")
	switch order {
	case "ABCD", "CDAB", "BADC", "DCBA":
	default:
		return fmt.Errorf("word_order must be ABCD, CDAB, BADC or DCBA")
	}
	timeout, _ := r.Duration()
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return context.DeadlineExceeded
	}
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	connectTimeout, requestTimeout, err := modbusTransportTimeouts(r, timeout)
	if err != nil {
		return err
	}
	if strings.HasPrefix(r.Endpoint, "rtu://") && requestTimeout > 5*time.Second {
		return fmt.Errorf("serial request_timeout_ms must not exceed5000; cancellation is bounded by the serial read timeout")
	}
	var handler modbus.ClientHandler
	var close func() error
	switch {
	case r.Endpoint == "mock://local":
		h := modbus.NewTCPClientHandler("")
		h.SlaveId = byte(unit)
		handler = &mockModbusHandler{packager: h}
		close = func() error { return nil }
		send(emit, "simulation", map[string]any{"simulated": true, "endpoint": r.Endpoint, "message": "本地内存模拟器；未连接真实设备；写入仅在当前进程保存"})
	case strings.HasPrefix(r.Endpoint, "tcp://"):
		h := modbus.NewTCPClientHandler(strings.TrimPrefix(r.Endpoint, "tcp://"))
		h.Timeout = timeout
		h.SlaveId = byte(unit)
		handler = &tcpContextHandler{packager: h, address: strings.TrimPrefix(r.Endpoint, "tcp://"), ctx: ctx, timeout: requestTimeout, connectTimeout: connectTimeout}
		close = func() error { return nil }
	case strings.HasPrefix(r.Endpoint, "rtu+tcp://"):
		h := modbus.NewRTUClientHandler("")
		h.SlaveId = byte(unit)
		handler = &rtuTCPHandler{packager: h, address: strings.TrimPrefix(r.Endpoint, "rtu+tcp://"), ctx: ctx, timeout: requestTimeout, connectTimeout: connectTimeout}
		close = func() error { return nil }
	case strings.HasPrefix(r.Endpoint, "usb://"):
		h, err := newModbusUSBHandler(ctx, r, unit, requestTimeout)
		if err != nil {
			return err
		}
		handler = h
		close = func() error { return nil }
	case strings.HasPrefix(r.Endpoint, "rtu://"):
		if err := checkModbusDeviceSerial(); err != nil {
			return err
		}
		h := modbus.NewRTUClientHandler(strings.TrimPrefix(r.Endpoint, "rtu://"))
		h.BaudRate = r.Int("baud", 9600)
		h.DataBits = r.Int("data_bits", 8)
		h.Parity = r.String("parity", "N")
		h.StopBits = r.Int("stop_bits", 1)
		h.SlaveId = byte(unit)
		h.Timeout = requestTimeout
		handler = h
		close = h.Close
	default:
		return fmt.Errorf("Modbus endpoint must use tcp://host:port, rtu+tcp://host:port, rtu://device, Android usb://device-id/port-index or explicit mock://local")
	}
	defer close()
	handler = &modbusTimedHandler{ClientHandler: handler, ctx: ctx, gap: time.Duration(r.Int("request_gap_ms", 0)) * time.Millisecond}
	if r.Action == "read-device-id" {
		start := time.Now()
		err := runModbusDeviceID(ctx, r, handler, emit)
		observeModbusTimedOperation(ctx, r, start, err, handler)
		return err
	}
	if r.Action == "read-raw" || r.Action == "write-raw" {
		start := time.Now()
		err := runModbusRaw(r, handler, emit)
		observeModbusTimedOperation(ctx, r, start, err, handler)
		return err
	}
	if r.Action == "read-write-registers" {
		start := time.Now()
		err := runModbusReadWrite(r, handler, emit)
		observeModbusTimedOperation(ctx, r, start, err, handler)
		return err
	}
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
		if !r.Mutates() {
			if err := waitModbusPause(ctx); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		operationStart := time.Now()
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
		case "write-coils":
			values, ok := r.Params["values"].([]any)
			if !ok || len(values) < 1 || len(values) > 1968 || addr+len(values) > 65536 {
				return fmt.Errorf("values must contain 1..1968 booleans")
			}
			b := make([]byte, (len(values)+7)/8)
			for i, v := range values {
				bit, ok := v.(bool)
				if !ok {
					return fmt.Errorf("coil values must be explicit booleans")
				}
				if bit {
					b[i/8] |= 1 << uint(i%8)
				}
			}
			data, e = client.WriteMultipleCoils(uint16(addr), uint16(len(values)), b)
		case "write-registers":
			values, ok := r.Params["values"].([]any)
			if !ok || len(values) < 1 || len(values) > 123 || addr+len(values) > 65536 {
				return fmt.Errorf("values must contain 1..123 register integers")
			}
			b := make([]byte, len(values)*2)
			for i, v := range values {
				x, err := exactInt(v)
				if err != nil || x < 0 || x > 65535 {
					return fmt.Errorf("register values must be integers 0..65535")
				}
				binary.BigEndian.PutUint16(b[i*2:], uint16(x))
			}
			data, e = client.WriteMultipleRegisters(uint16(addr), uint16(len(values)), b)
		default:
			return unsupported(r, "read-holding", "read-input", "read-coils", "read-discrete", "write-register", "write-registers", "write-coil", "write-coils")
		}
		if e == nil && (r.Action == "read-holding" || r.Action == "read-input") && len(data) != count*2 {
			e = fmt.Errorf("truncated or oversized Modbus register response")
		}
		if e == nil && (r.Action == "read-coils" || r.Action == "read-discrete") && len(data) < (count+7)/8 {
			e = fmt.Errorf("truncated Modbus bit response")
		}
		observeModbusTimedOperation(ctx, r, operationStart, e, handler)
		if e != nil {
			return e
		}
		if r.Action == "read-holding" || r.Action == "read-input" {
			rows := decodeRegisters(data, addr, r.String("word_order", "ABCD"))
			if err := annotateRegisters(rows, annotations, order); err != nil {
				return err
			}
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
