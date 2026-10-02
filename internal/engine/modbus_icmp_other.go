//go:build !linux && !darwin && !windows

package engine

import "context"

func probeModbusPing(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrModbusICMPUnavailable
}
