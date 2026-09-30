package engine

import (
	"context"
	"sync"
)

// ModbusPauseController gates the next read without reconnecting, clearing
// samples or replaying a write. A transaction already sent may finish normally.
// The original request deadline continues to apply while paused.
type ModbusPauseController struct {
	mu      sync.Mutex
	paused  bool
	changed chan struct{}
}

func NewModbusPauseController() *ModbusPauseController {
	return &ModbusPauseController{changed: make(chan struct{})}
}
func (p *ModbusPauseController) SetPaused(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changed == nil {
		p.changed = make(chan struct{})
	}
	if p.paused != paused {
		p.paused = paused
		close(p.changed)
		p.changed = make(chan struct{})
	}
}
func (p *ModbusPauseController) Paused() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.paused }
func (p *ModbusPauseController) Wait(ctx context.Context) error {
	for {
		p.mu.Lock()
		if p.changed == nil {
			p.changed = make(chan struct{})
		}
		paused, changed := p.paused, p.changed
		p.mu.Unlock()
		if !paused {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

type modbusPauseKey struct{}

func WithModbusPause(ctx context.Context, p *ModbusPauseController) context.Context {
	return context.WithValue(ctx, modbusPauseKey{}, p)
}
func waitModbusPause(ctx context.Context) error {
	p, _ := ctx.Value(modbusPauseKey{}).(*ModbusPauseController)
	if p == nil {
		return ctx.Err()
	}
	return p.Wait(ctx)
}
