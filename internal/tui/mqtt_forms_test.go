package tui

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func mqttFormPreview() map[string]any {
	return map[string]any{"topics": []string{"tree/a", "tree/deep/b"}, "count": 2, "confirm_token": strings.Repeat("a", 64), "bounded_snapshot": true}
}

func mqttFormSource() config.Request {
	return config.Request{Protocol: "mqtt", Action: "preview-retained", Endpoint: "mqtt://127.0.0.1:1883", Timeout: "10s", Params: map[string]any{"topics": []string{"tree/#"}, "qos": 2, "scan_duration_ms": 1000, "max_topics": 100, "password": "secret-password"}}
}

func TestMQTTCleanupRequestPreservesExactSnapshotAndSource(t *testing.T) {
	source := mqttFormSource()
	data := mqttFormPreview()
	r, err := mqttCleanupRequest(source, data)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Mutates() || r.Action != "clean-retained" || r.Protocol != "mqtt" || r.Endpoint != source.Endpoint || r.Timeout != source.Timeout {
		t.Fatalf("lost cleanup target: %#v", r)
	}
	for _, key := range []string{"topics", "qos", "scan_duration_ms", "max_topics", "password"} {
		if !reflect.DeepEqual(r.Params[key], source.Params[key]) {
			t.Fatal("lost preview parameter", key)
		}
	}
	if !reflect.DeepEqual(r.Params["confirm_topics"], data["topics"]) || r.Params["confirm_token"] != data["confirm_token"] {
		t.Fatal("wrong confirmed scope")
	}
	data["topics"].([]string)[0] = "unconfirmed/other"
	source.Params["topics"].([]string)[0] = "different/#"
	if r.Params["confirm_topics"].([]string)[0] != "tree/a" || r.Params["topics"].([]string)[0] != "tree/#" {
		t.Fatal("cleanup shared mutable scope with source")
	}
	if _, changed := source.Params["confirm_token"]; changed || source.Action != "preview-retained" {
		t.Fatal("preview changed saved request")
	}
	for _, broken := range []map[string]any{{}, {"topics": []string{}, "confirm_token": "bad"}, {"topics": []any{"tree/a"}, "confirm_token": strings.Repeat("a", 64)}} {
		if _, err := mqttCleanupRequest(source, broken); err == nil {
			t.Fatal("invalid preview accepted", broken)
		}
	}
	if _, err := mqttCleanupRequest(r, mqttFormPreview()); err == nil {
		t.Fatal("cleanup's recheck created another write request")
	}
}

func mqttPreviewPanel(t *testing.T, u *UI) (*tview.Flex, *tview.TextView, *tview.Form) {
	t.Helper()
	name, page := u.pages.GetFrontPage()
	panel, ok := page.(*tview.Flex)
	if name != "mqtt-retained" || !ok {
		t.Fatalf("missing scrollable retained preview: %q %T", name, page)
	}
	return panel, panel.GetItem(0).(*tview.TextView), panel.GetItem(1).(*tview.Form)
}

func TestMQTTRetainedPanelCancelReadonlyAndReopen(t *testing.T) {
	u, _ := newTestUI(t)
	u.lastRequest = mqttFormSource()
	for _, readonly := range []bool{true, false} {
		u.readonly = readonly
		for _, escape := range []bool{true, false} {
			u.retainedPreview(mqttFormPreview())
			panel, _, buttons := mqttPreviewPanel(t, u)
			wantButtons := 2
			if readonly {
				wantButtons = 1
			}
			if buttons.GetButtonCount() != wantButtons {
				t.Fatal("read-only preview offered a write button")
			}
			if escape {
				panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
			} else {
				buttons.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) { u.App.SetFocus(p) })
			}
			if u.pages.HasPage("mqtt-retained") || u.pages.HasPage("derived-confirm") || u.running {
				t.Fatal("cancel did not remain read-only")
			}
		}
	}
}

func TestMQTTRetainedPanelRequiresSecondExplicitConfirmation(t *testing.T) {
	u, _ := newTestUI(t)
	u.readonly = false
	u.lastRequest = mqttFormSource()
	u.retainedPreview(mqttFormPreview())
	panel, view, buttons := mqttPreviewPanel(t, u)
	if !view.HasFocus() {
		t.Fatal("preview should initially focus scrollable list")
	}
	for i := 0; i < 2; i++ {
		panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	}
	if !buttons.GetButton(1).HasFocus() {
		t.Fatal("keyboard cannot reach confirmation")
	}
	buttons.GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) { u.App.SetFocus(p) })
	if u.pages.HasPage("mqtt-retained") || !u.pages.HasPage("derived-confirm") || u.running {
		t.Fatal("preview bypassed final write confirmation")
	}
	_, page := u.pages.GetFrontPage()
	confirmation := page.(*tview.Modal)
	confirmation.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if u.pages.HasPage("derived-confirm") || u.running {
		t.Fatal("final confirmation cancel started cleanup")
	}
	if u.lastRequest.Action != "preview-retained" {
		t.Fatal("cancel altered current request")
	}
}

