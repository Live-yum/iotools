package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Queue real application events: direct calls to w.key miss both the global
// capture and mouse-focus routing through Pages/Flex to the focused primitive.
func TestUAScanApplicationF8StopsOnlyOwnedWork(t *testing.T) {
	for _, operation := range []string{"scan", "read", "subscribe", "record", "oneclick"} {
		for _, focus := range []string{"tree", "table", "header", "footer", "header-compact", "footer-compact", "detail", "status"} {
			t.Run(operation+"/"+focus, func(t *testing.T) {
				u, screen, w := openScanUI(t)
				if strings.HasSuffix(focus, "-compact") {
					screen.SetSize(80, 24)
				} else {
					screen.SetSize(150, 45)
				}
				fillScan(w)
				w.selected["ns=2;s=Temp"] = true
				var unrelatedRequest, unrelatedSubscription, calls atomic.Int32
				u.cancel = func() { unrelatedRequest.Add(1) }
				u.uaSubscriptions = map[string]*liveUASubscription{
					"other-endpoint": {Active: true, Cancel: func() { unrelatedSubscription.Add(1) }},
				}
				started, cancelled := make(chan struct{}), make(chan struct{})
				w.run = func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
					if calls.Add(1) != 1 {
						t.Error("F8 allowed a later engine operation to start")
						return context.Canceled
					}
					if write {
						t.Error("scan operation enabled writes")
					}
					if r.Action == "scan" {
						emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": oneClickTestFingerprint}})
						emit(scanNode("i=85", "", "Object", 0))
						emit(scanNode("ns=2;s=Temp", "i=85", "Variable", 1))
					} else {
						event := scanValue("ns=2;s=Temp", int64(123))
						if r.Action == "subscribe" {
							event.Kind = "notification"
						}
						emit(event)
					}
					close(started)
					<-ctx.Done()
					close(cancelled)
					return ctx.Err()
				}
				path := filepath.Join(t.TempDir(), "record.jsonl")
				directory := filepath.Join(t.TempDir(), "export")
				targets := []uaOneClickTarget{oneClickTestTarget(t, "first"), oneClickTestTarget(t, "not-started")}
				startScanTestApp(t, u)
				// Also finish the worker when a regression assertion fails, before the
				// application's cleanup stops servicing its final queued update.
				t.Cleanup(func() {
					u.App.QueueUpdate(func() { w.stop() })
					waitScanIdle(t, u, w)
				})
				u.App.QueueUpdateDraw(func() {
					switch operation {
					case "scan":
						w.startScan()
					case "read":
						w.readSelected()
					case "subscribe":
						w.subscribeSelected("", "")
					case "record":
						w.subscribeSelected(path, "jsonl")
					case "oneclick":
						w.startOneClick(directory, targets)
					}
				})
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("injected engine did not start")
				}
				var clickTarget tview.Primitive
				u.App.QueueUpdateDraw(func() {
					switch focus {
					case "tree":
						w.focusPane(0)
					case "table":
						w.focusPane(1)
					case "header", "header-compact":
						clickTarget = w.header
					case "footer", "footer-compact":
						clickTarget = w.footer
					case "detail":
						w.detail("ns=2;s=Temp")
					case "status":
						w.statusDetail()
					}
				})
				if clickTarget != nil {
					clickScanPrimitive(t, u, clickTarget)
				}
				u.App.QueueEvent(tcell.NewEventKey(tcell.KeyF8, 0, tcell.ModNone))
				select {
				case <-cancelled:
				case <-time.After(5 * time.Second):
					t.Fatalf("application F8 did not cancel %s with %s focus; unrelated request=%d subscription=%d", operation, focus, unrelatedRequest.Load(), unrelatedSubscription.Load())
				}
				waitScanIdle(t, u, w)
				if unrelatedRequest.Load() != 0 || unrelatedSubscription.Load() != 0 {
					t.Fatalf("scan F8 cancelled unrelated work: request=%d subscription=%d", unrelatedRequest.Load(), unrelatedSubscription.Load())
				}
				u.App.QueueUpdate(func() {
					if w.cancel != nil || w.active || w.stopping || len(u.localCancels) != 0 || calls.Load() != 1 {
						t.Error("F8 did not finish the single owned worker")
					}
					if !strings.Contains(w.status, "已停止") {
						t.Errorf("missing cancellation status: %s", w.status)
					}
					if w.nodes["ns=2;s=Temp"] == nil {
						t.Error("cancellation discarded discovered nodes")
					}
					if operation == "oneclick" && (w.oneClickReport == nil || !w.oneClickReport.Cancelled || !w.oneClickReport.Summary.Complete) {
						t.Error("one-click cancellation did not finish its summary")
					}
				})
				if operation == "record" {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					lines := strings.Split(strings.TrimSpace(string(data)), "\n")
					if len(lines) != 3 || !strings.Contains(lines[0], `"kind":"session_start"`) || !strings.Contains(lines[2], `"kind":"session_end"`) {
						t.Fatalf("recording was not flushed with its terminal record: %s", data)
					}
					for _, line := range lines {
						if !json.Valid([]byte(line)) {
							t.Fatal("recording contains an incomplete JSON line")
						}
					}
				}
				if operation == "oneclick" {
					if _, err := os.Stat(filepath.Join(directory, "001-first.json")); err != nil {
						t.Fatal("cancel discarded current target's partial export", err)
					}
					if _, err := os.Stat(filepath.Join(directory, "002-not-started.json")); !os.IsNotExist(err) {
						t.Fatal("cancel started a later target")
					}
				}
			})
		}
	}
}

func clickScanPrimitive(t *testing.T, u *UI, primitive tview.Primitive) {
	t.Helper()
	var x, y int
	u.App.QueueUpdate(func() {
		var width, height int
		x, y, width, height = primitive.GetRect()
		if width < 1 || height < 1 {
			t.Error("mouse target has no visible rectangle")
		}
	})
	u.App.QueueEvent(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	u.App.QueueEvent(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		focused := false
		u.App.QueueUpdate(func() { focused = u.App.GetFocus() == primitive && primitive.HasFocus() })
		if focused {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("real mouse events did not focus the requested scan text view")
}
