package engine

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

// ModbusOperation contains operational metadata only, never payloads,
// credentials, endpoint text or verbatim errors. Duration excludes sampling waits.
type ModbusOperation struct {
	Time                      time.Time
	Action                    string
	Unit, Address, Count      int
	Duration                  time.Duration
	Write, Success, Cancelled bool
	ErrorClass                string
}
type modbusObserverKey struct{}

// WithModbusObserver is opt-in and leaves ordinary CLI/protocol event streams
// unchanged. The observer executes synchronously; UI callers queue bounded work.
func WithModbusObserver(ctx context.Context, observer func(ModbusOperation)) context.Context {
	return context.WithValue(ctx, modbusObserverKey{}, observer)
}
func ModbusErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "超时"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "超时"
	}
	return "通信或响应校验失败"
}
func observeModbusOperation(ctx context.Context, r config.Request, start time.Time, err error) {
	observer, _ := ctx.Value(modbusObserverKey{}).(func(ModbusOperation))
	if observer == nil {
		return
	}
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	count := r.Int("count", 1)
	if values, ok := r.Params["values"].([]any); ok {
		count = len(values)
	}
	observer(ModbusOperation{Time: time.Now().UTC(), Action: r.Action, Unit: r.Int("unit", 1), Address: r.Int("address", 0), Count: count, Duration: time.Since(start), Write: r.Mutates(), Success: err == nil, Cancelled: errors.Is(err, context.Canceled), ErrorClass: ModbusErrorClass(err)})
}
