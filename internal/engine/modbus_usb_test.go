package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
)

type usbTestTransport struct {
	mu                sync.Mutex
	exchanges, closes int
	exchange          func(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error)
	closed            chan struct{}
}

func (f *usbTestTransport) Exchange(ctx context.Context, endpoint string, request []byte, settings ModbusRTUSettings, timeout time.Duration) ([]byte, error) {
	f.mu.Lock()
	f.exchanges++
	f.mu.Unlock()
	return f.exchange(ctx, endpoint, request, settings, timeout)
}
func (f *usbTestTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	if f.closed != nil && f.closes == 1 {
		close(f.closed)
	}
	return nil
}
func (f *usbTestTransport) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exchanges, f.closes
}
func usbTestRequest() config.Request {
	return config.Request{Endpoint: "usb://123/0", Action: "read-holding", Params: map[string]any{"unit": 1, "address": 0, "count": 1}}
}
func usbTestResponse(t *testing.T, unit byte, function byte, data []byte) []byte {
	t.Helper()
	p := modbus.NewRTUClientHandler("")
	p.SlaveId = unit
	b, e := p.Encode(&modbus.ProtocolDataUnit{FunctionCode: function, Data: data})
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestModbusUSBSelectedTransportAndRTUFraming(t *testing.T) {
	f := &usbTestTransport{}
	f.exchange = func(ctx context.Context, endpoint string, request []byte, settings ModbusRTUSettings, timeout time.Duration) ([]byte, error) {
		if endpoint != "usb://123/0" || settings != (ModbusRTUSettings{Baud: 9600, DataBits: 8, StopBits: 1, Parity: "N"}) {
			t.Fatalf("unexpected routing/settings %s %+v", endpoint, settings)
		}
		if timeout <= 0 || timeout > time.Second {
			t.Fatalf("timeout not bounded: %v", timeout)
		}
		expected := usbTestResponse(t, 1, 3, []byte{0, 0, 0, 1})
		if !bytes.Equal(request, expected) {
			t.Fatalf("expected complete RTU request %x, got %x", expected, request)
		}
		return usbTestResponse(t, 1, 3, []byte{2, 0x12, 0x34}), nil
	}
	h, e := newModbusUSBHandler(WithModbusRTUTransport(context.Background(), f), usbTestRequest(), 1, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if n, _ := f.counts(); n != 0 {
		t.Fatal("creating handler performed IO")
	}
	data, e := modbus.NewClient(h).ReadHoldingRegisters(0, 1)
	if e != nil || !bytes.Equal(data, []byte{0x12, 0x34}) {
		t.Fatalf("read=%x error=%v", data, e)
	}
	if n, c := f.counts(); n != 1 || c != 0 {
		t.Fatalf("exchanges=%d closes=%d", n, c)
	}
}

func TestModbusUSBFailClosedBeforeIO(t *testing.T) {
	if _, e := newModbusUSBHandler(context.Background(), usbTestRequest(), 1, time.Second); e == nil {
		t.Fatal("missing platform transport accepted")
	}
	for _, endpoint := range []string{"rtu:///dev/ttyUSB0", "usb:///0", "usb://123", "usb://123/0?x=1", "usb://123/0#x", "usb://user@123/0", "usb://123/256", "usb://123/-1", "usb://001/0", "usb://123/%30", "usb://2147483648/0"} {
		if e := validateModbusUSBEndpoint(endpoint); e == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	for _, endpoint := range []string{"usb://0/0", "usb://123/1", "usb://2147483647/255"} {
		if e := validateModbusUSBEndpoint(endpoint); e != nil {
			t.Errorf("rejected %q: %v", endpoint, e)
		}
	}
	f := &usbTestTransport{exchange: func(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error) {
		t.Fatal("unexpected IO")
		return nil, nil
	}}
	ctx := WithModbusRTUTransport(context.Background(), f)
	for _, params := range []map[string]any{{"baud": 299}, {"baud": 4000001}, {"data_bits": 7}, {"stop_bits": 3}, {"parity": "mark"}} {
		r := usbTestRequest()
		r.Params = params
		if _, e := newModbusUSBHandler(ctx, r, 1, time.Second); e == nil {
			t.Errorf("accepted settings %v", params)
		}
	}
	for _, timeout := range []time.Duration{0, -time.Second, 6 * time.Second} {
		if _, e := newModbusUSBHandler(ctx, usbTestRequest(), 1, timeout); e == nil {
			t.Errorf("accepted timeout %v", timeout)
		}
	}
}

func TestModbusUSBRejectsCorruptResponses(t *testing.T) {
	for _, name := range []string{"crc", "unit", "oversized", "truncated"} {
		t.Run(name, func(t *testing.T) {
			f := &usbTestTransport{exchange: func(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error) {
				response := usbTestResponse(t, 1, 3, []byte{2, 0x12, 0x34})
				switch name {
				case "crc":
					response[len(response)-1] ^= 1
				case "unit":
					response = usbTestResponse(t, 2, 3, []byte{2, 0x12, 0x34})
				case "oversized":
					response = make([]byte, 257)
				case "truncated":
					response = []byte{1, 3, 2}
				}
				return response, nil
			}}
			h, e := newModbusUSBHandler(WithModbusRTUTransport(context.Background(), f), usbTestRequest(), 1, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = modbus.NewClient(h).ReadHoldingRegisters(0, 1); e == nil {
				t.Fatal("corrupt response accepted")
			}
		})
	}
}

func TestModbusUSBCancelAndDeadlineCloseBlockedTransport(t *testing.T) {
	for _, name := range []string{"cancel", "deadline"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			f := &usbTestTransport{closed: make(chan struct{})}
			f.exchange = func(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error) {
				close(started)
				<-f.closed
				return nil, errors.New("port closed")
			}
			timeout := time.Second
			if name == "deadline" {
				timeout = 30 * time.Millisecond
			}
			h, e := newModbusUSBHandler(WithModbusRTUTransport(ctx, f), usbTestRequest(), 1, timeout)
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { _, e := modbus.NewClient(h).ReadHoldingRegisters(0, 1); done <- e }()
			<-started
			if name == "cancel" {
				cancel()
			}
			select {
			case e := <-done:
				expected := context.Canceled
				if name == "deadline" {
					expected = context.DeadlineExceeded
				}
				if !errors.Is(e, expected) {
					t.Fatalf("got %v, want %v", e, expected)
				}
			case <-time.After(time.Second):
				t.Fatal("cancel did not close blocked platform IO")
			}
			if n, c := f.counts(); n != 1 || c != 1 {
				t.Fatalf("exchanges=%d closes=%d", n, c)
			}
		})
	}
}

func TestModbusUSBNeverRetriesAmbiguousWrite(t *testing.T) {
	f := &usbTestTransport{exchange: func(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error) {
		return nil, errors.New("timeout after partial write")
	}}
	h, e := newModbusUSBHandler(WithModbusRTUTransport(context.Background(), f), usbTestRequest(), 1, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = modbus.NewClient(h).WriteSingleRegister(0, 23); e == nil || !strings.Contains(e.Error(), "partial write") {
		t.Fatalf("wrong write error %v", e)
	}
	if n, _ := f.counts(); n != 1 {
		t.Fatalf("write retried %d times", n)
	}
}

func TestRunModbusUSBRoutesWithoutSerialDeviceFallback(t *testing.T) {
	f := &usbTestTransport{exchange: func(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error) {
		return usbTestResponse(t, 1, 3, []byte{2, 0x12, 0x34}), nil
	}}
	r := usbTestRequest()
	r.Protocol = "modbus"
	r.Timeout = "1s"
	events := 0
	err := runModbus(WithModbusRTUTransport(context.Background(), f), r, func(e Event) {
		if e.Kind == "registers" {
			events++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := f.counts(); n != 1 || events != 1 {
		t.Fatalf("USB route exchanges=%d register events=%d", n, events)
	}
	if err = runModbus(context.Background(), r, func(Event) {}); err == nil || !strings.Contains(err.Error(), "USB transport is unavailable") {
		t.Fatalf("missing transport did not fail closed: %v", err)
	}
}
