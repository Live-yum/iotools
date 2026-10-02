package tui

import (
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"sync"
	"testing"
	"time"
)

func TestModbusPauseResumeKeepsUIHistoryAndRequest(t *testing.T) {
	u, _ := newTestUI(t)
	ready, first := make(chan struct{}), make(chan struct{})
	var readyOnce, firstOnce sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) {
		readyOnce.Do(func() { close(ready) })
		if u.inspector.modbus != nil && u.inspector.modbus.interaction != nil && len(u.inspector.modbus.interaction.frames) > 0 {
			firstOnce.Do(func() { close(first) })
		}
	})
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	r := config.Request{ID: "pause-test", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Timeout: "3s", Params: map[string]any{"unit": 246, "address": 0, "count": 1, "samples": 2, "interval_ms": 300}}
	u.App.QueueUpdateDraw(func() { u.start(r) })
	select {
	case <-first:
	case <-time.After(time.Second):
		u.App.QueueUpdateDraw(u.quit)
		t.Fatal("no sample")
	}
	var history *modbusInteraction
	u.App.QueueUpdateDraw(func() {
		history = u.inspector.modbus.interaction
		u.inspector.modbusDispatch("pause")
		if !u.modbusPause.Paused() {
			t.Error("not paused")
		}
	})
	time.Sleep(400 * time.Millisecond)
	u.App.QueueUpdateDraw(func() {
		if !u.running || len(history.frames) != 1 {
			t.Error("pause ended request or read more", len(history.frames))
		}
		u.inspector.modbusDispatch("pause")
	})
	deadline := time.Now().Add(time.Second)
	running := true
	for running && time.Now().Before(deadline) {
		u.App.QueueUpdateDraw(func() { running = u.running })
		time.Sleep(time.Millisecond)
	}
	u.App.QueueUpdateDraw(func() {
		if running || u.inspector.modbus.interaction != history || len(history.frames) != 2 {
			t.Error("resume reset history/budget")
		}
		u.quit()
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("quit blocked")
	}
}

func TestQuitUnblocksPausedModbusRequest(t *testing.T) {
	u, _ := newTestUI(t)
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	r := config.Request{ID: "pause-quit", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Timeout: "3s", Params: map[string]any{"unit": 247, "address": 0, "count": 1, "samples": 10, "interval_ms": 100}}
	u.App.QueueUpdateDraw(func() { u.start(r); u.toggleModbusPause(); u.quit() })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("quit left paused request blocked")
	}
}
