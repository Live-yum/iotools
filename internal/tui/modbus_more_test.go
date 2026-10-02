package tui

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestModbusMorePerRowSampleTimeAndCoilCSV(t *testing.T) {
	_, v, r := modbusTestView(t)
	r.Action = "read-coils"
	r.Params["columns"] = map[string]any{"visible": []any{"address", "time", "u16"}, "time_mode": "ago"}
	v.reset(r)
	at := time.Date(2026, 9, 30, 10, 20, 30, 123000000, time.UTC)
	v.add(engine.Event{Time: at, Kind: "bits", Data: map[string]any{"address": 10, "values": []bool{true, false}}})
	if v.table.GetRowCount() != 3 || v.values[10]["u16"] != uint16(1) || v.values[11]["u16"] != uint16(0) {
		t.Fatal("bits did not become raw rows")
	}
	if got := modbusSampleTime(v.values[10], "read_at", at); got != "10:20:30.123" {
		t.Fatal(got)
	}
	if got := modbusSampleTime(v.values[10], "ago", at.Add(45*time.Second)); got != "45秒前" {
		t.Fatal(got)
	}
	v.add(engine.Event{Time: at.Add(time.Second), Kind: "bits", Data: map[string]any{"address": 11, "values": []bool{true}}})
	if v.values[10]["sampled_at"] != at || v.values[11]["sampled_at"] != at.Add(time.Second) {
		t.Fatal("timestamps changed unrelated rows")
	}
	data, err := v.modbusDumpCSV()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := engine.ParseMTUICSV(data, false)
	if err != nil || parsed[engine.ModbusCSVCell{Type: "coil", Address: 10}].Value != 1 {
		t.Fatalf("coil dump roundtrip: %v %s", err, data)
	}
	if parsed[engine.ModbusCSVCell{Type: "coil", Address: 10}].Time != at.Format(time.RFC3339Nano) {
		t.Fatal("CSV used relative/export time as sample time")
	}
	if !strings.Contains(string(data), "captured_utc") {
		t.Fatal("export time no longer distinguished")
	}
	v.reset(r)
	if len(v.values) != 0 {
		t.Fatal("old scope retained")
	}
}
func TestModbusMoreEventCopiesRowsAndRejectsInvalidBits(t *testing.T) {
	_, v, _ := modbusTestView(t)
	row := map[string]any{"address": 3, "u16": uint16(42)}
	event := engine.Event{Kind: "registers", Data: []map[string]any{row}}
	v.add(event)
	if _, ok := row["sampled_at"]; ok {
		t.Fatal("event source mutated")
	}
	if _, ok := v.values[3]["sampled_at"].(time.Time); !ok {
		t.Fatal("row missing timestamp")
	}
	for _, m := range []map[string]any{{"address": -1, "values": []bool{true}}, {"address": 65535, "values": []bool{true, false}}, {"address": 0, "values": make([]bool, 2001)}} {
		out := v.modbusMoreEvent(engine.Event{Kind: "bits", Data: m})
		if out.Kind == "registers" {
			t.Fatal("invalid bits admitted")
		}
	}
}
func TestModbusMoreConfigPreviewCancelSaveAndConflict(t *testing.T) {
	u, v, _ := modbusTestView(t)
	u.readonly = true
	original := append([]byte{}, u.raw...)
	count := len(u.collection.Requests)
	v.modbusConfigImportForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(2).(*tview.TextArea).SetText(`{"name":"imported","device":{"interface":"mock"},"api":{"enabled":true},"read_only":false}`, false)
	modbusTestClick(form, 0)
	name, p := u.pages.GetFrontPage()
	if name != "modbus-config-preview" || u.running {
		t.Fatal("not offline preview")
	}
	panel := p.(*tview.Flex)
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !bytes.Equal(u.raw, original) {
		t.Fatal("cancel saved")
	}
	plan, err := engine.ImportMTUIConfig([]byte(`{"device":{"interface":"mock"}}`), "new-device")
	if err != nil {
		t.Fatal(err)
	}
	v.modbusConfigPreview(plan, "memory.json")
	_, p = u.pages.GetFrontPage()
	panel = p.(*tview.Flex)
	view := panel.GetItem(0).(*tview.TextView)
	buttons := panel.GetItem(1).(*tview.Form)
	u.App.SetFocus(view)
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if !buttons.HasFocus() {
		t.Fatal("preview save unreachable")
	}
	modbusTestClick(buttons, 0)
	if u.running || !u.readonly || len(u.collection.Requests) != count+4 {
		t.Fatal("save ran request/changed permissions/lost entries")
	}
	saved, _, err := config.Load(u.path)
	if err != nil || len(saved.Requests) != count+4 {
		t.Fatal("collection not saved", err)
	}
	stable := append([]byte{}, u.raw...)
	if err := v.modbusSaveConfigPlan(plan); err == nil || !bytes.Equal(stable, u.raw) {
		t.Fatal("duplicate import changed state")
	}
	other, _ := engine.ImportMTUIConfig([]byte(`{}`), "other")
	u.running = true
	if err := v.modbusSaveConfigPlan(other); err == nil {
		t.Fatal("saved while running")
	}
	u.running = false
	if err := os.WriteFile(u.path, append(stable, []byte("\n# external\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.modbusSaveConfigPlan(other); err == nil || !bytes.Equal(stable, u.raw) {
		t.Fatal("external conflict overwritten")
	}
}
func TestModbusMoreTimeTemporarySaveAndCancel(t *testing.T) {
	u, v, _ := modbusTestView(t)
	original := string(u.raw)
	v.modbusTimeForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(0).(*tview.DropDown).SetCurrentOption(1)
	modbusTestClick(form, 2)
	if string(u.raw) != original || v.modbus.timeMode != "read_at" {
		t.Fatal("cancel changed mode")
	}
	v.modbusTimeForm()
	_, p = u.pages.GetFrontPage()
	form = p.(*tview.Form)
	form.GetFormItem(0).(*tview.DropDown).SetCurrentOption(1)
	modbusTestClick(form, 0)
	if string(u.raw) != original || v.modbus.timeMode != "ago" {
		t.Fatal("temporary mode saved or not applied")
	}
	v.modbusTimeForm()
	_, p = u.pages.GetFrontPage()
	form = p.(*tview.Form)
	modbusTestClick(form, 1)
	r, err := v.modbusSavedRequest()
	if err != nil || r.Params["columns"].(map[string]any)["time_mode"] != "ago" {
		t.Fatal("time mode not saved", err)
	}
	v.modbusColumnsPanel()
	_, p = u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	buttons := panel.GetItem(2).(*tview.Form)
	modbusTestClick(buttons, 1)
	r, _ = v.modbusSavedRequest()
	if r.Params["columns"].(map[string]any)["time_mode"] != "ago" {
		t.Fatal("column edit reset time mode")
	}
}
func TestModbusMoreCSVRequiresScopeAndNeverReadsDevice(t *testing.T) {
	u, v, _ := modbusTestView(t)
	v.values[1] = map[string]any{"address": 1, "u16": uint16(7)}
	v.modbusCSVDiffForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(2).(*tview.TextArea).SetText("type,address,u16,time\nholding,1,6,14:00\nholding,2,9,14:01\ncoil,1,1,14:00\n", false)
	modbusTestClick(form, 0)
	name, _ := u.pages.GetFrontPage()
	if name != "modbus-csv-import" {
		t.Fatal("unconfirmed CSV scope accepted")
	}
	form.GetFormItem(3).(*tview.Checkbox).SetChecked(true)
	modbusTestClick(form, 0)
	name, p = u.pages.GetFrontPage()
	if name != "modbus-csv-diff" || u.running {
		t.Fatal("CSV unexpectedly ran device request")
	}
	table := p.(*tview.Table)
	if table.GetRowCount() != 4 || !strings.Contains(table.GetTitle(), "变化1 未读2") {
		t.Fatal(table.GetTitle())
	}
	table.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'f', 0))
	if table.GetRowCount() != 2 {
		t.Fatal("changed filter wrong")
	}
	table.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if len(v.values) != 1 || len(v.baseline) != 0 {
		t.Fatal("comparison modified live values/baseline")
	}
}
func TestModbusMoreKeysKeepCancelResponsive(t *testing.T) {
	u, v, _ := modbusTestView(t)
	cancelled := 0
	u.cancel = func() { cancelled++ }
	u.running = true
	if e := v.modbusAdvancedKey(tcell.NewEventKey(tcell.KeyRune, 'M', 0)); e != nil {
		t.Fatal("M menu not handled")
	}
	_, p := u.pages.GetFrontPage()
	menu := p.(*tview.List)
	menu.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if cancelled != 0 {
		t.Fatal("closing local menu cancelled device")
	}
	v.modbusTimeForm()
	_, p = u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetInputCapture()(tcell.NewEventKey(tcell.KeyF8, 0, 0))
	if cancelled != 1 {
		t.Fatal("F8 unavailable in modal")
	}
	// Configured export must still include absolute raw value/address/sample time.
	u.running = false
	v.modbus.columns = []string{"label"}
	v.values[1] = map[string]any{"u16": uint16(65535), "sampled_at": time.Now()}
	data, err := v.modbusDumpCSV()
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || !strings.Contains(strings.Join(records[0], ","), "time") {
		t.Fatal("sample time absent in selected-column dump")
	}
}

