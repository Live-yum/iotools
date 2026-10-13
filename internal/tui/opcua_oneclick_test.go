package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/rivo/tview"
)

func clickOneClickButton(form *tview.Form, index int) {
	form.GetButton(index).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
}

func TestUAOneClickRejectedScanLimitsDoNotDereferenceMissingScan(t *testing.T) {
	for _, key := range []string{"max_depth", "max_nodes", "max_references", "read_batch_size"} {
		t.Run(key, func(t *testing.T) {
			u, _ := newTestUI(t)
			r := oneClickTestRequest("invalid-limit")
			r.Params[key] = "invalid"
			u.collection.Requests = []config.Request{r}
			u.showUAOneClick()
			if u.uaWorkspace == nil || u.uaWorkspace.scan != nil || !u.pages.HasPage("modal") || u.pages.HasPage("ua-oneclick-form") || u.pages.HasPage("ua-scan") || u.running {
				t.Fatal("invalid scan limits created or dereferenced a scan workspace")
			}
		})
	}
}

func TestUAOneClickInvalidFirstTargetDoesNotBlockLaterCollectionSelection(t *testing.T) {
	u, _ := newTestUI(t)
	u.profile = "field"
	u.collection.Profiles["field"] = map[string]string{"root": "ns=2;s=${literal}", "password": "FAKE${literal}", "literal": "must-not-expand-twice"}
	bad, good := oneClickTestRequest("bad-first"), oneClickTestRequest("good-second")
	bad.Params["max_nodes"] = "invalid"
	good.Params["max_depth"] = 7
	good.Params["node_id"], good.Params["password"] = "${root}", "${password}"
	u.collection.Requests = []config.Request{bad, good}
	u.showUAOneClick()
	name, page := u.pages.GetFrontPage()
	if name != "ua-oneclick-form" || u.uaWorkspace == nil || u.uaWorkspace.scan == nil {
		t.Fatalf("invalid first target hid valid later services: page=%q", name)
	}
	w := u.uaWorkspace.scan
	if w.base.ID != "good-second" || w.maxDepth != 7 || page.(*tview.Form).GetFormItemCount() != 3 || u.running || w.active {
		t.Fatal("collection picker lost targets, used an invalid seed, or started work")
	}
	targets := w.oneClickTargets(true)
	if len(targets) != 2 || targets[0].Request.ID != "bad-first" || !strings.Contains(targets[0].Error, "max_nodes") || targets[1].Request.ID != "good-second" || targets[1].Error != "" {
		t.Fatalf("seed selection hid or sanitized an invalid target: %+v", targets)
	}
	if u.collection.Requests[0].Params["max_nodes"] != "invalid" {
		t.Fatal("opening the picker rewrote invalid source limits")
	}
	current := w.oneClickTargets(false)
	if w.rootNode != "ns=2;s=${literal}" || w.base.String("password", "") != "FAKE${literal}" || len(current) != 1 || current[0].Request.String("node_id", "") != "ns=2;s=${literal}" || current[0].Request.String("password", "") != "FAKE${literal}" {
		t.Fatal("selecting the workspace seed resolved literal template syntax twice")
	}
}

