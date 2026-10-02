package tui

import (
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPLongConfirmationScrollAndSafeKeyboard(t *testing.T) {
	u, s := newTestUI(t)
	s.SetSize(80, 24)
	called, accepted := false, false
	text := "目标：https://[::1]/[red]?" + strings.Repeat("参数很长", 1000) + "\n最终风险说明"
	panel := u.httpConfirmation(" 测试确认 ", text, "批准", func(ok bool) { called = true; accepted = ok })
	u.pages.AddPage("test-http-confirm", panel, true, true)
	u.App.SetFocus(panel)
	u.App.ForceDraw()
	view := panel.GetItem(0).(*tview.TextView)
	if view.GetText(false) != text {
		t.Fatal("target text altered")
	}
	view.InputHandler()(tcell.NewEventKey(tcell.KeyPgDn, 0, 0), func(tview.Primitive) {})
	u.App.ForceDraw()
	row, _ := view.GetScrollOffset()
	if row == 0 {
		t.Fatal("long text did not scroll")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	form := panel.GetItem(1).(*tview.Form)
	_, button := form.GetFocusedItemIndex()
	if button != 0 || called {
		t.Fatal("first focused action is not Cancel", button)
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !called || accepted {
		t.Fatal("escape did not cancel")
	}
}

func TestFC23ReadbackSnapshotUsesHoldingScope(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Action = "read-write-registers"
	u.lastRequest = r
	v.reset(r)
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(42)}}})
	v.snapshot(false)
	_, page := u.pages.GetFrontPage()
	form := page.(*tview.Form)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	form.GetFormItem(0).(*tview.InputField).SetText(path)
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	snapshot, err := engine.LoadRegisterSnapshot(path)
	if err != nil || snapshot.Values[10] != 42 {
		t.Fatal(snapshot, err)
	}
}