func TestModbusMoreWriteLogOptInAndReadOnlyViewer(t *testing.T) {
	u, v, _ := modbusTestView(t)
	path := t.TempDir() + "/writes.jsonl"
	v.modbusConfigImportForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(2).(*tview.TextArea).SetText(`{"write_log":{"enabled":true,"directory":"/ignored"}}`, false)
	form.GetFormItem(3).(*tview.InputField).SetText(path)
	modbusTestClick(form, 0)
	name, _ := u.pages.GetFrontPage()
	if name != "modbus-config-import" {
		t.Fatal("unconfirmed log opt-in accepted")
	}
	form.GetFormItem(4).(*tview.Checkbox).SetChecked(true)
	modbusTestClick(form, 0)
	name, p = u.pages.GetFrontPage()
	if name != "modbus-config-preview" {
		t.Fatal("opt-in preview missing")
	}
	panel := p.(*tview.Flex)
	view := panel.GetItem(0).(*tview.TextView)
	if !strings.Contains(view.GetText(false), path) {
		t.Fatal("log path not shown in preview")
	}
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("import created write log")
	}
	for _, r := range u.collection.Requests {
		if strings.HasPrefix(r.ID, "mtui-") && (r.Mutates() || r.String("write_log_file", "") != path) {
			t.Fatal("log choice not saved only on read requests")
		}
	}
	entry := engine.ModbusWriteLogEntry{Timestamp: time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC), OperationID: "safe-operation-id", Phase: "attempt", Endpoint: "mock://local", Unit: 7, Address: 10, Count: 4, Type: "holding", Value: json.Number("18446744073709551615"), Function: "0x10", Status: "pending"}
	data, _ := json.Marshal(entry)
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	v.modbusWriteLogForm()
	_, p = u.pages.GetFrontPage()
	form = p.(*tview.Form)
	form.GetFormItem(0).(*tview.InputField).SetText(path)
	modbusTestClick(form, 0)
	name, p = u.pages.GetFrontPage()
	if name != "modbus-write-logs" || u.running {
		t.Fatal("viewer did not stay local")
	}
	table := p.(*tview.Table)
	if table.GetCell(1, 7).Text != "18446744073709551615" || table.GetCell(1, 1).Text != "attempt" {
		t.Fatal("viewer lost value precision/attempt semantics")
	}
	table.Select(1, 0)
	table.GetInputCapture()(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	name, p = u.pages.GetFrontPage()
	if name != "modbus-log-detail" || !strings.Contains(p.(*tview.TextView).GetText(false), "safe-operation-id") {
		t.Fatal("details unavailable")
	}
	p.(*tview.TextView).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	name, _ = u.pages.GetFrontPage()
	if name != "modbus-write-logs" {
		t.Fatal("detail escape did not return to log")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, append(data, '\n')) {
		t.Fatal("viewer modified log")
	}
}
func TestModbusMoreWriteLogPathValidation(t *testing.T) {
	if path, err := modbusReviewedLogPath("", false); err != nil || path != "" {
		t.Fatal("default log not off")
	}
	for _, tc := range []struct {
		path    string
		confirm bool
	}{{"", true}, {"logs.jsonl", false}, {"\nfile", true}, {"${env:SECRET}", true}, {strings.Repeat("x", 4097), true}} {
		if _, err := modbusReviewedLogPath(tc.path, tc.confirm); err == nil {
			t.Fatal("invalid log opt-in accepted")
		}
	}
}
