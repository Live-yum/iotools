package tui

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func mqttHistoryTestMessage(topic, text string, n int, retained bool) map[string]any {
	return map[string]any{"topic": topic, "payload": text, "payload_hex": hex.EncodeToString([]byte(text)), "bytes": len(text), "qos": byte(1), "retained": retained, "received_at": time.Date(2026, 9, 30, 12, 0, n, 0, time.UTC).Format(time.RFC3339Nano), "payload_format": "text"}
}
func mqttHistoryTestUI(t *testing.T) (*UI, *inspector) {
	t.Helper()
	u, _ := newTestUI(t)
	r := config.Request{ID: "history", Protocol: "mqtt", Action: "subscribe", Endpoint: "mqtt://127.0.0.1:1883"}
	u.lastRequest = r
	u.inspector.reset(r)
	return u, u.inspector
}
func TestMQTTHistoryPerTopicRetainsImmutableLatest(t *testing.T) {
	_, v := mqttHistoryTestUI(t)
	for i := 0; i < 150; i++ {
		v.mqttHistoryAdd(mqttHistoryTestMessage("a", fmt.Sprint(i), i, false))
	}
	history := v.mqttHistory.topics["a"]
	if len(history.entries) != 128 || history.entries[0].text != "22" || history.entries[127].text != "149" {
		t.Fatal("per-topic retention incorrect")
	}
	data := map[string]any{"temperature": json.Number("9007199254740993")}
	message := mqttHistoryTestMessage("b", "{}", 0, false)
	message["payload_json"] = data
	v.mqttHistoryAdd(message)
	data["temperature"] = "changed"
	entry := v.mqttHistory.topics["b"].entries[0]
	if !strings.Contains(string(entry.json), "9007199254740993") || strings.Contains(string(entry.json), "changed") {
		t.Fatal("history not exact immutable snapshot")
	}
}
func TestMQTTHistoryGlobalBudgetsAndReset(t *testing.T) {
	_, v := mqttHistoryTestUI(t)
	for i := 0; i < 3000; i++ {
		v.mqttHistoryAdd(mqttHistoryTestMessage(fmt.Sprintf("topic-%d", i%300), strings.Repeat("x", 20000), i, false))
	}
	h := v.mqttHistory
	if len(h.topics) > mqttHistoryTopicLimit || h.count > mqttHistoryEntryLimit || h.bytes > mqttHistoryByteLimit || h.evicted == 0 {
		t.Fatalf("unbounded history: %d topics %d entries %d bytes", len(h.topics), h.count, h.bytes)
	}
	count, cost := 0, 0
	for topic, history := range h.topics {
		cost += len(topic)
		for _, entry := range history.entries {
			count++
			cost += entry.cost
			if len(entry.binary) > 4096 || len(entry.text) > 8300 || len(entry.json) > mqttHistoryValueLimit {
				t.Fatal("individual entry unbounded")
			}
		}
	}
	if count != h.count || cost != h.bytes {
		t.Fatal("eviction accounting inconsistent")
	}
	v.reset(config.Request{Protocol: "modbus", Action: "read-holding"})
	if v.mqttHistory != nil {
		t.Fatal("MQTT history leaked into another protocol")
	}
}
func TestMQTTSelectorsValidateAndSelectWithoutFallback(t *testing.T) {
	valid := map[string]int{"$": 0, "$.sensor.temperature": 2, "$.values[0]": 2, "$['a.b'][2][\"x\"]": 3, "$['']": 1}
	for path, want := range valid {
		steps, err := mqttParseSelector(path)
		if err != nil || len(steps) != want {
			t.Fatalf("%s: %v", path, err)
		}
	}
	for _, path := range []string{"", "foo", "$..x", "$[*]", "$[?(@.x)]", "$[-1]", "$[999999999999999999999999]", "$.x)", "$['unterminated]", "$" + strings.Repeat(".x", 33)} {
		if _, err := mqttParseSelector(path); err == nil {
			t.Fatal("accepted selector", path)
		}
	}
	h := &mqttHistoryState{binaryIndex: -1}
	h.selectors, _ = mqttParseSelector("$.values[1]")
	entry := &mqttHistoryEntry{json: []byte(`{"values":[3,9007199254740993]}`)}
	value, ok := h.selectedValue(entry)
	if !ok || value != json.Number("9007199254740993") {
		t.Fatal("selector lost precision")
	}
	h.selectors, _ = mqttParseSelector("$.missing")
	if _, ok = h.selectedValue(entry); ok {
		t.Fatal("missing selector silently fell back to root")
	}
	h.binaryIndex = 1
	entry.binary = []byte{1, 255}
	value, ok = h.selectedValue(entry)
	if !ok || value != 255 {
		t.Fatal("binary byte selection failed")
	}
}
func TestMQTTGraphSourceSemanticsRetainedAndFinite(t *testing.T) {
	h := &mqttHistoryState{binaryIndex: -1}
	entries := []*mqttHistoryEntry{}
	for i, value := range []string{"100", "1.5 °C", "2.5 °C", "NaN", "Inf"} {
		entry := mqttHistorySnapshot(mqttHistoryTestMessage("a", value, i, i == 0), uint64(i))
		entries = append(entries, entry)
	}
	points := mqttGraphPoints(h, entries)
	if len(points) != 2 || points[0].value != 1.5 || points[1].value != 2.5 {
		t.Fatalf("graph included retained/nonfinite: %#v", points)
	}
	chart := mqttRenderGraph(h, entries)
	if !strings.Contains(chart, "有效点:2") || !strings.Contains(chart, "●") || !strings.Contains(chart, "UTC") {
		t.Fatal("numeric time graph missing", chart)
	}
	for _, tc := range []struct {
		v      any
		format string
		want   float64
		valid  bool
	}{{true, "json", 1, true}, {false, "json", 0, true}, {[]any{1, 2}, "json", 2, true}, {map[string]any{"x": 1}, "json", 0, false}, {map[string]any{"x": 1}, "messagepack", 1, true}, {map[string]any{"messagepack_type": "extension"}, "messagepack", 0, false}, {json.Number("18446744073709551615"), "json", float64(uint64(math.MaxUint64)), true}} {
		got, ok := mqttNumeric(tc.v, tc.format)
		if ok != tc.valid || (ok && got != tc.want) {
			t.Fatalf("numeric semantics %#v got %g %t", tc, got, ok)
		}
	}
	extreme := []*mqttHistoryEntry{{json: []byte(`-1.7976931348623157e308`), format: "json", received: time.Now()}, {json: []byte(`1.7976931348623157e308`), format: "json", received: time.Now().Add(time.Second)}}
	if chart := mqttRenderGraph(h, extreme); strings.Contains(chart, "NaN") {
		t.Fatal("finite extremes overflow graph normalization")
	}
}
func TestMQTTHistoryLivePanelSelectionAndCloseDoesNotCancel(t *testing.T) {
	u, v := mqttHistoryTestUI(t)
	topic := "sensor/[red]temperature"
	for i := 0; i < 3; i++ {
		v.add(engine.Event{Kind: "message", Data: mqttHistoryTestMessage(topic, fmt.Sprint(i), i, false)})
	}
	v.tree.SetCurrentNode(v.topics["/"+topic])
	v.tree.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	page, p := u.pages.GetFrontPage()
	if page != "mqtt-history" {
		t.Fatal("history shortcut did not open")
	}
	h := v.mqttHistory
	if h.table.GetRowCount() != 4 {
		t.Fatal("history rows missing")
	}
	panel := p.(*tview.Flex)
	cancelled := false
	u.cancel = func() { cancelled = true }
	u.running = true
	h.table.Select(1, 0)
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	v.mqttHistoryAdd(mqttHistoryTestMessage(topic, "3", 3, false))
	row, _ := h.table.GetSelection()
	if row != 1 || h.table.GetRowCount() != 5 {
		t.Fatal("live update stole older selection")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'f', 0))
	row, _ = h.table.GetSelection()
	if row != 4 {
		t.Fatal("follow latest did not resume")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	if !h.graph || !strings.Contains(h.chart.GetText(false), "有效点:4") {
		t.Fatal("graph toggle failed")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if cancelled {
		t.Fatal("closing view cancelled broker subscription")
	}
	if page, _ := u.pages.GetFrontPage(); page != "main" {
		t.Fatal("history close failed")
	}
	v.mqttHistoryOpen(topic, false)
	v.mqttHistory.panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyF8, 0, 0))
	if !cancelled {
		t.Fatal("F8 inaccessible inside history")
	}
}
func TestMQTTHistorySelectorCancelAndInvalidKeepsState(t *testing.T) {
	u, v := mqttHistoryTestUI(t)
	message := mqttHistoryTestMessage("json", "{}", 0, false)
	message["payload_json"] = map[string]any{"temperature": 3}
	v.mqttHistoryAdd(message)
	v.mqttHistoryOpen("json", true)
	v.mqttHistorySelectorForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(0).(*tview.InputField).SetText("$..*")
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if page, _ := u.pages.GetFrontPage(); page != "mqtt-history-selector" || v.mqttHistory.path != "$" {
		t.Fatal("invalid selector changed state")
	}
	form.GetFormItem(0).(*tview.InputField).SetText("$.temperature")
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if page, _ := u.pages.GetFrontPage(); page != "mqtt-history" || v.mqttHistory.path != "$.temperature" {
		t.Fatal("valid selector not applied")
	}
}

