package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// These are deterministic simulated-screen and injected-engine tests, not a
// certification against a physical PLC, industrial network or production endpoint.
func scanNode(id, parent, class string, depth int) engine.Event {
	return engine.Event{Kind: "scan-node", Data: map[string]any{"node_id": id, "parent_id": parent, "display_name": "测试 " + id, "browse_name": id, "node_class": class, "depth": depth}}
}
func scanValue(id string, value any) engine.Event {
	return engine.Event{Time: time.Date(2026, 10, 13, 1, 2, 3, 0, time.UTC), Kind: "value", Data: map[string]any{"node_id": id, "value": value, "value_type": uint8(8), "value_type_name": "Int64", "value_is_array": false, "status": "Good", "source_timestamp": time.Date(2026, 10, 13, 1, 2, 1, 0, time.UTC), "server_timestamp": time.Date(2026, 10, 13, 1, 2, 2, 0, time.UTC)}}
}
func fillScan(w *uaScanWorkspace) {
	w.apply(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": strings.Repeat("a", 64)}})
	w.apply(scanNode("i=85", "", "Object", 0))
	w.apply(scanNode("ns=2;s=Temp", "i=85", "Variable", 1))
	w.apply(scanNode("ns=2;s=temp", "i=85", "Variable", 1))
	w.apply(scanNode("ns=2;s=Start", "i=85", "Method", 1))
}
func openScanUI(t *testing.T) (*UI, tcell.SimulationScreen, *uaScanWorkspace) {
	t.Helper()
	u, s := newTestUI(t)
	u.showUAWorkspaceFor(workspaceRequest())
	u.uaWorkspace.key(0, tcell.NewEventKey(tcell.KeyRune, 'B', 0))
	return u, s, u.uaWorkspace.scan
}
func TestUAScanSimulatedScreenNoIOAndSelection(t *testing.T) {
	u, s, w := openScanUI(t)
	if w == nil || w.active || len(u.localCancels) != 0 {
		t.Fatal("opening scanner unexpectedly starts work")
	}
	if !strings.Contains(u.uaWorkspace.footer.GetText(false), "B全树扫描") {
		t.Fatal("scan entry is undiscoverable")
	}
	fillScan(w)
	if len(w.nodes) != 4 || len(w.variables) != 2 {
		t.Fatalf("variable filtering incorrect: %+v", w.variables)
	}
	w.focusPane(0)
	w.tree.SetCurrentNode(w.nodes["i=85"].tree)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if len(w.selected) != 0 {
		t.Fatal("object selected as a variable")
	}
	w.focusPane(1)
	w.table.Select(1, 0)
	w.key(1, tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if !w.selected["ns=2;s=Temp"] || w.selected["ns=2;s=temp"] {
		t.Fatal("case-sensitive NodeIDs conflated")
	}
	w.key(1, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	if len(w.selected) != 2 {
		t.Fatal("select all missed discovered variables")
	}
	w.apply(scanNode("ns=2;s=Later", "i=85", "Variable", 1))
	if w.selected["ns=2;s=Later"] {
		t.Fatal("select discovered also selected future nodes")
	}
	w.apply(scanValue("ns=2;s=Temp", int64(9223372036854775807)))
	if w.nodes["ns=2;s=Temp"].row.Value != int64(9223372036854775807) || !w.nodes["ns=2;s=Temp"].row.HasValue {
		t.Fatal("read result lost type/value")
	}
	w.apply(scanValue("ns=2;s=Unknown", 1))
	if w.received != 1 {
		t.Fatal("foreign value accepted")
	}
	s.SetSize(150, 45)
	u.App.ForceDraw()
	if w.compact || w.body.GetItemCount() != 2 {
		t.Fatal("wide layout hides tree/table")
	}
	if !strings.Contains(snapshot(s), "CSV/JSON") {
		t.Fatal("export shortcut not rendered")
	}
	s.SetSize(80, 24)
	w.focusPane(1)
	u.App.ForceDraw()
	if !w.compact || w.body.GetItemCount() != 1 || w.body.GetItem(0) != w.table {
		t.Fatal("narrow layout does not show selected pane")
	}
	w.key(1, tcell.NewEventKey(tcell.KeyTab, 0, 0))
	u.App.ForceDraw()
	if w.body.GetItem(0) != w.tree {
		t.Fatal("tab does not reveal tree")
	}
	s.SetSize(150, 45)
	u.App.ForceDraw()
	if len(w.nodes) != 5 || len(w.selected) != 2 || w.table.GetRowCount() != 4 {
		t.Fatal("resize discarded state")
	}
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'n', 0))
	if len(w.selected) != 0 {
		t.Fatal("clear selection failed")
	}
	w.close()
	if u.uaWorkspace.scan != nil || !w.closed {
		t.Fatal("closing scan retains overlay")
	}
}
func TestUAScanExplicitScanAndFileForms(t *testing.T) {
	u, _, w := openScanUI(t)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'r', 0))
	name, primitive := u.pages.GetFrontPage()
	if name != "ua-scan-form" {
		t.Fatal("scan settings missing")
	}
	form := primitive.(*tview.Form)
	form.GetFormItem(0).(*tview.InputField).SetText("ns=bad;i=85")
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if w.active || len(u.localCancels) != 0 {
		t.Fatal("invalid settings triggered a connection")
	}
	form.GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'l', 0))
	_, primitive = u.pages.GetFrontPage()
	form = primitive.(*tview.Form)
	if form.GetFormItem(0).(*tview.InputField).GetText() != "" {
		t.Fatal("recording path was silently chosen")
	}
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if w.active || len(u.localCancels) != 0 {
		t.Fatal("empty recording path starts a connection")
	}
	w.parent.close()
	if !w.closed || u.uaWorkspace != nil {
		t.Fatal("parent close leaves scanner open")
	}
}

