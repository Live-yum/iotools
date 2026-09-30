package tui

import (
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPOverridesStrictInput(t *testing.T) {
	for _, text := range []string{`{"unknown":true}`, `{} {}`, `{"Body":3}`, strings.Repeat(" ", 65537)} {
		if _, err := parseHTTPOverrides(text); err == nil {
			t.Fatalf("accepted %q", text[:min(len(text), 50)])
		}
	}
	value, err := parseHTTPOverrides(`{"Headers":["Authorization=private"],"Body":"hello"}`)
	if err != nil || len(value.Headers) != 1 || *value.Body != "hello" {
		t.Fatal(value, err)
	}
}
func TestHTTPOverridePreviewCancelKeepsCollection(t *testing.T) {
	u, _ := newTestUI(t)
	r := config.Request{ID: "once", Protocol: "http", Action: "POST", Endpoint: "http://127.0.0.1:1", Params: map[string]any{"body": "original"}}
	u.collection.Requests = []config.Request{r}
	u.selected = 0
	u.httpOverrideForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(1).(*tview.TextArea).SetText(`{"Headers":["Authorization=private"],"Body":"new"}`, false)
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	name, p := u.pages.GetFrontPage()
	if name != "http-override-preview" {
		t.Fatal(name)
	}
	flex := p.(*tview.Flex)
	view := flex.GetItem(0).(*tview.TextView)
	if strings.Contains(view.GetText(false), "private") {
		t.Fatal("credential exposed")
	}
	flex.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if u.collection.Requests[0].Params["body"] != "original" || u.running {
		t.Fatal("cancel changed source or sent request")
	}
}
func TestHTTPOverrideReadOnlyGate(t *testing.T) {
	u, _ := newTestUI(t)
	u.readonly = true
	r := config.Request{ID: "once", Protocol: "http", Action: "POST", Endpoint: "http://127.0.0.1:1"}
	c := &config.Collection{Requests: []config.Request{r}}
	u.httpOverridePreview(c, r, "")
	_, p := u.pages.GetFrontPage()
	buttons := p.(*tview.Flex).GetItem(1).(*tview.Form)
	buttons.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	if u.running {
		t.Fatal("readonly request started")
	}
}

func TestHTTPOverrideRunsOnceWithoutSaving(t *testing.T) {
	u, _ := newTestUI(t)
	var hits atomic.Int32
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		seen <- r.Header.Get("X-Temporary")
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	r := config.Request{ID: "once", Protocol: "http", Action: "GET", Endpoint: server.URL, Params: map[string]any{"headers": map[string]any{"X-Temporary": "old"}}}
	u.collection.Requests = []config.Request{r}
	u.selected = 0
	c, request, profile, err := engine.ApplyHTTPOverrides(u.collection, r, "", engine.HTTPOverrides{Headers: []string{"X-Temporary=new"}})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	u.App.QueueUpdateDraw(func() {
		u.httpOverridePreview(c, request, profile)
		_, p := u.pages.GetFrontPage()
		b := p.(*tview.Flex).GetItem(1).(*tview.Form).GetButton(0)
		for i := 0; i < 2; i++ {
			b.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		}
	})
	select {
	case value := <-seen:
		if value != "new" {
			t.Error(value)
		}
	case <-time.After(3 * time.Second):
		t.Error("request not received")
	}
	deadline := time.Now().Add(3 * time.Second)
	running := true
	for running && time.Now().Before(deadline) {
		u.App.QueueUpdateDraw(func() { running = u.running })
		time.Sleep(time.Millisecond)
	}
	u.App.QueueUpdateDraw(func() {
		if u.collection.Requests[0].Params["headers"].(map[string]any)["X-Temporary"] != "old" {
			t.Error("source changed")
		}
		u.quit()
	})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("UI did not exit")
	}
	if hits.Load() != 1 {
		t.Fatal("duplicate run", hits.Load())
	}
}
