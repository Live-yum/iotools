package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"testing"
)

func TestHTTPConsoleCancelAndNoImplicitNetwork(t *testing.T) {
	u, _ := newTestUI(t)
	u.HTTPHistoryPath = "history.sqlite"
	u.httpConsole()
	_, p := u.pages.GetFrontPage()
	form, ok := p.(*tview.Form)
	if !ok {
		t.Fatal("console missing")
	}
	if form.GetFormItem(1).(*tview.InputField).GetText() != "history.sqlite" {
		t.Fatal("history option lost")
	}
	if u.running {
		t.Fatal("opening console started execution")
	}
	form.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	if u.pages.HasPage("http-console") {
		t.Fatal("escape failed")
	}
	u.running = true
	u.httpConsole()
	if u.pages.HasPage("http-console") {
		t.Fatal("opened while request active")
	}
}