func TestUAOneClickMainShortcutProfilePreviewCancelNoIO(t *testing.T) {
	u, screen := newTestUI(t)
	u.profile = "field"
	u.collection = &config.Collection{Profiles: map[string]map[string]string{"field": {"host": "example.invalid", "root": "i=85", "depth": "7", "nodes": "90000", "refs": "1234"}}, Requests: []config.Request{
		{ID: "first", Protocol: "opcua", Action: "scan", Endpoint: "opc.tcp://${host}:4840", Timeout: "10m", Params: map[string]any{"node_id": "${root}", "max_depth": "${depth}", "max_nodes": "${nodes}", "max_references": "${refs}", "auth": "username", "username": "operator", "password": "test-secret"}},
		{ID: "same-endpoint-other-identity", Protocol: "opcua", Action: "scan", Endpoint: "opc.tcp://${host}:4840", Params: map[string]any{"auth": "anonymous"}},
		{ID: "not-a-scan", Protocol: "opcua", Action: "write", Endpoint: "opc.tcp://${host}:4840", Params: map[string]any{"value": 1}},
	}}
	u.App.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlE, 0, 0))
	name, page := u.pages.GetFrontPage()
	if name != "ua-oneclick-form" {
		t.Fatalf("shortcut did not open direct picker: %s", name)
	}
	w := u.uaWorkspace.scan
	var calls atomic.Int32
	w.run = func(context.Context, config.Request, bool, engine.Emit) error { calls.Add(1); return nil }
	form := page.(*tview.Form)
	if form.GetFormItemCount() != 3 {
		t.Fatalf("picker includes non-scan requests: %d", form.GetFormItemCount())
	}
	if w.rootNode != "i=85" || w.maxDepth != 7 || w.maxNodes != 90000 || w.maxReferences != 1234 {
		t.Fatalf("workspace did not resolve profile first: %s %d %d %d", w.rootNode, w.maxDepth, w.maxNodes, w.maxReferences)
	}
	current := w.oneClickTargets(false)
	if len(current) != 1 || current[0].Error != "" || current[0].Request.String("node_id", "") != "i=85" || current[0].Request.Int("max_depth", -1) != 7 {
		t.Fatalf("current target lost resolved limits: %+v", current)
	}
	targets := w.oneClickTargets(true)
	if len(targets) != 2 || targets[0].Request.String("password", "") != "test-secret" || targets[1].Request.String("auth", "") != "anonymous" {
		t.Fatal("same endpoint requests were merged or authentication changed")
	}
	form.GetFormItem(0).(*tview.InputField).SetText(filepath.Join(t.TempDir(), "new-export"))
	clickOneClickButton(form, 0)
	name, page = u.pages.GetFrontPage()
	if name != "ua-oneclick-confirm" || !w.oneClickPending {
		t.Fatal("confirmation page missing")
	}
	preview := page.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
	for _, want := range []string{"first", "same-endpoint-other-identity", "example.invalid", "90000", "1234", "10m", "非", "auth=username"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %q", want)
		}
	}
	if strings.Contains(preview, "test-secret") || strings.Contains(preview, "not-a-scan") {
		t.Fatal("preview leaks password or includes unrelated request")
	}
	for _, size := range [][2]int{{80, 24}, {160, 48}, {80, 24}} {
		screen.SetSize(size[0], size[1])
		u.App.ForceDraw()
	}
	page.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if calls.Load() != 0 || w.active || w.oneClickPending || len(u.localCancels) != 0 {
		t.Fatal("opening/cancelling preview performed work")
	}
	w.oneClickForm(true)
	w.close()
	for _, page := range []string{"ua-oneclick-form", "ua-oneclick-confirm", "ua-oneclick-result"} {
		if u.pages.HasPage(page) {
			t.Errorf("close left %s behind", page)
		}
	}
}

func TestUAOneClickUIRepeatedClicksCancelAndSummary(t *testing.T) {
	u, _, w := openScanUI(t)
	startScanTestApp(t, u)
	started := make(chan struct{})
	var calls atomic.Int32
	directory := filepath.Join(t.TempDir(), "new-export")
	targets := []uaOneClickTarget{oneClickTestTarget(t, "first"), oneClickTestTarget(t, "not-started")}
	u.App.QueueUpdateDraw(func() {
		w.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			calls.Add(1)
			emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": oneClickTestFingerprint}})
			emit(scanNode("i=85", "", "Object", 0))
			emit(scanNode("ns=2;s=V", "i=85", "Variable", 1))
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		w.startOneClick(directory, targets)
		w.startOneClick(directory, targets)
		w.key(0, tcell.NewEventKey(tcell.KeyRune, 'X', 0))
		if w.oneClickPending {
			t.Error("repeated click opened overlapping dialog")
		}
	})
	<-started
	u.App.QueueUpdateDraw(func() { w.stop(); w.stop() })
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		if calls.Load() != 1 || w.cancel != nil || w.oneClickReport == nil || !w.oneClickReport.Cancelled {
			t.Errorf("cancel/repeated-click lifecycle incomplete: calls=%d", calls.Load())
		}
		if w.oneClickReport == nil {
			return
		}
		if !w.oneClickReport.Summary.Complete || w.oneClickReport.Partial != 2 {
			t.Errorf("cancel summary missing: %+v", w.oneClickReport)
		}
		if name, _ := u.pages.GetFrontPage(); name != "ua-oneclick-result" {
			t.Error("result summary not shown")
		}
	})
	if _, err := os.Stat(filepath.Join(directory, "001-first.json")); err != nil {
		t.Fatal("cancel discarded current discovered variables", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "002-not-started.json")); !os.IsNotExist(err) {
		t.Fatal("cancel started later request")
	}
}

