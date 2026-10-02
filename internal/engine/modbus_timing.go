package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
	"sync"
	"time"
)

type modbusGapKey struct{}
type modbusGapState struct {
	mu   sync.Mutex
	last time.Time
}

func withModbusGap(ctx context.Context) context.Context {
	if _, ok := ctx.Value(modbusGapKey{}).(*modbusGapState); ok {
		return ctx
	}
	return context.WithValue(ctx, modbusGapKey{}, &modbusGapState{})
}

type modbusTimedHandler struct {
	modbus.ClientHandler
	ctx    context.Context
	gap    time.Duration
	waited time.Duration
}

func (h *modbusTimedHandler) Send(request []byte) ([]byte, error) {
	state, _ := h.ctx.Value(modbusGapKey{}).(*modbusGapState)
	if state != nil && h.gap > 0 {
		state.mu.Lock()
		last := state.last
		state.mu.Unlock()
		wait := time.Until(last.Add(h.gap))
		if !last.IsZero() && wait > 0 {
			start := time.Now()
			timer := time.NewTimer(wait)
			select {
			case <-h.ctx.Done():
				timer.Stop()
				h.waited += time.Since(start)
				return nil, h.ctx.Err()
			case <-timer.C:
			}
			h.waited += time.Since(start)
		}
	}
	pauseStart := time.Now()
	pauseErr := waitModbusPause(h.ctx)
	h.waited += time.Since(pauseStart)
	if pauseErr != nil {
		return nil, pauseErr
	}
	if err := h.ctx.Err(); err != nil {
		return nil, err
	}
	response, err := h.ClientHandler.Send(request)
	if state != nil {
		state.mu.Lock()
		state.last = time.Now()
		state.mu.Unlock()
	}
	return response, err
}
func observeModbusTimedOperation(ctx context.Context, r config.Request, start time.Time, err error, h modbus.ClientHandler) {
	if timed, ok := h.(*modbusTimedHandler); ok {
		start = start.Add(timed.waited)
		timed.waited = 0
	}
	observeModbusOperation(ctx, r, start, err)
}
func modbusTransportTimeouts(r config.Request, fallback time.Duration) (time.Duration, time.Duration, error) {
	connect, request := fallback, fallback
	for _, f := range []struct {
		key string
		out *time.Duration
	}{{"connect_timeout_ms", &connect}, {"request_timeout_ms", &request}} {
		if raw, ok := r.Params[f.key]; ok {
			n, e := exactInt(raw)
			if e != nil || n < 1 || n > 60000 {
				return 0, 0, fmt.Errorf("%s must be1..60000", f.key)
			}
			*f.out = time.Duration(n) * time.Millisecond
		}
	}
	if raw, ok := r.Params["request_gap_ms"]; ok {
		n, e := exactInt(raw)
		if e != nil || n < 0 || n > 60000 {
			return 0, 0, fmt.Errorf("request_gap_ms must be0..60000")
		}
	}
	return connect, request, nil
}
