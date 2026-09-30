package engine

import (
	"context"
	"errors"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/ua"
	"io"
	"net"
	"time"
)

func runOPCUA(ctx context.Context, r config.Request, emit Emit) error {
	return runOPCUARestoring(ctx, r, emit, runOPCUASession)
}

// Only failed subscription sessions reconnect. Each new session repeats endpoint
// selection and certificate validation; writes/calls are never retried.
func runOPCUARestoring(ctx context.Context, r config.Request, emit Emit, run func(context.Context, config.Request, Emit) error) error {
	enabled := r.Action == "subscribe"
	if v, ok := r.Params["auto_reconnect"].(bool); ok {
		enabled = enabled && v
	}
	if !enabled {
		return run(ctx, r, emit)
	}
	params := map[string]any{}
	for k, v := range r.Params {
		params[k] = v
	}
	r.Params = params
	remaining := r.Int("max_events", 10)
	delay := time.Duration(r.Int("reconnect_interval_ms", 1000)) * time.Millisecond
	for attempt := 0; ; attempt++ {
		r.Params["max_events"] = remaining
		err := run(ctx, r, func(e Event) {
			if e.Kind == "notification" {
				remaining--
			}
			if emit != nil {
				emit(e)
			}
		})
		if err == nil || remaining <= 0 {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= 5 || !retryOPCUAError(err) {
			return err
		}
		send(emit, "reconnecting", map[string]any{"attempt": attempt + 1, "remaining_events": remaining, "message": "连接中断，重新验证证书并恢复订阅；不重放写入"})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func retryOPCUAError(err error) bool {
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ua.StatusBadConnectionClosed) || errors.Is(err, ua.StatusBadSecureChannelClosed) || errors.Is(err, ua.StatusBadSessionClosed) || errors.Is(err, ua.StatusBadTimeout)
}
