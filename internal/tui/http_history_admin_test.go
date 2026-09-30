package tui

import (
	"context"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHistoryAdminPreviewCancelReadonlyAndRepeat(t *testing.T) {
	u, screen := newTestUI(t)
	screen.SetSize(80, 24)
	u.HTTPHistoryPath = filepath.Join(t.TempDir(), "h.sqlite")
	h, e := engine.OpenHTTPHistory(u.HTTPHistoryPath)
	if e != nil {
		t.Fatal(e)
	}
	e = h.Add(context.Background(), engine.HTTPHistoryEntry{Collection: "first", Recipe: "r", Time: time.Now(), Status: 200})
	h.Close()
	if e != nil {
		t.Fatal(e)
	}
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	click := func(b *tview.Button) {
		b.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	}
	var form *tview.Form
	u.App.QueueUpdateDraw(func() {
		u.historyAdmin()
		_, p := u.pages.GetFrontPage()
		form = p.(*tview.Form)
		form.GetFormItem(0).(*tview.DropDown).SetCurrentOption(2)
		form.GetFormItem(5).(*tview.TextArea).SetText("UPDATE http_history SET status=status+1", true)
		u.readonly = true
		click(form.GetButton(0))
		if !strings.Contains(form.GetTitle(), "只读") {
			t.Error("readonly allowed preview/execution")
		}
		u.readonly = false
		click(form.GetButton(0))
		click(form.GetButton(0))
	})
	waitPage := func(name string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			found := false
			u.App.QueueUpdateDraw(func() { front, _ := u.pages.GetFrontPage(); found = front == name })
			if found {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("page %s unavailable", name)
	}
	waitPage("history-admin-preview")
	u.App.QueueUpdateDraw(func() {
		captureTUIScreen(t, u, screen, os.Getenv("IOTOOLS_SCREENSHOT_DIR"), "32-history-write-preview-small")
	})
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	})
	rows, e := engine.QueryHTTPHistory(context.Background(), u.HTTPHistoryPath, "SELECT status FROM http_history")
	if e != nil || rows[0]["status"] != int64(200) {
		t.Fatal("cancel mutated DB", rows, e)
	}
	u.App.QueueUpdateDraw(func() { click(form.GetButton(0)) })
	waitPage("history-admin-preview")
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		buttons := p.(*tview.Flex).GetItem(1).(*tview.Flex)
		yes := buttons.GetItem(1).(*tview.Button)
		click(yes)
		click(yes)
	})
	waitPage("history-admin-result")
	rows, e = engine.QueryHTTPHistory(context.Background(), u.HTTPHistoryPath, "SELECT status FROM http_history")
	if e != nil || rows[0]["status"] != int64(201) {
		t.Fatal("repeat mutated twice", rows, e)
	}
	u.App.QueueUpdateDraw(u.quit)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("history admin quit hung")
	}
}