func TestUAScanDepthDefaultsUnlimitedAndAcceptsOptionalLimit(t *testing.T) {
	u, _, w := openScanUI(t)
	if w.maxDepth != 0 {
		t.Fatalf("scanner defaults to depth limit %d", w.maxDepth)
	}
	requests := make(chan config.Request, 2)
	w.run = func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		if write {
			return errors.New("unexpected writes")
		}
		requests <- r
		emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"nodes": 0, "variables": 0, "complete": true, "reason": ""}})
		return nil
	}
	startScanTestApp(t, u)
	for _, depth := range []string{"0", "32"} {
		u.App.QueueUpdateDraw(func() {
			w.scanForm()
			_, primitive := u.pages.GetFrontPage()
			form := primitive.(*tview.Form)
			field := form.GetFormItem(1).(*tview.InputField)
			if !strings.Contains(field.GetLabel(), "0不限深度") {
				t.Errorf("unlimited depth is not explained: %q", field.GetLabel())
			}
			if depth == "0" && field.GetText() != "0" {
				t.Errorf("form defaults to depth %q", field.GetText())
			}
			field.SetText(depth)
			form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		})
		waitScanIdle(t, u, w)
		select {
		case r := <-requests:
			want := 0
			if depth == "32" {
				want = 32
			}
			if r.Action != "scan" || r.Int("max_depth", -1) != want || r.Int("max_nodes", -1) != 10000 || r.Int("max_references", -1) != 1000 || r.Timeout == "" {
				t.Fatalf("unexpected scan limits: %+v", r)
			}
		default:
			t.Fatal("valid scan settings did not run")
		}
	}
}

