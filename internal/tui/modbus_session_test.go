package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestModbusSessionStatsCountLatencyAndReset(t *testing.T) {
	u, v, r := modbusTestView(t)
	u.modbusSessionStart(r)
	now := time.Now().UTC()
	base := engine.ModbusOperation{Time: now, Action: "read-holding", Unit: 1, Count: 1, Success: true, Duration: 2 * time.Millisecond}
	u.modbusOperation(base)
	base.Duration = 4 * time.Millisecond
	u.modbusOperation(base)
	base.Success = false
	base.ErrorClass = "超时"
	u.modbusOperation(base)
	base.Write = true
	base.Success = true
	u.modbusOperation(base)
	base.Success = false
	base.ErrorClass = "raw secret"
	u.modbusOperation(base)
	base.Cancelled = true
	base.ErrorClass = "已取消"
	u.modbusOperation(base)
	s := u.modbusSession.stats
	if s.reads != 2 || s.readErrors != 1 || s.writes != 1 || s.writeErrors != 1 || s.cancelled != 1 || s.min != 2 || s.max != 4 || s.sum != 6 {
		t.Fatalf("stats wrong: %#v", s)
	}
	if strings.Contains(u.modbusActivityText(), "raw secret") {
		t.Fatal("raw error leaked")
	}
	u.modbusStatsPanel()
	if !strings.Contains(u.modbusSession.statsView.GetText(false), "平均 3.000ms") {
		t.Fatal("latency not rendered")
	}
	v.values[1] = map[string]any{"u16": uint16(1)}
	v.pins[1] = true
	u.modbusSession.statsView.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'c', 0))
	if len(v.values) != 0 || u.modbusSession.stats.reads != 0 {
		t.Fatal("clear did not clear volatile session")
	}
	if !v.pins[1] {
		t.Fatal("clear removed saved annotation")
	}
	u.modbusOperation(engine.ModbusOperation{Success: true, Duration: time.Millisecond})
	u.modbusSessionStart(r)
	if u.modbusSession.stats.reads != 1 {
		t.Fatal("same target reset stats")
	}
	r.Endpoint = "mock://different"
	u.modbusSessionStart(r)
	if u.modbusSession.stats.reads != 0 {
		t.Fatal("target switch kept stats")
	}
}
func TestModbusActivityBoundedFollowWrapCopyExport(t *testing.T) {
	u, _, r := modbusTestView(t)
	r.Params["password"] = "private-token"
	r.Endpoint = "tcp://user:secret@host:502"
	u.modbusSessionStart(r)
	u.modbusSessionEnd(r, errors.New("secret body and token"))
	if text := u.modbusActivityText(); strings.Contains(text, "private-token") || strings.Contains(text, "secret") || strings.Contains(text, "user:") {
		t.Fatal("activity leaked credential/error")
	}
	for i := 0; i < 2200; i++ {
		u.modbusActivity("INFO", strings.Repeat("界", 512))
	}
	s := u.modbusSession
	if len(s.entries) > 1000 || s.bytes > 1<<20 || s.dropped == 0 {
		t.Fatal("activity unbounded")
	}
	u.modbusActivityPanel()
	view := s.logView
	u.App.ForceDraw()
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	u.modbusActivity("INFO", "new event")
	if s.follow {
		t.Fatal("live update stole historical scroll")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'w', 0))
	if !s.wrap {
		t.Fatal("wrap not toggled")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'f', 0))
	if !s.follow {
		t.Fatal("follow not restored")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlY, 0, 0))
	name, p := u.pages.GetFrontPage()
	if name != "clipboard" {
		t.Fatal("copy skipped review")
	}
	p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	u.modbusActivityExport()
	_, p = u.pages.GetFrontPage()
	form := p.(*tview.Form)
	path := t.TempDir() + "/activity.log"
	form.GetFormItem(0).(*tview.InputField).SetText(path)
	modbusTestClick(form, 0)
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "new event") {
		t.Fatal("export unavailable", err)
	}
	u.modbusActivityExport()
	_, p = u.pages.GetFrontPage()
	form = p.(*tview.Form)
	form.GetFormItem(0).(*tview.InputField).SetText(path)
	modbusTestClick(form, 0)
	after, _ := os.ReadFile(path)
	if string(content) != string(after) {
		t.Fatal("export overwrote file")
	}
}
func TestModbusSessionCloseAndCancelRemainSeparate(t *testing.T) {
	u, _, _ := modbusTestView(t)
	cancelled := 0
	u.cancel = func() { cancelled++ }
	u.running = true
	u.modbusStatsPanel()
	view := u.modbusSession.statsView
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if cancelled != 0 {
		t.Fatal("close cancelled communication")
	}
	u.modbusActivityPanel()
	view = u.modbusSession.logView
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyF8, 0, 0))
	if cancelled != 1 {
		t.Fatal("F8 did not cancel")
	}
	u.modbusSessionEnd(u.lastRequest, context.Canceled)
}

func TestModbusCancelBetweenSamplesCountedOnce(t *testing.T) {
	u, _, r := modbusTestView(t)
	u.modbusSessionStart(r)
	u.modbusSessionEnd(r, context.Canceled)
	u.modbusSessionEnd(r, context.Canceled)
	if u.modbusSession.stats.cancelled != 1 {
		t.Fatal("wait cancellation lost or duplicated")
	}
	u.modbusSessionStart(r)
	u.modbusOperation(engine.ModbusOperation{Cancelled: true, ErrorClass: "已取消"})
	u.modbusSessionEnd(r, context.Canceled)
	if u.modbusSession.stats.cancelled != 2 {
		t.Fatal("operation/end cancellation double counted")
	}
}