func TestMQTTHistoryUncacheOnlyOlderLocalEntry(t *testing.T) {
	u, v := mqttHistoryTestUI(t)
	for i := 0; i < 3; i++ {
		v.mqttHistoryAdd(mqttHistoryTestMessage("topic", fmt.Sprint(i), i, false))
	}
	v.mqttHistoryOpen("topic", false)
	h := v.mqttHistory
	history := h.topics["topic"]
	if h.uncache("topic", history.entries[2].seq) {
		t.Fatal("removed newest entry")
	}
	h.follow = false
	h.table.Select(1, 0)
	h.panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyDelete, 0, 0))
	if len(history.entries) != 2 || history.entries[0].text != "1" || h.count != 2 || h.removed != 1 {
		t.Fatal("uncache not local bounded removal")
	}
	if u.running {
		t.Fatal("local removal executed network task")
	}
	h.graph = true
	h.table.Select(1, 0)
	h.panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyDelete, 0, 0))
	if h.count != 2 {
		t.Fatal("graph removed nonvisible selected row")
	}
}

func TestMQTTTopicSearchExpandCollapseKeepsRawNames(t *testing.T) {
	u, v := mqttHistoryTestUI(t)
	for _, topic := range []string{"Factory/room/temp", "Factory/room/status", "other/[blue]value"} {
		v.add(engine.Event{Kind: "message", Data: mqttHistoryTestMessage(topic, "1", 1, false)})
	}
	matches := v.mqttSearchTopics("FACTORY/")
	if len(matches) != 2 || matches[0] != "Factory/room/status" {
		t.Fatal("case-insensitive topic search incorrect", matches)
	}
	v.mqttExpandAll(false)
	if v.topics["/Factory"].IsExpanded() {
		t.Fatal("collapse all failed")
	}
	v.mqttExpandAll(true)
	if !v.topics["/Factory"].IsExpanded() {
		t.Fatal("expand all failed")
	}
	v.mqttTopicSearch()
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	input := panel.GetItem(0).(*tview.InputField)
	list := panel.GetItem(1).(*tview.List)
	if list.GetItemCount() != 3 {
		t.Fatal("initial topic matches duplicated")
	}
	input.SetText("[blue]")
	if list.GetItemCount() != 1 {
		t.Fatal("topic markup affected search")
	}
	list.SetCurrentItem(0)
	list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if page, _ := u.pages.GetFrontPage(); page != "main" {
		t.Fatal("search select did not close")
	}
	m, ok := v.tree.GetCurrentNode().GetReference().(map[string]any)
	if !ok || m["topic"] != "other/[blue]value" {
		t.Fatal("selected topic raw identity altered")
	}
	if u.running {
		t.Fatal("topic search executed network request")
	}
}

func TestMQTTHistoryRateExcludesRetained(t *testing.T) {
	entries := []*mqttHistoryEntry{}
	for i := 0; i < 3; i++ {
		entries = append(entries, mqttHistorySnapshot(mqttHistoryTestMessage("a", "1", i, i == 0), uint64(i)))
	}
	rate, ok := mqttHistoryRate(entries)
	if !ok || rate != 1 {
		t.Fatalf("rate %g %t", rate, ok)
	}
}

func TestMQTTHistoryRejectsDeepStructuredPreview(t *testing.T) {
	var value any = json.Number("1")
	for i := 0; i < 40; i++ {
		value = []any{value}
	}
	message := mqttHistoryTestMessage("deep", "deep", 0, false)
	message["payload_json"] = value
	entry := mqttHistorySnapshot(message, 1)
	if len(entry.json) != 0 || !entry.truncated {
		t.Fatal("deep structure retained for synchronous graph parsing")
	}
}