func TestUAOneClickCancelledSummaryFailureRemainsVisible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	directory := filepath.Join(t.TempDir(), "export")
	report, err := runUAOneClick(ctx, directory, []uaOneClickTarget{oneClickTestTarget(t, "cancel")}, func(_ context.Context, _ config.Request, _ bool, emit engine.Emit) error {
		oneClickTestScan(emit, true, "", "ns=2;s=V")
		if err := os.WriteFile(filepath.Join(directory, "summary.json"), []byte("existing"), 0600); err != nil {
			t.Fatal(err)
		}
		cancel()
		return context.Canceled
	}, nil, nil)
	if err == nil || !errors.Is(err, context.Canceled) || uaScanOnlyCancelled(err) || !report.Cancelled || report.Summary.Complete || report.Summary.Error == "" {
		t.Fatalf("summary I/O failure hidden by cancellation: %+v %v", report, err)
	}
	status := uaOneClickStatus(report, err)
	if !strings.Contains(status, "导出错误") || !strings.Contains(status, "已停止") || !strings.Contains(status, "summary.json") {
		t.Fatal(status)
	}
	content, err := os.ReadFile(filepath.Join(directory, "summary.json"))
	if err != nil || string(content) != "existing" {
		t.Fatal("summary file overwritten")
	}
	u, _, w := openScanUI(t)
	w.oneClickReport, w.status, w.mode = &report, status, "一键导出"
	w.oneClickResult()
	_, page := u.pages.GetFrontPage()
	text := page.(*tview.TextView).GetText(false)
	if !strings.Contains(text, "汇总未完整写入") || strings.Contains(text, "汇总已写入") {
		t.Fatal("result page claims summary success", text)
	}
}

func TestUAScanValuePresenceDistinguishesBadAndNull(t *testing.T) {
	for _, tc := range []struct {
		name string
		data map[string]any
		want bool
	}{
		{"bad-node-no-variant", map[string]any{"status": ua.StatusBadNodeIDUnknown.Error(), "value": nil, "value_type": nil}, false},
		{"denied-no-variant", map[string]any{"status": ua.StatusBadUserAccessDenied.Error(), "has_value": false, "value": nil}, false},
		{"valid-null-explicit", map[string]any{"status": ua.StatusOK.Error(), "has_value": true, "value": nil, "value_type": ua.TypeIDNull}, true},
		{"valid-null-compatibility", map[string]any{"status": ua.StatusOK.Error(), "value": nil, "value_type": ua.TypeIDNull}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := uaScanValueRow(uaScanRow{}, engine.Event{Kind: "value", Data: tc.data})
			if row.HasValue != tc.want {
				t.Fatalf("variant presence=%t want %t: %+v", row.HasValue, tc.want, row)
			}
		})
	}
}

func TestUAOneClickCancelledSummaryWriteFailuresRemainVisible(t *testing.T) {
	for _, tc := range []struct {
		name string
		sink *uaScanTestSink
	}{
		{"write", &uaScanTestSink{writeErr: errors.New("summary disk failure")}},
		{"sync", &uaScanTestSink{syncErr: errors.New("summary sync failure")}},
		{"close", &uaScanTestSink{closeErr: errors.New("summary close failure")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := uaOneClickReport{Cancelled: true, Summary: uaOneClickFileResult{Path: "summary.json"}}
			err := errors.Join(uaOneClickWriteSummary(&report, []byte(`{"cancelled":true}`), tc.sink), context.Canceled)
			if uaScanOnlyCancelled(err) || report.Summary.Complete || report.Summary.Error == "" {
				t.Fatal("summary failure lost", report, err)
			}
			if status := uaOneClickStatus(report, err); !strings.Contains(status, "导出错误") || !strings.Contains(status, "已停止") || !strings.Contains(status, "summary") {
				t.Fatal(status)
			}
		})
	}
}