func TestMQTTRetainedPanelDefersUntilSuccessfulCompletion(t *testing.T) {
	for _, success := range []bool{false, true} {
		u, _ := newTestUI(t)
		u.lastRequest = mqttFormSource()
		u.running = true
		u.inspector.add(engine.Event{Kind: "retained-preview", Data: mqttFormPreview()})
		if u.pages.HasPage("mqtt-retained") || u.mqttPreviewPending == nil {
			t.Fatal("running preview exposed premature confirmation")
		}
		u.running = false
		u.finishMQTTPreview(success)
		if u.pages.HasPage("mqtt-retained") != success || u.mqttPreviewPending != nil {
			t.Fatal("deferred preview did not match completion outcome")
		}
	}
	u, _ := newTestUI(t)
	u.lastRequest = mqttFormSource()
	u.lastRequest.Action = "clean-retained"
	u.inspector.add(engine.Event{Kind: "retained-preview", Data: mqttFormPreview()})
	if u.pages.HasPage("mqtt-retained") || u.mqttPreviewPending != nil {
		t.Fatal("cleanup recheck prompted for confirmation again")
	}
}

func TestMQTTRetainedPanelScrollResizeAndMarkup(t *testing.T) {
	u, screen := newTestUI(t)
	u.lastRequest = mqttFormSource()
	preview := mqttFormPreview()
	topics := []string{"[red]literal[-]/a"}
	for i := 0; i < 100; i++ {
		topics = append(topics, fmt.Sprintf("tree/%03d", i))
	}
	preview["topics"] = topics
	u.retainedPreview(preview)
	panel, view, _ := mqttPreviewPanel(t, u)
	u.App.ForceDraw()
	if !strings.Contains(snapshot(screen), "[red]literal[-]/a") {
		t.Fatal("topic markup hid part of approved scope")
	}
	view.ScrollToEnd()
	u.App.ForceDraw()
	if !strings.Contains(snapshot(screen), "tree/099") {
		t.Fatal("cannot inspect last confirmed topic")
	}
	screen.SetSize(60, 20)
	u.App.ForceDraw()
	if !strings.Contains(strings.ReplaceAll(snapshot(screen), " ", ""), "取消") {
		t.Fatalf("resize hid cancel action\n%s", snapshot(screen))
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
}

func TestMQTTCleanupRequestCopiesYAMLFilterList(t *testing.T) {
	source := mqttFormSource()
	source.Params["topics"] = []any{"tree/#"}
	request, err := mqttCleanupRequest(source, mqttFormPreview())
	if err != nil {
		t.Fatal(err)
	}
	source.Params["topics"].([]any)[0] = "different/#"
	if request.Params["topics"].([]any)[0] != "tree/#" {
		t.Fatal("YAML list was not copied")
	}
}

func TestMQTTFinalConfirmationIsCompactAndRechecksReadonly(t *testing.T) {
	u, screen := newTestUI(t)
	u.readonly = false
	u.lastRequest = mqttFormSource()
	preview := mqttFormPreview()
	topics := make([]string, 1000)
	for i := range topics {
		topics[i] = fmt.Sprintf("tree/%04d", i)
	}
	preview["topics"] = topics
	request, err := mqttCleanupRequest(u.lastRequest, preview)
	if err != nil {
		t.Fatal(err)
	}
	u.confirmDerived(request)
	name, page := u.pages.GetFrontPage()
	modal, ok := page.(*tview.Modal)
	if name != "derived-confirm" || !ok {
		t.Fatal("missing final gate")
	}
	screen.SetSize(60, 20)
	u.App.ForceDraw()
	text := strings.Join(strings.Fields(snapshot(screen)), "")
	for _, want := range []string{"1000", request.Endpoint, "确认执行"} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact confirmation omitted %q\n%s", want, snapshot(screen))
		}
	}
	tokenChars := 0
	for _, run := range regexp.MustCompile(`a{20,}`).FindAllString(snapshot(screen), -1) {
		tokenChars += len(run)
	}
	if tokenChars != 64 {
		t.Fatal("compact gate did not show full token", tokenChars)
	}
	if strings.Contains(text, "secret-password") || strings.Contains(text, "tree/0999") {
		t.Fatal("compact gate duplicated raw request")
	}
	u.readonly = true
	modal.SetFocus(1)
	u.App.SetFocus(modal)
	modal.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) { u.App.SetFocus(p) })
	if u.running || u.lastRequest.Action != "preview-retained" || u.pages.HasPage("derived-confirm") {
		t.Fatal("read-only change bypassed final gate")
	}
}
