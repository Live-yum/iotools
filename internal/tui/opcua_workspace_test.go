package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
)

func workspaceRequest() config.Request {
	return config.Request{ID: "workspace", Protocol: "opcua", Action: "browse", Endpoint: "opc.tcp://127.0.0.1:4840", Params: map[string]any{"node_id": "i=85"}}
}
func workspaceRef(id uint32) *ua.ReferenceDescription {
	return &ua.ReferenceDescription{NodeID: ua.NewExpandedNodeID(ua.NewNumericNodeID(0, id), "", 0), DisplayName: &ua.LocalizedText{Text: "示例节点"}, NodeClass: ua.NodeClassVariable, IsForward: true}
}
func TestUAWorkspaceResponsiveAndNoImplicitIO(t *testing.T) {
	u, s := newTestUI(t)
	u.showUAWorkspaceFor(workspaceRequest())
	w := u.uaWorkspace
	if w == nil || w.loading || len(u.localCancels) != 0 {
		t.Fatal("opening workspace starts network work")
	}
	w.apply("browse", engine.Event{Kind: "reference", Data: workspaceRef(2258)})
	w.apply("attributes", engine.Event{Kind: "attribute", Data: map[string]any{"node_id": "i=85", "attribute": "DisplayName", "value": "对象", "status": "Good"}})
	w.apply("references", engine.Event{Kind: "reference", Data: workspaceRef(2258)})
	s.SetSize(120, 40)
	u.App.ForceDraw()
	if w.compact {
		t.Fatal("large screen lost four panes")
	}
	for _, p := range w.widgets {
		_, _, width, height := p.GetRect()
		if width < 40 || height < 10 {
			t.Fatalf("hidden pane %dx%d", width, height)
		}
	}
	s.SetSize(80, 24)
	w.focusPane(2)
	u.App.ForceDraw()
	if !w.compact || w.body.GetItemCount() != 1 || w.body.GetItem(0) != w.references {
		t.Fatal("small screen did not select references")
	}
	w.key(2, tcell.NewEventKey(tcell.KeyRune, '2', 0))
	u.App.ForceDraw()
	if w.body.GetItem(0) != w.attributes || w.attributes.GetRowCount() != 2 || len(w.browse.GetRoot().GetChildren()) != 1 {
		t.Fatal("resize/tab lost independent views")
	}
	w.key(1, tcell.NewEventKey(tcell.KeyTab, 0, 0))
	u.App.ForceDraw()
	if w.focus != 2 {
		t.Fatal("tab lost focus")
	}
	s.SetSize(120, 40)
	u.App.ForceDraw()
	if w.compact || w.references.GetRowCount() != 2 {
		t.Fatal("expansion lost references")
	}
	w.close()
	if u.uaWorkspace != nil || !w.closed {
		t.Fatal("close retained workspace")
	}
}
func TestUAWorkspaceRejectsForeignAndStaleNodeData(t *testing.T) {
	ref := workspaceRef(2258)
	ref.NodeID.ServerIndex = 1
	if _, ok := uaWorkspaceNodeID(ref); ok {
		t.Fatal("foreign server silently mapped to local node")
	}
	u, _ := newTestUI(t)
	u.showUAWorkspaceFor(workspaceRequest())
	w := u.uaWorkspace
	w.apply("attributes", engine.Event{Kind: "attribute", Data: map[string]any{"node_id": "i=999", "attribute": "Value"}})
	if w.attributes.GetRowCount() != 1 {
		t.Fatal("stale attribute accepted")
	}
	for i := 0; i < 50; i++ {
		w.apply("attributes", engine.Event{Kind: "attribute", Data: map[string]any{"node_id": "i=85", "attribute": "Value"}})
	}
	if w.attributes.GetRowCount() != 28 {
		t.Fatal("attributes unbounded")
	}
}
func TestUAWorkspaceGenerationAndCloseCancel(t *testing.T) {
	u, _ := newTestUI(t)
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	<-ready
	var calls atomic.Int32
	oldStarted := make(chan struct{})
	oldRelease := make(chan struct{})
	var w *uaWorkspace
	u.App.QueueUpdateDraw(func() {
		u.showUAWorkspaceFor(workspaceRequest())
		w = u.uaWorkspace
		w.run = func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
			calls.Add(1)
			if r.String("node_id", "") == "i=85" {
				close(oldStarted)
				<-oldRelease
				emit(engine.Event{Kind: "reference", Data: workspaceRef(999)})
				return ctx.Err()
			}
			if r.Action == "browse" {
				emit(engine.Event{Kind: "reference", Data: workspaceRef(2258)})
			}
			return nil
		}
		w.refresh()
		w.key(0, tcell.NewEventKey(tcell.KeyRune, 'r', 0))
	})
	<-oldStarted
	if calls.Load() != 1 {
		t.Fatal("repeated refresh started duplicate")
	}
	u.App.QueueUpdateDraw(func() { w.navigate("i=86", true) })
	close(oldRelease)
	deadline := time.Now().Add(3 * time.Second)
	finished := false
	for time.Now().Before(deadline) {
		u.App.QueueUpdateDraw(func() { finished = !w.loading && len(u.localCancels) == 0 })
		if finished {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	u.App.QueueUpdateDraw(func() {
		if !finished {
			t.Error("refresh workers not released")
		}
		children := w.browse.GetRoot().GetChildren()
		if len(children) != 1 {
			t.Errorf("stale browse applied: %d children", len(children))
		} else if id, _ := uaWorkspaceNodeID(children[0].GetReference()); id != "i=2258" {
			t.Errorf("stale node %s", id)
		}
		if !strings.Contains(w.status, "完成") {
			t.Errorf("status %s", w.status)
		}
		w.run = func(ctx context.Context, _ config.Request, _ bool, _ engine.Emit) error {
			<-ctx.Done()
			return ctx.Err()
		}
		w.refresh()
		w.close()
		u.quit()
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close/quit did not cancel workspace")
	}
}
