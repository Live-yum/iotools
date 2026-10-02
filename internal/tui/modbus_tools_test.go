package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestModbusToolsStructuredRulePreviewSaveDeleteAndCancel(t *testing.T) {
	u, v, _ := modbusTestView(t)
	before := string(u.raw)
	for i := 0; i < 2; i++ {
		v.modbusRuleForm(10)
		f := modbusFrontForm(t, u, "modbus-rule")
		f.GetFormItem(4).(*tview.InputField).SetText(`["/0"]`)
		modbusTestClick(f, 0)
		if n, _ := u.pages.GetFrontPage(); n != "modbus-rule" {
			t.Fatal("invalid operation previewed")
		}
		f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
		if string(u.raw) != before || u.running {
			t.Fatal("cancel changed source")
		}
	}
	v.add(engine.Event{Time: time.Unix(100, 0), Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(1)}, {"address": 20, "u16": uint16(2)}}})
	v.modbusRuleForm(10)
	f := modbusFrontForm(t, u, "modbus-rule")
	f.GetFormItem(1).(*tview.DropDown).SetCurrentOption(3)
	f.GetFormItem(3).(*tview.InputField).SetText("20")
	f.GetFormItem(4).(*tview.InputField).SetText(`["*2"]`)
	f.GetFormItem(9).(*tview.InputField).SetText(" V")
	modbusTestClick(f, 0)
	n, p := u.pages.GetFrontPage()
	if n != "modbus-rule-preview" {
		t.Fatalf("preview failed %s title %s", n, f.GetTitle())
	}
	panel := p.(*tview.Flex)
	text := panel.GetItem(0).(*tview.TextView).GetText(false)
	if !strings.Contains(text, "131076 V") {
		t.Fatal("coherent rule preview missing: " + text)
	}
	u.running = true
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if string(u.raw) != before {
		t.Fatal("busy save")
	}
	u.running = false
	u.readonly = true
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if string(u.raw) == before || v.values[10]["custom"] != "131076 V" {
		t.Fatal("rule save/reinterpret failed")
	}
	v.modbusRuleForm(10)
	f = modbusFrontForm(t, u, "modbus-rule")
	modbusTestClick(f, 1)
	_, p = u.pages.GetFrontPage()
	panel = p.(*tview.Flex)
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	rules, _ := engine.ModbusRules(v.modbus.request)
	if len(rules) != 1 {
		t.Fatal("cancel deleted")
	}
	v.modbusRuleForm(10)
	f = modbusFrontForm(t, u, "modbus-rule")
	modbusTestClick(f, 1)
	_, p = u.pages.GetFrontPage()
	modbusTestClick(p.(*tview.Flex).GetItem(1).(*tview.Form), 0)
	rules, _ = engine.ModbusRules(v.modbus.request)
	if len(rules) != 0 {
		t.Fatal("explicit delete failed")
	}
}
func TestModbusToolsRuleBoundsConflictAndStalePreview(t *testing.T) {
	u, v, r := modbusTestView(t)
	a := 10
	for _, rule := range []engine.RegisterRule{{Address: &a, Repr: "u32", Next: []int{20, 21}}, {Address: &a, Repr: "u16", Bits: map[int]string{16: "bad"}}, {Address: &a, Repr: "u16", Ops: []string{"/0"}}} {
		if _, err := modbusRuleReplace(r, a, &rule); err == nil {
			t.Fatal("invalid rule accepted")
		}
	}
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(1)}, {"address": 11, "u16": uint16(2)}}})
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(3)}}})
	rules, err := modbusRuleReplace(r, 10, &engine.RegisterRule{Address: &a, Repr: "u32"})
	if err != nil {
		t.Fatal(err)
	}
	v.modbusRulePreview(10, rules, false)
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	text := panel.GetItem(0).(*tview.TextView).GetText(false)
	if strings.Contains(text, "196610") {
		t.Fatal("stale word used")
	}
	if err = os.WriteFile(u.path, append(u.raw, []byte("\n#external\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if !strings.Contains(panel.GetItem(0).(*tview.TextView).GetTitle(), "外部修改") {
		t.Fatal("conflict not caught")
	}
}
func TestModbusToolsLabelsRulesPanelNoAutoReads(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Params["rules"] = []any{map[string]any{"address": 20, "repr": "u16"}}
	v.reset(r)
	v.labels[10] = "Cached"
	v.labels[30] = "Uncached"
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(2)}}})
	v.modbusAnnotationPanel(false)
	_, p := u.pages.GetFrontPage()
	table := p.(*tview.Table)
	if table.GetRowCount() != 4 {
		t.Fatal("annotation union missing")
	}
	table.Select(3, 0)
	table.GetInputCapture()(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	f := modbusFrontForm(t, u, "modbus-read-controls")
	if f.GetFormItem(1).(*tview.InputField).GetText() != "30" || u.running {
		t.Fatal("unread annotation did not preview")
	}
	f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	v.modbusAnnotationPanel(true)
	_, p = u.pages.GetFrontPage()
	table = p.(*tview.Table)
	if table.GetRowCount() != 2 {
		t.Fatal("rule filter wrong")
	}
	table.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if u.running {
		t.Fatal("panel connected")
	}
}
func TestModbusToolsRawValidationPreviewGateAndFocus(t *testing.T) {
	u, v, r := modbusTestView(t)
	for _, bad := range []struct {
		unit, code int
		s          string
	}{{0, 3, "00000001"}, {1, 99, "00000001"}, {1, 3, "0000ffff"}, {1, 6, "000"}, {1, 5, "00001234"}, {1, 16, "00000002020001"}} {
		if _, err := modbusRawCandidate(r, bad.unit, bad.code, bad.s); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	read, err := modbusRawCandidate(r, 1, 3, "00 0a\n00 01")
	if err != nil || read.Mutates() || read.String("pdu_hex", "") != "03000a0001" {
		t.Fatal("read classification")
	}
	before := string(u.raw)
	for i := 0; i < 2; i++ {
		v.modbusRawForm()
		f := modbusFrontForm(t, u, "modbus-raw")
		f.GetFormItem(1).(*tview.InputField).SetText("6")
		f.GetFormItem(2).(*tview.InputField).SetText("000a0001")
		modbusTestClick(f, 0)
		if n, _ := u.pages.GetFrontPage(); n != "modbus-raw" {
			t.Fatal("readonly raw write admitted")
		}
		f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
		if u.running || string(u.raw) != before {
			t.Fatal("cancel changed state")
		}
	}
	u.readonly = false
	write, err := modbusRawCandidate(r, 1, 6, "000a0001")
	if err != nil {
		t.Fatal(err)
	}
	v.modbusRawPreview(write)
	f := modbusFrontForm(t, u, "modbus-raw-preview")
	u.running = true
	modbusTestClick(f, 0)
	if n, _ := u.pages.GetFrontPage(); n != "modbus-raw-preview" {
		t.Fatal("busy bypass")
	}
	u.running = false
	modbusTestClick(f, 0)
	n, p := u.pages.GetFrontPage()
	if n != "derived-confirm" {
		t.Fatal(n)
	}
	panel := p.(*tview.Flex)
	if u.App.GetFocus() != panel.GetItem(0) {
		t.Fatal("closing raw preview stole confirmation focus")
	}
	text := panel.GetItem(0).(*tview.TextView).GetText(false)
	if !strings.Contains(text, "06000a0001") || !strings.Contains(text, "write-raw") {
		t.Fatal("PDU absent from confirmation")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if u.running {
		t.Fatal("cancel sent PDU")
	}
}
func TestModbusToolsDeviceIDRawResultsAndProbeSelection(t *testing.T) {
	u, v, r := modbusTestView(t)
	v.modbusDeviceIDForm()
	f := modbusFrontForm(t, u, "modbus-device-id")
	f.GetFormItem(2).(*tview.InputField).SetText("256")
	modbusTestClick(f, 0)
	if u.running || !strings.Contains(f.GetTitle(), "对象") {
		t.Fatal("invalid object accepted")
	}
	f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	r.Action = "read-device-id"
	r.Params["read_code"] = 3
	v.reset(r)
	v.add(engine.Event{Kind: "device-identification", Data: map[string]any{"objects": map[int]string{2: "rev", 0: "vendor[red]"}}})
	if v.table.GetRowCount() != 3 || v.table.GetCell(1, 0).Text != "0x00" || !strings.Contains(v.table.GetTitle(), "3") {
		t.Fatal("native ID table")
	}
	r.Action = "read-raw"
	v.reset(r)
	v.add(engine.Event{Kind: "raw-pdu", Data: map[string]any{"request_hex": "0300000001", "response_hex": "03020001"}})
	if v.table.GetCell(2, 2).Text != "03020001" {
		t.Fatal("raw response table")
	}
	r.Action = "scan-units"
	v.reset(r)
	v.add(engine.Event{Kind: "unit-probe", Data: map[string]any{"unit": 7, "responsive": true, "type": "read-coils", "address": 100, "count": 2, "values": []any{true, false}}})
	v.table.Select(1, 0)
	v.modbusInteractionKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	f = modbusFrontForm(t, u, "modbus-read-controls")
	if f.GetFormItem(2).(*tview.InputField).GetText() != "7" || u.running {
		t.Fatal("probe selection auto-connected/wrong unit")
	}
	idx, _ := f.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
	if idx != 2 {
		t.Fatal("probe space lost")
	}
}
func TestModbusToolsSweepProbeBoundsCancelAndPreview(t *testing.T) {
	for _, s := range []string{"0", "1,1", "248", "", "1-10", strings.Repeat("1,", 40)} {
		if _, err := modbusUnits(s); err == nil {
			t.Fatal(s)
		}
	}
	u, v, _ := modbusTestView(t)
	before := string(u.raw)
	for _, scan := range []bool{false, true} {
		for i := 0; i < 2; i++ {
			v.modbusRangeForm(scan)
			name := "modbus-sweep"
			if scan {
				name = "modbus-unit-scan"
			}
			f := modbusFrontForm(t, u, name)
			f.GetFormItem(2).(*tview.InputField).SetText("126")
			modbusTestClick(f, 0)
			if n, _ := u.pages.GetFrontPage(); n != name {
				t.Fatal("invalid range previewed")
			}
			f.GetFormItem(2).(*tview.InputField).SetText("2")
			if scan {
				f.GetFormItem(3).(*tview.InputField).SetText("2,4,6")
				f.GetFormItem(4).(*tview.Checkbox).SetChecked(true)
			} else {
				f.GetFormItem(4).(*tview.InputField).SetText("20")
				f.GetFormItem(5).(*tview.InputField).SetText("2")
			}
			modbusTestClick(f, 0)
			f = modbusFrontForm(t, u, "modbus-range-preview")
			text := f.GetFormItem(0).(*tview.TextView).GetText(false)
			if scan && !strings.Contains(text, "stop_first") || !scan && !strings.Contains(text, "sweep_cycles") {
				t.Fatal("scope preview missing")
			}
			f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
			if u.running || string(u.raw) != before {
				t.Fatal("preview/cancel ran or saved")
			}
		}
	}
}
func TestModbusToolsForms80x24AndSafetyKeys(t *testing.T) {
	u, screen := newTestUI(t)
	screen.SetSize(80, 24)
	var r config.Request
	for _, candidate := range u.collection.Requests {
		if candidate.Protocol == "modbus" {
			r = candidate
			break
		}
	}
	u.lastRequest = r
	u.inspector.reset(r)
	v := u.inspector
	for _, open := range []func(){func() { v.modbusRuleForm(10) }, v.modbusRawForm, v.modbusDeviceIDForm, func() { v.modbusRangeForm(false) }, func() { v.modbusRangeForm(true) }} {
		open()
		_, p := u.pages.GetFrontPage()
		form := p.(*tview.Form)
		for i := 0; i < form.GetButtonCount(); i++ {
			form.SetFocus(form.GetFormItemCount() + i)
			u.App.SetFocus(form)
			u.App.ForceDraw()
			x, y, w, h := form.GetButton(i).GetRect()
			if x < 0 || y < 0 || w < 1 || h < 1 || x+w > 80 || y+h > 24 {
				t.Fatalf("bad button %d rect %d,%d %dx%d", i, x, y, w, h)
			}
		}
		cancelled := false
		u.cancel = func() { cancelled = true }
		form.GetInputCapture()(tcell.NewEventKey(tcell.KeyF8, 0, 0))
		if !cancelled {
			t.Fatal("F8 hidden by form")
		}
		form.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	}
}
func TestModbusToolsLiveIDAndProbeWorkflows(t *testing.T) {
	u, _ := newTestUI(t)
	ready, result := make(chan struct{}), make(chan string, 8)
	var once sync.Once
	last := ""
	u.App.SetAfterDrawFunc(func(tcell.Screen) {
		once.Do(func() { close(ready) })
		if !u.running && u.inspector.modbus != nil {
			kind := u.inspector.modbus.tools.kind
			if kind != "" && kind != last {
				last = kind
				result <- kind
			}
		}
	})
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("start")
	}
	defer func() {
		u.App.Stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("stop")
		}
	}()
	r := config.Request{ID: "tools-local", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 225, "address": 10, "count": 2}}
	u.App.QueueUpdateDraw(func() {
		u.lastRequest = r
		u.inspector.reset(r)
		u.inspector.modbusDeviceIDForm()
		_, p := u.pages.GetFrontPage()
		f := p.(*tview.Form)
		f.GetFormItem(1).(*tview.DropDown).SetCurrentOption(2)
		modbusTestClick(f, 0)
	})
	select {
	case kind := <-result:
		if kind != "device-identification" {
			t.Fatal(kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ID did not settle")
	}
	u.App.QueueUpdateDraw(func() {
		u.inspector.modbusRangeForm(true)
		_, p := u.pages.GetFrontPage()
		f := p.(*tview.Form)
		f.GetFormItem(3).(*tview.InputField).SetText("225,226")
		f.GetFormItem(4).(*tview.Checkbox).SetChecked(true)
		modbusTestClick(f, 0)
		_, p = u.pages.GetFrontPage()
		f = p.(*tview.Form)
		modbusTestClick(f, 0)
		modbusTestClick(f, 0)
	})
	select {
	case kind := <-result:
		if kind != "unit-probe" {
			t.Fatal(kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not settle")
	}
	u.App.QueueUpdateDraw(func() {
		if len(u.inspector.modbus.tools.probes) != 1 {
			t.Error(fmt.Sprint("duplicate probe: ", len(u.inspector.modbus.tools.probes)))
		}
	})
}
func TestModbusToolsRecoveryPreviewAndUnknownBatch(t *testing.T) {
	u, v, r := modbusTestView(t)
	v.modbusRangeForm(false)
	f := modbusFrontForm(t, u, "modbus-sweep")
	if f.GetFormItem(7).(*tview.Checkbox).IsChecked() {
		t.Fatal("recovery enabled by default")
	}
	f.GetFormItem(4).(*tview.InputField).SetText("15999")
	f.GetFormItem(5).(*tview.InputField).SetText("1000")
	f.GetFormItem(7).(*tview.Checkbox).SetChecked(true)
	modbusTestClick(f, 0)
	if n, _ := u.pages.GetFrontPage(); n != "modbus-sweep" {
		t.Fatal("unsafe worst-case preview")
	}
	f.GetFormItem(4).(*tview.InputField).SetText("10")
	f.GetFormItem(5).(*tview.InputField).SetText("2")
	modbusTestClick(f, 0)
	f = modbusFrontForm(t, u, "modbus-range-preview")
	if !strings.Contains(f.GetFormItem(0).(*tview.TextView).GetText(false), "sweep_recover") {
		t.Fatal("review hides recovery")
	}
	f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	r.Action = "sweep-holding"
	v.reset(r)
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(1)}, {"address": 11, "u16": uint16(2)}}})
	v.add(engine.Event{Kind: "sweep-error", Data: map[string]any{"batch_address": 10, "batch_count": 2, "skipped_position": 10}})
	if v.values[10] != nil || v.values[11] != nil || len(v.modbus.tools.unknown) != 2 {
		t.Fatal("failed batch still claimed known")
	}
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 11, "u16": uint16(3)}}})
	v.add(engine.Event{Kind: "sweep-progress", Data: map[string]any{"cycle": 1, "cycles": 1, "through": 11, "end_address": 11, "failed_batches": 1, "skipped_positions": 1}})
	if len(v.modbus.tools.unknown) != 1 || v.values[10] != nil || v.values[11] == nil || !strings.Contains(v.table.GetTitle(), "1个地址仍未知") {
		t.Fatal("recovery unknown tracking incorrect")
	}
}
func TestModbusToolsSweepCSVSpaceMetadata(t *testing.T) {
	_, v, r := modbusTestView(t)
	for action, want := range map[string]string{"sweep-holding": "holding", "sweep-input": "input", "sweep-coils": "coil", "sweep-discrete": "discrete", "read-write-registers": "holding"} {
		r.Action = action
		v.reset(r)
		v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(1)}}})
		csv, err := v.modbusDumpCSV()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := engine.ParseMTUICSV(csv, false)
		if err != nil || parsed[engine.ModbusCSVCell{Type: want, Address: 10}].Value != 1 {
			t.Fatalf("%s %s %v", action, csv, err)
		}
	}
}

func TestModbusRuleInitialButtonsVisible80x24(t *testing.T) {
	u, screen := newTestUI(t)
	v := u.inspector
	v.reset(config.Request{ID: "rule-test", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"unit": 1, "address": 10, "count": 2}})
	screen.SetSize(80, 24)
	v.modbusRuleForm(10)
	u.App.ForceDraw()
	text := strings.ReplaceAll(snapshot(screen), " ", "")
	if !strings.Contains(text, "本机预览") || !strings.Contains(text, "取消") {
		t.Fatalf("initial rule actions clipped: %s", text)
	}
}
