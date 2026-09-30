package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/twmb/franz-go/pkg/kadm"
)

func kafkaTestRequest(u *UI, action string) {
	r := config.Request{ID: "kafka-test", Protocol: "kafka", Action: action, Endpoint: "127.0.0.1:9092", Params: map[string]any{"subject": "temperature-value", "topic": "events"}}
	u.lastRequest = r
	u.inspector.reset(r)
}
func kafkaTestPress(form *tview.Form, index int) {
	form.GetButton(index).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
}
func TestKafkaGroupMembersAndLagDedicatedTables(t *testing.T) {
	u, s := newTestUI(t)
	kafkaTestRequest(u, "groups")
	v := u.inspector
	v.add(engine.Event{Kind: "groups", Data: kadm.ListedGroups{"events-workers": {Group: "events-workers", State: "Stable", ProtocolType: "consumer", Coordinator: 2}}})
	if v.table.GetCell(1, 0).GetReference().(kafkaItem).name != "events-workers" {
		t.Fatal("raw group identity lost")
	}
	kafkaTestRequest(u, "group")
	v.add(engine.Event{Kind: "group", Data: kadm.DescribedGroups{"events-workers": {Group: "events-workers", State: "Stable", Members: []kadm.DescribedGroupMember{{MemberID: "member-1", ClientID: "worker", ClientHost: "127.0.0.1"}}}}})
	if v.table.GetCell(1, 3).Text != "worker" {
		t.Fatal("member client not shown")
	}
	kafkaTestRequest(u, "lag")
	v.add(engine.Event{Kind: "lag", Data: kadm.DescribedGroupLags{"events-workers": {Group: "events-workers", Lag: kadm.GroupLag{"events": {0: {Topic: "events", Partition: 0, Commit: kadm.Offset{At: 3}, Start: kadm.ListedOffset{Offset: 0}, End: kadm.ListedOffset{Offset: 12}, Lag: 9}}}}}})
	if v.table.GetCell(1, 6).Text != "9" {
		t.Fatal("lag lost")
	}
	u.App.ForceDraw()
	if !strings.Contains(snapshot(s), "events-workers") {
		t.Fatal("Kafka table did not render")
	}
}
func TestKafkaSchemaVersionsAndDetails(t *testing.T) {
	u, _ := newTestUI(t)
	kafkaTestRequest(u, "schemas")
	v := u.inspector
	v.add(engine.Event{Kind: "response", Data: map[string]any{"status": 200, "body": []any{"temperature-value"}}})
	if v.table.GetCell(1, 0).GetReference().(kafkaItem).kind != "subject" {
		t.Fatal("subject not navigable")
	}
	kafkaTestRequest(u, "schema-versions")
	v.add(engine.Event{Kind: "response", Data: map[string]any{"status": 200, "body": []any{json.Number("1"), json.Number("2")}}})
	if v.table.GetRowCount() != 3 || v.table.GetCell(2, 0).GetReference().(kafkaItem).data != "2" {
		t.Fatal("schema versions not navigable")
	}
	kafkaTestRequest(u, "schema")
	schema := `{"type":"record","name":"Reading","fields":[{"name":"value","type":"long"}]}`
	v.add(engine.Event{Kind: "response", Data: map[string]any{"status": 200, "body": map[string]any{"subject": "temperature-value", "id": 3, "version": 2, "schema": schema}}})
	item := v.table.GetCell(1, 0).GetReference().(kafkaItem)
	if item.version != "2" {
		t.Fatal("exact schema version lost")
	}
	v.kafkaSelect(1, 0)
	_, p := u.pages.GetFrontPage()
	view := p.(*tview.TextView)
	if !strings.Contains(view.GetText(false), "Reading") {
		t.Fatal("schema detail missing")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if page, _ := u.pages.GetFrontPage(); page != "main" {
		t.Fatal("schema close failed")
	}
}
func TestKafkaConnectDedicatedStatusAndConfig(t *testing.T) {
	u, _ := newTestUI(t)
	kafkaTestRequest(u, "connectors")
	v := u.inspector
	cfg := map[string]any{"connector.class": "FileStreamSink", "topics": "events", "tasks.max": "2"}
	v.add(engine.Event{Kind: "response", Data: map[string]any{"status": 200, "body": map[string]any{"sink-a": map[string]any{"info": map[string]any{"config": cfg}, "status": map[string]any{"connector": map[string]any{"state": "RUNNING", "worker_id": "local:8083"}, "tasks": []any{map[string]any{"id": 0}}, "type": "sink"}}}}})
	if v.table.GetCell(1, 1).Text != "RUNNING" {
		t.Fatal("connector status missing")
	}
	item := v.table.GetCell(1, 0).GetReference().(kafkaItem)
	if item.data.(map[string]any)["topics"] != "events" {
		t.Fatal("connector config lost")
	}
	kafkaTestRequest(u, "connector")
	v.kafka.request.Params["connector"] = "sink-a"
	v.add(engine.Event{Kind: "response", Data: map[string]any{"status": 200, "body": map[string]any{"connector": map[string]any{"state": "RUNNING"}, "tasks": []any{map[string]any{"id": 0, "state": "FAILED", "trace": "task failed"}}}}})
	if v.table.GetCell(2, 2).Text != "FAILED" || v.table.GetCell(2, 4).Text != "task failed" {
		t.Fatal("task diagnostic missing")
	}
}
func TestKafkaRecordBoundDetailAndNext(t *testing.T) {
	u, _ := newTestUI(t)
	kafkaTestRequest(u, "consume")
	v := u.inspector
	for i := 0; i < 150; i++ {
		v.add(engine.Event{Kind: "record", Data: map[string]any{"topic": "events", "partition": 0, "offset": i, "value": fmt.Sprintf("value-%d", i), "key": "sensor"}})
	}
	if len(v.kafka.rows) != kafkaMaxRecords || v.table.GetRowCount() != kafkaMaxRecords+1 {
		t.Fatal("record view unbounded")
	}
	v.kafkaSelect(1, 0)
	_, p := u.pages.GetFrontPage()
	view := p.(*tview.TextView)
	if !strings.Contains(view.GetText(false), "value-22") {
		t.Fatal("oldest bounded record wrong")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlN, 0, 0))
	_, p = u.pages.GetFrontPage()
	if !strings.Contains(p.(*tview.TextView).GetText(false), "value-23") {
		t.Fatal("next record navigation failed")
	}
}
func TestKafkaFilterNumericSortAndCap(t *testing.T) {
	u, _ := newTestUI(t)
	kafkaTestRequest(u, "topics")
	v := u.inspector
	v.kafkaTable("test", "name", "partitions")
	v.kafkaRow("a", "a", 10)
	v.kafkaRow("b", "b", 2)
	v.kafka.sortColumn = 1
	v.kafkaRender()
	if v.table.GetCell(1, 0).Text != "b" {
		t.Fatal("numeric sort is lexical")
	}
	v.kafka.filter = "a"
	v.kafkaRender()
	if v.table.GetRowCount() != 2 || v.table.GetCell(1, 0).Text != "a" {
		t.Fatal("filter failed")
	}
	for i := 0; i < 1500; i++ {
		v.kafkaRow(nil, i)
	}
	if len(v.kafka.rows) != kafkaMaxRows {
		t.Fatal("table rows unbounded")
	}
}
func TestKafkaFormsValidateCancelAndKeepConfirmFocus(t *testing.T) {
	u, _ := newTestUI(t)
	u.readonly = false
	kafkaTestRequest(u, "topics")
	original := string(u.raw)
	u.kafkaTopicForm("create-topic", "")
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	kafkaTestPress(form, 0)
	if page, _ := u.pages.GetFrontPage(); page != "kafka-topic" || u.running {
		t.Fatal("invalid form ran")
	}
	form.GetFormItem(0).(*tview.InputField).SetText("new-events")
	form.GetFormItem(1).(*tview.InputField).SetText("2147483648")
	kafkaTestPress(form, 0)
	if page, _ := u.pages.GetFrontPage(); page != "kafka-topic" {
		t.Fatal("overflow accepted")
	}
	form.GetFormItem(1).(*tview.InputField).SetText("2")
	kafkaTestPress(form, 0)
	page, p := u.pages.GetFrontPage()
	if page != "derived-confirm" || u.running {
		t.Fatal("write bypassed confirmation")
	}
	if u.App.GetFocus() == u.inspector.table {
		t.Fatal("form stole confirmation focus")
	}
	p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if page, _ := u.pages.GetFrontPage(); page != "main" || string(u.raw) != original {
		t.Fatal("cancel changed source")
	}
	u.readonly = true
	u.kafkaTopicForm("create-topic", "blocked")
	if u.running {
		t.Fatal("readonly write started")
	}
	if page, _ := u.pages.GetFrontPage(); page != "modal" {
		t.Fatal("readonly form not blocked")
	}
}
func TestKafkaPurgeRequiresExactSubject(t *testing.T) {
	u, _ := newTestUI(t)
	u.readonly = false
	kafkaTestRequest(u, "schemas")
	u.kafkaMutate("purge-subject", map[string]any{"subject": "temperature-value"})
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(0).(*tview.InputField).SetText("temperature")
	kafkaTestPress(form, 0)
	if page, _ := u.pages.GetFrontPage(); page != "kafka-purge" {
		t.Fatal("wrong subject accepted")
	}
	form.GetFormItem(0).(*tview.InputField).SetText("temperature-value")
	kafkaTestPress(form, 0)
	if page, _ := u.pages.GetFrontPage(); page != "derived-confirm" || u.running {
		t.Fatal("typed match skipped action confirmation")
	}
}
func TestKafkaRequestCopiesWithoutStaleBodiesOrSelectors(t *testing.T) {
	source := config.Request{Protocol: "kafka", Params: map[string]any{"json": map[string]any{"old": "body"}, "query": map[string]any{"x": "1"}, "consume_partitions": []any{2}, "password": "secret"}}
	r := kafkaRequest(source, "consume", map[string]any{"consume_partitions": nil, "topic": "new"})
	if _, ok := r.Params["json"]; ok {
		t.Fatal("stale HTTP body leaked")
	}
	if _, ok := r.Params["consume_partitions"]; ok {
		t.Fatal("empty partition did not restore all")
	}
	if source.Params["json"] == nil || source.Params["topic"] != nil {
		t.Fatal("source mutated")
	}
	if r.Params["password"] != "secret" {
		t.Fatal("auth not preserved")
	}
}
func TestKafkaConfigAndConsumeBounds(t *testing.T) {
	for _, s := range []string{"{}", "[]", `{"tasks.max":2}`, `{"k":"v"} {"extra":"v"}`, strings.Repeat("x", kafkaEditorLimit+1)} {
		if _, e := kafkaConfigJSON(s); e == nil {
			t.Fatalf("accepted %q", kafkaShort(s, 50))
		}
	}
	if _, e := kafkaConfigJSON(`{"tasks.max":"2"}`); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"-1", "2147483648", "1,1", "1,", "x"} {
		if _, e := kafkaPartitionInput(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	partitions, e := kafkaPartitionInput("0,2, 4")
	if e != nil || len(partitions) != 3 {
		t.Fatal("valid partition list rejected")
	}
	now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.FixedZone("plus2", 7200))
	start, e := kafkaStartTime("today", now)
	if e != nil || start != "2026-09-30T00:00:00Z" {
		t.Fatalf("relative time %s %v", start, e)
	}
}

func TestKafkaBackHistoryNeverRetainsMutation(t *testing.T) {
	u, _ := newTestUI(t)
	for _, action := range []string{"delete-topic", "delete-group", "purge-subject", "delete-schema", "pause-connector", "update-connector", "produce", "expand-partitions"} {
		u.kafkaRememberRead(config.Request{Protocol: "kafka", Action: action})
	}
	if len(u.navigation) != 0 {
		t.Fatal("Back navigation could replay confirmed mutation without confirmation")
	}
	for i := 0; i < 40; i++ {
		u.kafkaRememberRead(config.Request{Protocol: "kafka", Action: "topics"})
	}
	if len(u.navigation) != 32 {
		t.Fatal("navigation history not bounded")
	}
}