func TestUAScanSelectionLimitsRejectBeforeNetwork(t *testing.T) {
	_, _, w := openScanUI(t)
	var calls atomic.Int32
	w.run = func(context.Context, config.Request, bool, engine.Emit) error { calls.Add(1); return nil }
	for i := 0; i < 10001; i++ {
		w.selected[time.Unix(int64(i), 0).String()] = true
	}
	w.readSelected()
	if calls.Load() != 0 || w.active || !strings.Contains(w.status, "10000") {
		t.Fatal("oversized read started or silently truncated")
	}
	w.subscribeSelected("", "")
	if calls.Load() != 0 || w.active || !strings.Contains(w.status, "1000") {
		t.Fatal("oversized subscription started or silently truncated")
	}
}
func startScanTestApp(t *testing.T, u *UI) chan struct{} {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan struct{})
	go func() {
		if err := u.Run(); err != nil {
			t.Errorf("test UI: %v", err)
		}
		close(done)
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("test UI did not start")
	}
	t.Cleanup(func() {
		u.App.Stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("test UI did not stop")
		}
	})
	return done
}
func waitScanIdle(t *testing.T, u *UI, w *uaScanWorkspace) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		idle := false
		u.App.QueueUpdateDraw(func() { idle = !w.active && len(u.localCancels) == 0 })
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scan work/cancellation did not finish")
}
func TestUAScanSharedReadAndSubscriptionLifecycle(t *testing.T) {
	u, _, w := openScanUI(t)
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	startScanTestApp(t, u)
	var calls atomic.Int32
	requests := make(chan config.Request, 4)
	u.App.QueueUpdateDraw(func() {
		w.run = func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
			calls.Add(1)
			requests <- r
			if write {
				return errors.New("unexpected writes")
			}
			ids, ok := r.Params["node_ids"].([]string)
			if !ok || len(ids) != 2 {
				return errors.New("missing shared node_ids")
			}
			for _, id := range ids {
				e := scanValue(id, int64(123))
				if r.Action == "subscribe" {
					e.Kind = "notification"
				}
				emit(e)
			}
			if r.Action == "subscribe" {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		w.readSelected()
		w.readSelected()
	})
	waitScanIdle(t, u, w)
	read := <-requests
	if read.Action != "read" || read.Int("read_batch_size", 0) != 100 || calls.Load() != 1 || read.String("expected_namespace_fingerprint", "") != strings.Repeat("a", 64) {
		t.Fatalf("batch read was not one shared call: %+v", read)
	}
	path := filepath.Join(t.TempDir(), "live.jsonl")
	u.App.QueueUpdateDraw(func() {
		if w.received != 2 {
			t.Errorf("read events=%d", w.received)
		}
		w.subscribeSelected(path, "jsonl")
		w.subscribeSelected(path, "jsonl")
	})
	sub := <-requests
	if sub.Action != "subscribe" || sub.Int("max_events", 0) != 100000 || sub.Bool("auto_reconnect") || sub.String("expected_namespace_fingerprint", "") != strings.Repeat("a", 64) {
		t.Fatalf("unexpected subscription: %+v", sub)
	}
	deadline := time.Now().Add(5 * time.Second)
	count := 0
	for time.Now().Before(deadline) {
		u.App.QueueUpdateDraw(func() { count = w.recorded })
		if count == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if count != 3 {
		t.Fatal("streaming records not reflected")
	}
	u.App.QueueUpdateDraw(func() {
		w.key(0, tcell.NewEventKey(tcell.KeyRune, 'n', 0))
		if len(w.selected) != 2 {
			t.Error("active subscription selection changed")
		}
		w.key(0, tcell.NewEventKey(tcell.KeyF8, 0, 0))
	})
	waitScanIdle(t, u, w)
	if calls.Load() != 2 {
		t.Fatalf("duplicate/multi-session invocation: %d", calls.Load())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 {
		t.Fatalf("stream file lines=%d", len(lines))
	}
	if !strings.Contains(lines[0], `"kind":"session_start"`) || !strings.Contains(lines[len(lines)-1], `"kind":"session_end"`) {
		t.Fatal("stream is missing lifecycle/gap boundary records")
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("invalid stream row: %s", line)
		}
	}
	u.App.QueueUpdateDraw(func() {
		if !strings.Contains(w.status, "已停止") {
			t.Errorf("stop status %s", w.status)
		}
	})
}
func TestUAScanFileFailureDoesNotConnect(t *testing.T) {
	u, _, w := openScanUI(t)
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	startScanTestApp(t, u)
	path := filepath.Join(t.TempDir(), "existing.csv")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	u.App.QueueUpdateDraw(func() {
		w.run = func(context.Context, config.Request, bool, engine.Emit) error { calls.Add(1); return nil }
		w.subscribeSelected(path, "csv")
	})
	waitScanIdle(t, u, w)
	if calls.Load() != 0 {
		t.Fatal("file create error still opened a network session")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "preserve" {
		t.Fatal("existing recording overwritten")
	}
	u.App.QueueUpdateDraw(func() {
		if !strings.Contains(w.status, "失败") {
			t.Errorf("missing visible file error: %s", w.status)
		}
	})
}
func TestUAScanCancelledGenerationAndQuit(t *testing.T) {
	u, _, old := openScanUI(t)
	done := startScanTestApp(t, u)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	u.App.QueueUpdateDraw(func() {
		old.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			calls.Add(1)
			close(started)
			<-release
			emit(scanNode("ns=2;s=Stale", "i=85", "Variable", 1))
			return ctx.Err()
		}
		old.startScan()
		old.startScan()
	})
	<-started
	var current *uaScanWorkspace
	u.App.QueueUpdateDraw(func() {
		old.close()
		u.uaWorkspace.showScan()
		current = u.uaWorkspace.scan
		current.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": strings.Repeat("a", 64)}})
			emit(scanNode("i=85", "", "Object", 0))
			emit(scanNode("ns=2;s=Fresh", "i=85", "Variable", 1))
			emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"nodes": 2, "variables": 1, "complete": true, "reason": ""}})
			return nil
		}
		current.startScan()
	})
	close(release)
	waitScanIdle(t, u, current)
	u.App.QueueUpdateDraw(func() {
		if calls.Load() != 1 || len(current.variables) != 1 || current.variables[0] != "ns=2;s=Fresh" || !current.complete {
			t.Errorf("stale generation/duplicate scan: %v", current.variables)
		}
		current.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
		current.run = func(ctx context.Context, _ config.Request, _ bool, _ engine.Emit) error {
			<-ctx.Done()
			return ctx.Err()
		}
		current.subscribeSelected("", "")
		u.quit()
	})
	// Quit can return only after the active shared session releases its cancellation registration.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("quit leaked scan session")
	}

}
func TestUAScanPartialSummaryAndConnectionFailure(t *testing.T) {
	u, _, w := openScanUI(t)
	startScanTestApp(t, u)
	u.App.QueueUpdateDraw(func() {
		w.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": strings.Repeat("a", 64)}})
			emit(scanNode("i=85", "", "Object", 0))
			emit(scanNode("ns=2;s=Kept", "i=85", "Variable", 1))
			emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"complete": false, "reason": "max_nodes reached", "nodes": 2, "variables": 1}})
			return errors.New("max_nodes reached")
		}
		w.startScan()
	})
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		if w.complete || len(w.variables) != 1 || !strings.Contains(w.reason, "max_nodes") {
			t.Errorf("partial scan hidden: %s", w.reason)
		}
		w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
		w.run = func(context.Context, config.Request, bool, engine.Emit) error {
			return errors.New("simulated server disconnect")
		}
		w.subscribeSelected("", "")
	})
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		if !strings.Contains(w.status, "disconnect") || w.active {
			t.Errorf("disconnect hidden: %s", w.status)
		}
	})
}

