package engine

import (
	"context"
	"errors"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
	"sync/atomic"
	"testing"
	"time"
)

func TestModbusPauseKeepsOriginalSampleBudget(t *testing.T) {
	p := NewModbusPauseController()
	p.SetPaused(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var count atomic.Int32
	first := make(chan struct{})
	done := make(chan error, 1)
	r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Timeout: "2s", Params: map[string]any{"unit": 245, "address": 0, "count": 1, "samples": 3, "interval_ms": 10}}
	go func() {
		done <- Run(WithModbusPause(ctx, p), r, false, func(e Event) {
			if e.Kind == "registers" && count.Add(1) == 1 {
				p.SetPaused(true)
				close(first)
			}
		})
	}()
	time.Sleep(30 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatal("paused request read data")
	}
	p.SetPaused(false)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first sample missing")
	}
	time.Sleep(40 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatal("pause did not gate next sample")
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
	if count.Load() != 3 {
		t.Fatal("sample budget reset", count.Load())
	}
}
func TestModbusPauseDeadlineAndCancelRemainEffective(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		p := NewModbusPauseController()
		p.SetPaused(true)
		ctx, cancel := context.WithCancel(context.Background())
		if timeout {
			ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
		} else {
			cancel()
		}
		err := p.Wait(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
}

func TestModbusPauseNeverQueuesOrReplaysWrites(t *testing.T) {
	p := NewModbusPauseController()
	p.SetPaused(true)
	var writes atomic.Int32
	r := config.Request{Protocol: "modbus", Action: "write-register", Endpoint: "mock://local", Timeout: "1s", Params: map[string]any{"unit": 245, "address": 22, "value": 42}}
	if err := Run(WithModbusPause(context.Background(), p), r, false, func(Event) { writes.Add(1) }); err == nil || writes.Load() != 0 {
		t.Fatal("write gate bypassed")
	}
	if err := Run(WithModbusPause(context.Background(), p), r, true, func(e Event) {
		if e.Kind == "written" {
			writes.Add(1)
		}
	}); err != nil || writes.Load() != 1 {
		t.Fatal("write queued by pause", err)
	}
	p.SetPaused(false)
	time.Sleep(20 * time.Millisecond)
	if writes.Load() != 1 {
		t.Fatal("resume replayed write")
	}
}

type pauseSendMock struct {
	modbus.ClientHandler
	hits atomic.Int32
}

func (m *pauseSendMock) Send([]byte) ([]byte, error) { m.hits.Add(1); return []byte{1}, nil }
func TestPauseDuringRequestGapGatesActualSend(t *testing.T) {
	p := NewModbusPauseController()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = withModbusGap(WithModbusPause(ctx, p))
	state := ctx.Value(modbusGapKey{}).(*modbusGapState)
	state.last = time.Now()
	mock := &pauseSendMock{}
	handler := &modbusTimedHandler{ClientHandler: mock, ctx: ctx, gap: 50 * time.Millisecond}
	done := make(chan error, 1)
	go func() { _, err := handler.Send(nil); done <- err }()
	time.Sleep(10 * time.Millisecond)
	p.SetPaused(true)
	time.Sleep(70 * time.Millisecond)
	if mock.hits.Load() != 0 {
		t.Fatal("request sent after gap while paused")
	}
	p.SetPaused(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not release gap")
	}
	if mock.hits.Load() != 1 || handler.waited < 50*time.Millisecond {
		t.Fatal("send count or excluded wait wrong")
	}
}
