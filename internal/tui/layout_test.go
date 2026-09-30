package tui

import (
	"github.com/gdamore/tcell/v2"
	"strings"
	"testing"
)

func TestLogScrollAndLayoutResize(t *testing.T) {
	u, _ := newTestUI(t)
	u.App.ForceDraw()
	text := strings.Repeat("history\n", 100)
	u.result.SetText(text)
	u.result.ScrollTo(4, 0)
	u.updateResult(text + "new event")
	row, _ := u.result.GetScrollOffset()
	if row != 4 {
		t.Fatal("scrolled history jumped", row)
	}
	if !u.resizeLayout(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt)) || u.listWidth != 32 {
		t.Fatal("width resize failed")
	}
	if !u.resizeLayout(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModAlt)) || u.resultHeight < 5 {
		t.Fatal("height resize failed")
	}
	if u.resizeLayout(tcell.NewEventKey(tcell.KeyRight, 0, 0)) {
		t.Fatal("unmodified arrow intercepted")
	}
}
