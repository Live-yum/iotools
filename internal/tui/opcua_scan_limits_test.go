package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestUAScanLimitsUseDefaultsOnlyWhenAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   [4]int
	}{
		{"absent", nil, [4]int{0, 10000, 1000, 100}},
		{"minimum", map[string]any{"max_depth": 0, "max_nodes": 1, "max_references": 1, "read_batch_size": 1}, [4]int{0, 1, 1, 1}},
		{"maximum", map[string]any{"max_depth": 256, "max_nodes": 100000, "max_references": 100000, "read_batch_size": 1000}, [4]int{256, 100000, 100000, 1000}},
		{"exact-types", map[string]any{"max_depth": float64(3), "max_nodes": int64(400), "max_references": uint64(50), "read_batch_size": "20"}, [4]int{3, 400, 50, 20}},
		{"json-integer", map[string]any{"max_nodes": json.Number("456")}, [4]int{0, 456, 1000, 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := config.Request{Params: tc.params}
			before := copyRequest(r)
			values, err := uaScanLimitValues(r)
			if err != nil || values != tc.want {
				t.Fatalf("limits = %v, %v; want %v", values, err, tc.want)
			}
			if len(r.Params) != len(before.Params) {
				t.Fatal("limit defaults were written into the source request")
			}
			for key, value := range r.Params {
				if !reflect.DeepEqual(value, before.Params[key]) {
					t.Fatalf("source parameter %s changed", key)
				}
			}
		})
	}
}

func TestUAScanLimitsRejectMalformedAndOutOfRangeValues(t *testing.T) {
	for _, limit := range []struct {
		key      string
		min, max int
	}{
		{"max_depth", 0, 256}, {"max_nodes", 1, 100000},
		{"max_references", 1, 100000}, {"read_batch_size", 1, 1000},
	} {
		for _, tc := range []struct {
			name string
			raw  any
		}{
			{"fraction", 1.5}, {"fraction-string", "1.5"}, {"invalid-string", "bad"},
			{"empty-string", ""}, {"whitespace", " 1 "}, {"null", nil}, {"boolean", true},
			{"nan", math.NaN()}, {"infinity", math.Inf(1)}, {"overflow", uint64(math.MaxUint64)},
			{"object", map[string]any{"value": 1}}, {"array", []any{1}},
			{"below-minimum", limit.min - 1}, {"above-maximum", limit.max + 1},
		} {
			t.Run(limit.key+"/"+tc.name, func(t *testing.T) {
				r := config.Request{Params: map[string]any{limit.key: tc.raw}}
				_, err := uaScanLimitValues(r)
				if err == nil || !strings.Contains(err.Error(), limit.key) {
					t.Fatalf("accepted %s=%v (%T): %v", limit.key, tc.raw, tc.raw, err)
				}
			})
		}
	}
}

// Only simulated screens and injected runners are used. Invalid limits must
// never become a different effective scan, or interrupt the existing workspace.
func TestUAScanInvalidConfiguredLimitsRejectBeforeWorkspaceOrIO(t *testing.T) {
	for _, key := range []string{"max_depth", "max_nodes", "max_references", "read_batch_size"} {
		for _, raw := range []any{1.5, "bad", nil, true, -1, 100001} {
			t.Run(fmt.Sprintf("%s/%T=%v", key, raw, raw), func(t *testing.T) {
				u, screen := newTestUI(t)
				r := workspaceRequest()
				r.Params[key] = raw
				u.showUAWorkspaceFor(r)
				parent := u.uaWorkspace
				before := copyRequest(parent.base)
				calls, cancels := 0, 0
				parent.run = func(context.Context, config.Request, bool, engine.Emit) error {
					calls++
					return nil
				}
				parent.cancel = func() { cancels++ }
				parent.loading = true
				parent.generation = 7
				parent.key(0, tcell.NewEventKey(tcell.KeyRune, 'B', 0))
				if parent.scan != nil || calls != 0 || cancels != 0 || len(u.localCancels) != 0 {
					t.Fatal("invalid limits created a scan or changed execution")
				}
				if !parent.loading || parent.cancel == nil || parent.generation != 7 || !reflect.DeepEqual(parent.base, before) {
					t.Fatal("rejecting invalid limits changed the parent request or operation")
				}
				if !u.pages.HasPage("modal") || u.pages.HasPage("ua-scan") {
					t.Fatal("invalid scan limits were not visibly rejected")
				}
				u.App.ForceDraw()
				if !strings.Contains(strings.ReplaceAll(snapshot(screen), " ", ""), key) {
					t.Fatalf("error does not identify invalid parameter %s", key)
				}
			})
		}
	}
}

func TestUAScanValidConfiguredLimitsReachFormAndExplicitExecution(t *testing.T) {
	u, _ := newTestUI(t)
	r := workspaceRequest()
	r.Params["max_depth"], r.Params["max_nodes"] = float64(2), "345"
	r.Params["max_references"], r.Params["read_batch_size"] = int64(678), uint64(90)
	u.showUAWorkspaceFor(r)
	requests := make(chan config.Request, 1)
	u.uaWorkspace.run = func(_ context.Context, request config.Request, write bool, emit engine.Emit) error {
		if write {
			t.Error("scan enabled writes")
		}
		requests <- request
		emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"complete": true}})
		return nil
	}
	u.uaWorkspace.showScan()
	w := u.uaWorkspace.scan
	if w == nil || w.active || len(requests) != 0 {
		t.Fatal("valid configuration did not open an idle scan workspace")
	}
	startScanTestApp(t, u)
	u.App.QueueUpdateDraw(func() {
		w.scanForm()
		_, page := u.pages.GetFrontPage()
		form := page.(*tview.Form)
		for i, want := range []string{"2", "345", "678", "90"} {
			if actual := form.GetFormItem(i + 1).(*tview.InputField).GetText(); actual != want {
				t.Errorf("scan form field %d = %q, want %q", i+1, actual, want)
			}
		}
		form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	waitScanIdle(t, u, w)
	select {
	case request := <-requests:
		if request.Action != "scan" || request.Params["max_depth"] != 2 || request.Params["max_nodes"] != 345 || request.Params["max_references"] != 678 {
			t.Fatalf("explicit scan changed validated limits: %+v", request)
		}
	default:
		t.Fatal("explicit scan did not execute")
	}
	u.App.QueueUpdate(func() {
		if w.readBatch != 90 || !reflect.DeepEqual(w.base.Params, r.Params) {
			t.Error("scan lost the read batch or changed original parameter types")
		}
	})
}
