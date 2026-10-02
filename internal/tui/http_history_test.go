package tui

import (
	"context"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestHistoryBrowserTypedDeletionAndScope(t *testing.T) {
	u, _ := newTestUI(t)
	u.readonly = true
	u.HTTPHistoryPath = filepath.Join(t.TempDir(), "history.db")
	h, e := engine.OpenHTTPHistory(u.HTTPHistoryPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = h.Add(context.Background(), engine.HTTPHistoryEntry{Collection: u.collection.SourcePath, Recipe: "get", Time: time.Now(), Status: 200, Body: []byte("test")}); e != nil {
		t.Fatal(e)
	}
	h.Close()
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	u.App.QueueUpdateDraw(func() { u.httpHistory() })
	var table *tview.Table
	var id int64
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		u.App.QueueUpdateDraw(func() {
			_, p := u.pages.GetFrontPage()
			if v, ok := p.(*tview.Table); ok && v.GetRowCount() == 2 {
				table = v
				id, _ = v.GetCell(1, 0).GetReference().(int64)
			}
		})
		if table != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if table == nil {
		u.App.QueueUpdateDraw(u.quit)
		t.Fatal("history list unavailable")
	}
	u.App.QueueUpdateDraw(func() {
		table.Select(1, 0)
		table.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'D', 0))
		if !u.pages.HasPage("modal") || u.pages.HasPage("history-delete") {
			t.Error("readonly opened deletion form")
		}
		u.pages.RemovePage("modal")
		u.App.SetFocus(table)
	})
	protectedRows, err := engine.ListHTTPHistory(context.Background(), u.HTTPHistoryPath, u.collection.SourcePath, "")
	if err != nil || len(protectedRows) != 1 {
		t.Fatal("readonly deletion changed history", protectedRows, err)
	}
	u.App.QueueUpdateDraw(func() { u.readonly = false })
	u.App.QueueUpdateDraw(func() {
		table.Select(1, 0)
		table.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'D', 0))
		_, p := u.pages.GetFrontPage()
		f := p.(*tview.Form)
		f.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		if !u.pages.HasPage("history-delete") {
			t.Error("untyped ID accepted")
		}
		f.GetFormItem(0).(*tview.InputField).SetText(strconv.FormatInt(id, 10))
		f.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	deleted := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, e := engine.ListHTTPHistory(context.Background(), u.HTTPHistoryPath, u.collection.SourcePath, "")
		if e == nil && len(rows) == 0 {
			deleted = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	u.App.QueueUpdateDraw(u.quit)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("history worker did not exit")
	}
	if !deleted {
		t.Fatal("confirmed history not deleted")
	}
}
