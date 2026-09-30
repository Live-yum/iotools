package tui

import (
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPInsecureTLSRequiresVisiblePerRunChoice(t *testing.T) {
	u, _ := newTestUI(t)
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("ok")) }))
	defer server.Close()
	request := config.Request{ID: "tls-test", Protocol: "http", Action: "GET", Endpoint: server.URL, Params: map[string]any{"ignore_certificate_hosts": []any{"127.0.0.1"}}}
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	for iteration := 0; iteration < 2; iteration++ {
		u.App.QueueUpdateDraw(func() { u.start(request) })
		shown := false
		deadline := time.Now().Add(3 * time.Second)
		for !shown && time.Now().Before(deadline) {
			u.App.QueueUpdateDraw(func() { shown = u.pages.HasPage("http-insecure-confirm") })
			time.Sleep(time.Millisecond)
		}
		if !shown {
			u.App.QueueUpdateDraw(u.quit)
			t.Fatal("risk dialog unavailable")
		}
		if hits.Load() != 0 {
			t.Fatal("request sent before approval")
		}
		u.App.QueueUpdateDraw(func() {
			_, p := u.pages.GetFrontPage()
			m := p.(*tview.Flex)
			if iteration == 0 {
				m.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
			} else {
				button := m.GetItem(1).(*tview.Form).GetButton(1)
				button.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
			}
		})
		running := true
		deadline = time.Now().Add(3 * time.Second)
		for running && time.Now().Before(deadline) {
			u.App.QueueUpdateDraw(func() { running = u.running })
			time.Sleep(time.Millisecond)
		}
		if running {
			u.App.QueueUpdateDraw(u.quit)
			t.Fatal("request did not complete")
		}
	}
	u.App.QueueUpdateDraw(u.quit)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("quit blocked")
	}
	if hits.Load() != 1 {
		t.Fatal("expected only approved request", hits.Load())
	}
}