func TestUAScanNamespaceFingerprintRequiredAndReset(t *testing.T) {
	_, _, w := openScanUI(t)
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	w.namespaceFingerprint = ""
	w.readSelected()
	if w.active || !strings.Contains(w.status, "指纹") {
		t.Fatal("read without namespace binding allowed")
	}
	w.subscribeSelected("", "")
	if w.active || !strings.Contains(w.status, "指纹") {
		t.Fatal("subscription without namespace binding allowed")
	}
	w.apply(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": "not a hash"}})
	if w.namespaceFingerprint != "" {
		t.Fatal("invalid namespace fingerprint accepted")
	}
	w.apply(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": strings.Repeat("b", 64)}})
	if w.namespaceFingerprint == "" {
		t.Fatal("valid namespace fingerprint ignored")
	}
	w.reset()
	if w.namespaceFingerprint != "" {
		t.Fatal("rescan kept stale namespace binding")
	}
}
func TestUAScanMissingReadResultsDoNotReuseValues(t *testing.T) {
	u, _, w := openScanUI(t)
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	w.apply(scanValue("ns=2;s=Temp", 123))
	w.apply(scanValue("ns=2;s=temp", 456))
	startScanTestApp(t, u)
	u.App.QueueUpdateDraw(func() {
		w.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			emit(scanValue("ns=2;s=Temp", 789))
			return errors.New("simulated incomplete read")
		}
		w.readSelected()
	})
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		if n := w.nodes["ns=2;s=temp"]; n.row.HasValue || n.row.Value != nil || !strings.Contains(n.row.Status, "未返回") {
			t.Errorf("old good value masquerades as new: %+v", n.row)
		}
		if n := w.nodes["ns=2;s=Temp"]; !n.row.HasValue || n.row.Value != 789 {
			t.Errorf("new value missing: %+v", n.row)
		}
		w.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			emit(engine.Event{Kind: "value", Data: map[string]any{"node_id": "ns=2;s=Temp", "value": nil, "status": "Unavailable", "error": "simulated service failed", "source_timestamp": nil, "server_timestamp": nil}})
			return errors.New("simulated service failed")
		}
		w.readSelected()
	})
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		n := w.nodes["ns=2;s=Temp"]
		if n.row.HasValue || n.row.Error != "simulated service failed" || n.row.SourceTimestamp != nil {
			t.Errorf("unavailable result falsely marked value: %+v", n.row)
		}
	})
}
func TestUAScanRecordErrorJoinedWithCancelStaysVisible(t *testing.T) {
	u, _, w := openScanUI(t)
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	startScanTestApp(t, u)
	sink := &uaScanTestSink{}
	u.App.QueueUpdateDraw(func() {
		w.newRecorder = func(path, format, endpoint string) (*uaScanRecorder, error) {
			return newUAScanRecorderTo(path, format, endpoint, sink, 2, uaScanFileLimit)
		}
		w.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			for i := 0; i < 2; i++ {
				e := scanValue("ns=2;s=Temp", i)
				e.Kind = "notification"
				emit(e)
			}
			<-ctx.Done()
			return ctx.Err()
		}
		w.subscribeSelected("injected-limit.jsonl", "jsonl")
	})
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		if !strings.Contains(w.status, "record limit") || !strings.Contains(w.status, "失败") {
			t.Errorf("record error swallowed by cancellation: %s", w.status)
		}
		if w.received != 1 {
			t.Errorf("unrecorded event displayed as recorded: %d", w.received)
		}
	})
	if sink.closes != 1 {
		t.Errorf("record failure leaked sink: closes %d", sink.closes)
	}
	if uaScanOnlyCancelled(errors.Join(context.Canceled, errors.New("disk failure"))) {
		t.Fatal("mixed cancellation classified as safe stop")
	}
	if !uaScanOnlyCancelled(errors.Join(context.Canceled, context.Canceled)) {
		t.Fatal("pure cancellation classified as error")
	}
}
func TestUAScanNarrowErrorAndFullDetailsVisible(t *testing.T) {
	u, s, w := openScanUI(t)
	fillScan(w)
	w.reason = strings.Repeat("max_references at ns=2;s=LongNodeId; ", 20)
	w.setStatus("磁盘记录失败：simulated disk full")
	s.SetSize(80, 24)
	u.App.ForceDraw()
	if !strings.Contains(strings.ReplaceAll(snapshot(s), " ", ""), "磁盘记录失败") {
		t.Fatalf("long scan reason hides current error on 80x24: %s", snapshot(s))
	}
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'i', 0))
	name, view := u.pages.GetFrontPage()
	if name != "ua-scan-detail" || !strings.Contains(view.(*tview.TextView).GetText(false), w.reason) {
		t.Fatal("complete diagnostic unavailable")
	}
}
