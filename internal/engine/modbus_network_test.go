package engine

import (
	"context"
	"errors"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
	"io"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModbusDiscoveryPlanBoundsExactTargets(t *testing.T) {
	p, e := PrepareModbusDiscovery("127.0.0.0/24", 502, 500, 8)
	if e != nil || len(p.Targets) != 254 || p.Targets[0] != "127.0.0.1" || p.Targets[253] != "127.0.0.254" {
		t.Fatalf("%+v %v", p, e)
	}
	for _, bad := range []string{"localhost", "127.0.0.0/23", "127.0.0.1/24", "127.0.0.1,127.0.0.1", "0.0.0.0", "224.0.0.1", "255.255.255.255", "::%lo", "127.0.0.1,", "::/24"} {
		if _, e := PrepareModbusDiscovery(bad, 502, 100, 1); e == nil {
			t.Fatal(bad)
		}
	}
	p, e = PrepareModbusDiscovery("::1,127.0.0.1", 1502, 100, 1)
	if e != nil || len(p.Targets) != 2 {
		t.Fatal(p, e)
	}
	for _, q := range []ModbusDiscoveryPlan{{Targets: []string{"127.0.0.1"}, Port: 0, TimeoutMS: 100, Concurrency: 1}, {Targets: []string{"localhost"}, Port: 502, TimeoutMS: 100, Concurrency: 1}, {Targets: []string{"127.0.0.1"}, Port: 502, TimeoutMS: 99, Concurrency: 1}, {Targets: []string{"127.0.0.1"}, Port: 502, TimeoutMS: 100, Concurrency: 33}} {
		called := false
		e := discoverModbusTCP(context.Background(), q, func(context.Context, string, string) (net.Conn, error) { called = true; return nil, nil }, nil)
		if e == nil || called {
			t.Fatal("invalid plan dialed")
		}
	}
}
func TestModbusDiscoveryLoopbackClosesWithoutSending(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			var b [1]byte
			_, e = c.Read(b[:])
		}
		done <- e
	}()
	p, _ := PrepareModbusDiscovery("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, 500, 1)
	var results []ModbusDiscoveryResult
	e = DiscoverModbusTCP(context.Background(), p, func(r ModbusDiscoveryResult) { results = append(results, r) })
	if e != nil || len(results) != 1 || !results[0].Open || results[0].Completed != 1 {
		t.Fatal(results, e)
	}
	if e = <-done; !errors.Is(e, io.EOF) {
		t.Fatal("discovery sent bytes or leaked connection", e)
	}
}
func TestModbusDiscoveryConcurrencyCancellationNoRetry(t *testing.T) {
	p, _ := PrepareModbusDiscovery("127.0.0.0/29", 502, 2000, 2)
	ctx, cancel := context.WithCancel(context.Background())
	var active, max, calls atomic.Int32
	ready := make(chan struct{}, 2)
	done := make(chan error, 1)
	go func() {
		done <- discoverModbusTCP(ctx, p, func(ctx context.Context, network, address string) (net.Conn, error) {
			calls.Add(1)
			n := active.Add(1)
			for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
			}
			ready <- struct{}{}
			<-ctx.Done()
			active.Add(-1)
			return nil, ctx.Err()
		}, nil)
	}()
	<-ready
	<-ready
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish")
	}
	if calls.Load() != 2 || max.Load() != 2 {
		t.Fatal(calls.Load(), max.Load())
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	called := false
	_ = discoverModbusTCP(canceled, p, func(context.Context, string, string) (net.Conn, error) { called = true; return nil, nil }, nil)
	if called {
		t.Fatal("canceled plan dialed")
	}
}
func TestModbusSerialNormalizeBounds(t *testing.T) {
	p := normalizeModbusPorts([]string{"COM10", "COM2", "COM2", "bad\n", ""}, false)
	if !reflect.DeepEqual(p.Names, []string{"COM2", "COM10"}) {
		t.Fatal(p)
	}
	names := make([]string, 300)
	for i := range names {
		names[i] = time.Unix(int64(i), 0).Format(time.RFC3339)
	}
	if p = normalizeModbusPorts(names, false); len(p.Names) != 256 || !p.Truncated {
		t.Fatal(p)
	}
}

type gapTestHandler struct {
	modbus.ClientHandler
	mu   sync.Mutex
	sent []time.Time
}

