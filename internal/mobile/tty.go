// Package mobile connects the real terminal UI to an app-owned, bounded byte
// transport. It does not emulate protocol responses or require a shell/exec.
package mobile

import (
	"bytes"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"io"
	"sync"
)

type TTY struct {
	mu              sync.Mutex
	ready           *sync.Cond
	input, output   bytes.Buffer
	closed, drained bool
	width, height   int
	resize          func()
}

func NewTTY(width, height int) *TTY {
	t := &TTY{width: width, height: height}
	t.ready = sync.NewCond(&t.mu)
	return t
}
func (t *TTY) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return io.ErrClosedPipe
	}
	t.drained = false
	return nil
}
func (t *TTY) Stop() error { return nil }
func (t *TTY) Drain() error {
	t.mu.Lock()
	t.drained = true
	t.ready.Broadcast()
	t.mu.Unlock()
	return nil
}
func (t *TTY) Close() error {
	t.mu.Lock()
	t.closed = true
	t.ready.Broadcast()
	t.mu.Unlock()
	return nil
}
func (t *TTY) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.input.Len() == 0 && !t.closed && !t.drained {
		t.ready.Wait()
	}
	if t.closed || t.drained {
		return 0, io.EOF
	}
	return t.input.Read(p)
}
func (t *TTY) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, io.ErrClosedPipe
	}
	if t.output.Len()+len(p) > 4<<20 {
		return 0, fmt.Errorf("终端输出超过4MiB缓冲，请重新打开终端")
	}
	return t.output.Write(p)
}
func (t *TTY) Input(p []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return io.ErrClosedPipe
	}
	if t.input.Len()+len(p) > 64<<10 {
		return fmt.Errorf("单次输入缓冲最多64KiB")
	}
	_, err := t.input.Write(p)
	t.ready.Broadcast()
	return err
}
func (t *TTY) Output(p []byte) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, _ := t.output.Read(p)
	return n
}
func (t *TTY) NotifyResize(cb func()) { t.mu.Lock(); t.resize = cb; t.mu.Unlock() }
func (t *TTY) WindowSize() (tcell.WindowSize, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return tcell.WindowSize{Width: t.width, Height: t.height}, nil
}
func (t *TTY) Resize(width, height int) error {
	if width < 20 || width > 300 || height < 8 || height > 150 {
		return fmt.Errorf("终端尺寸范围20..300列、8..150行")
	}
	t.mu.Lock()
	t.width, t.height = width, height
	cb := t.resize
	t.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}
