package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
)

func TestHelpBuildIdentityAndActionMenuReadOnlyGate(t *testing.T) {
	u, v, _ := modbusTestView(t)
	u.BuildVersion = "verified-test-sha"
	u.readonly = true
	u.showHelp()
	_, page := u.pages.GetFrontPage()
	view := page.(*tview.TextView)
	if !strings.Contains(view.GetText(false), "verified-test-sha") || !strings.Contains(view.GetText(false), "Apache-2.0") {
		t.Fatal("help lacks build identity")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'M', 0))
	name, page := u.pages.GetFrontPage()
	if name != "modbus-action-menu" {
		t.Fatal(name)
	}
	list := page.(*tview.List)
	if list.GetItemCount() != len(modbusActionLabels) {
		t.Fatal("action menu incomplete")
	}
	for i := 0; i < list.GetItemCount(); i++ {
		text, _ := list.GetItemText(i)
		if text == modbusActionLabels["write"] {
			list.SetCurrentItem(i)
			list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
			break
		}
	}
	name, _ = u.pages.GetFrontPage()
	if u.running || name == "derived-confirm" || name == "modbus-write" {
		t.Fatal("palette bypassed readonly")
	}
	_ = v
}