func (h *gapTestHandler) Send(b []byte) ([]byte, error) {
	h.mu.Lock()
	h.sent = append(h.sent, time.Now())
	h.mu.Unlock()
	return b, nil
}
func TestModbusRequestGapSharedAndNotLatency(t *testing.T) {
	ctx := withModbusGap(context.Background())
	base := &gapTestHandler{}
	h := &modbusTimedHandler{ClientHandler: base, ctx: ctx, gap: 80 * time.Millisecond}
	if _, e := h.Send(nil); e != nil {
		t.Fatal(e)
	}
	other := &modbusTimedHandler{ClientHandler: base, ctx: ctx, gap: 80 * time.Millisecond}
	start := time.Now()
	_, e := other.Send(nil)
	var op ModbusOperation
	observer := WithModbusObserver(ctx, func(o ModbusOperation) { op = o })
	observeModbusTimedOperation(observer, config.Request{Action: "read-holding"}, start, e, other)
	if len(base.sent) != 2 || base.sent[1].Sub(base.sent[0]) < 70*time.Millisecond || op.Duration > 50*time.Millisecond {
		t.Fatal(base.sent, op.Duration)
	}
	canceled, cancel := context.WithCancel(ctx)
	h.ctx = canceled
	h.gap = time.Second
	cancel()
	if _, e := h.Send(nil); !errors.Is(e, context.Canceled) || len(base.sent) != 2 {
		t.Fatal("cancel sent request", e)
	}
	for _, r := range []config.Request{{Params: map[string]any{"request_timeout_ms": 0}}, {Params: map[string]any{"connect_timeout_ms": 60001}}, {Params: map[string]any{"request_gap_ms": -1}}} {
		if _, _, e := modbusTransportTimeouts(r, time.Second); e == nil {
			t.Fatal(r)
		}
	}
}
func TestModbusExplicitRequestTimeoutLoopback(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, c)
	}()
	r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://" + ln.Addr().String(), Timeout: "2s", Params: map[string]any{"unit": 1, "address": 0, "count": 1, "connect_timeout_ms": 500, "request_timeout_ms": 20, "request_gap_ms": 0}}
	start := time.Now()
	e = Run(context.Background(), r, false, nil)
	if e == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatal(e, time.Since(start))
	}
	<-done
}

func TestModbusRequestGapThenPauseNeverSendsUntilResume(t *testing.T) {
	p := NewModbusPauseController()
	ctx := WithModbusPause(withModbusGap(context.Background()), p)
	base := &gapTestHandler{}
	h := &modbusTimedHandler{ClientHandler: base, ctx: ctx, gap: 100 * time.Millisecond}
	_, _ = h.Send(nil)
	done := make(chan error, 1)
	go func() { _, err := h.Send(nil); done <- err }()
	time.Sleep(20 * time.Millisecond)
	p.SetPaused(true)
	time.Sleep(120 * time.Millisecond)
	base.mu.Lock()
	count := len(base.sent)
	base.mu.Unlock()
	if count != 1 {
		p.SetPaused(false)
		<-done
		t.Fatal("sent while paused during gap")
	}
	p.SetPaused(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resume blocked")
	}
}
func TestModbusImportedTimingExplicitAndSerialBounds(t *testing.T) {
	p, e := ImportMTUIConfig([]byte(`{"device":{"interface":{"tcp":{"ip":"127.0.0.1","port":502}},"connect_timeout_ms":1234,"request_timeout_ms":5678,"request_gap_ms":123}}`), "timing")
	if e != nil {
		t.Fatal(e)
	}
	r := p.Requests[0]
	if r.Int("connect_timeout_ms", 0) != 1234 || r.Int("request_timeout_ms", 0) != 5678 || r.Int("request_gap_ms", 0) != 123 || r.Timeout != "7912ms" {
		t.Fatal(r)
	}
	p, e = ImportMTUIConfig([]byte(`{"device":{"interface":{"serial":{"path":"COM7","baud_rate":9600,"data_bits":8,"parity":"none","stop_bits":1}},"request_timeout_ms":6000}}`), "serial")
	if e != nil || p.Requests[0].Int("request_timeout_ms", 0) != 5000 || len(p.Warnings) == 0 {
		t.Fatal(p, e)
	}
}
