package tui

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func modbusTestView(t *testing.T) (*UI, *inspector, config.Request) {
	t.Helper()
	u, _ := newTestUI(t)
	for i, r := range u.collection.Requests {
		if r.Protocol == "modbus" && r.Action == "read-holding" {
			u.selected = i
			u.lastRequest = r
			u.inspector.reset(r)
			return u, u.inspector, r
		}
	}
	t.Fatal("sample has no Modbus read")
	return nil, nil, config.Request{}
}
func modbusTestClick(form *tview.Form, index int) {
	form.GetButton(index).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
}
func TestModbusColumnsValidateRenderAllRepresentations(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Params = map[string]any{"columns": map[string]any{"visible": []any{"address", "u64", "binary", "ascii", "f16"}, "widths": map[string]any{"ascii": 3}, "address_mode": "hex"}}
	v.reset(r)
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 200, "u16": uint16(1), "u64": "18446744073709551615", "binary": "0000000000000001", "ascii": "hello", "f16": "1.5"}}})
	if v.table.GetCell(1, 0).Text != "0x00C8" || v.table.GetCell(1, 1).Text != "18446744073709551615" {
		t.Fatal("configured representations lost precision/address mode")
	}
	if v.table.GetCell(1, 2).GetReference() != 200 {
		t.Fatal("column reorder lost register reference")
	}
	u.App.ForceDraw()
	for _, cfg := range []any{"bad", map[string]any{"visible": []any{}}, map[string]any{"visible": []any{"u16", "u16"}}, map[string]any{"visible": []any{"password"}}, map[string]any{"visible": []any{"u16"}, "widths": map[string]any{"u16": 121}}, map[string]any{"visible": []any{"u16"}, "address_mode": "octal"}} {
		if _, _, _, err := modbusParseColumns(cfg); err == nil {
			t.Fatalf("accepted %#v", cfg)
		}
	}
}
func TestModbusKeymapScopedAndCollisionSafe(t *testing.T) {
	_, v, r := modbusTestView(t)
	r.Params = map[string]any{"keymap": map[string]any{"pin": "x"}}
	v.reset(r)
	e := tcell.NewEventKey(tcell.KeyRune, 'x', 0)
	mapped := v.modbusAdvancedKey(e)
	if mapped == nil || mapped.Rune() != 'p' {
		t.Fatal("custom pin not translated")
	}
	if v.modbusAdvancedKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0)) != nil {
		t.Fatal("old mapping remained active")
	}
	for _, e := range []*tcell.EventKey{tcell.NewEventKey(tcell.KeyCtrlC, 0, 0), tcell.NewEventKey(tcell.KeyEscape, 0, 0), tcell.NewEventKey(tcell.KeyF8, 0, 0), tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModCtrl)} {
		if v.modbusAdvancedKey(e) != e {
			t.Fatal("keymap captured safety/global key")
		}
	}
	for _, cfg := range []any{map[string]any{"pin": "l"}, map[string]any{"pin": "q"}, map[string]any{"pin": "?"}, map[string]any{"pin": "\n"}, map[string]any{"pin": "xx"}, map[string]any{"write": "w"}} {
		if _, err := modbusParseKeymap(cfg); err == nil {
			t.Fatalf("accepted %#v", cfg)
		}
	}
}
func TestModbusColumnsPanelCancelAndSavePreserveSource(t *testing.T) {
	u, v, r := modbusTestView(t)
	original := string(u.raw)
	v.modbusColumnsPanel()
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if string(u.raw) != original || v.modbus.columns != nil {
		t.Fatal("cancel changed config")
	}
	cfg := map[string]any{"visible": []any{"address", "u16", "u64"}, "widths": map[string]any{"u64": 22}, "address_mode": "decimal"}
	if err := v.modbusSaveParams(map[string]any{"columns": cfg}); err != nil {
		t.Fatal(err)
	}
	saved, _, err := config.Load(u.path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range saved.Requests {
		if entry.ID == r.ID {
			found = true
			if entry.Endpoint != r.Endpoint {
				t.Fatal("saved resolved endpoint")
			}
			if entry.Params["columns"] == nil {
				t.Fatal("columns not persisted")
			}
		}
	}
	if !found {
		t.Fatal("request lost")
	}
	old := string(u.raw)
	if err := os.WriteFile(u.path, []byte(old+"\n# external\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.modbusSaveParams(map[string]any{"keymap": map[string]any{"pin": "x"}}); err == nil {
		t.Fatal("external change overwritten")
	}
	if string(u.raw) != old {
		t.Fatal("failed save changed memory")
	}
}
func TestModbusImportMergeMatchesMTUIUnion(t *testing.T) {
	source := config.Request{Action: "read-holding", Params: map[string]any{"pins": []any{1}, "labels": map[string]any{"1": "old", "3": "keep"}, "rules": []any{map[string]any{"address": 1, "repr": "u16", "suffix": " old"}}}}
	incoming, err := engine.ImportMTUIRegisters([]byte(`{"holdings":[{"address":1,"label":"新标签","custom":{"repr":"i16"}},{"address":2,"pinned":true}],"inputs":[{"address":1,"label":"other space"}]}`), source.Action)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := modbusMergeAnnotations(source, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(merged["pins"], []any{1, 2}) {
		t.Fatalf("pins not union: %#v", merged["pins"])
	}
	labels := merged["labels"].(map[string]any)
	if labels["1"] != "新标签" || labels["3"] != "keep" {
		t.Fatal("merge overwrite scope wrong")
	}
	if source.Params["labels"].(map[string]any)["1"] != "old" {
		t.Fatal("source mutated")
	}
	if merged["rules"].([]any)[0].(map[string]any)["repr"] != "i16" {
		t.Fatal("incoming rule did not replace address")
	}
}
func TestModbusImportPreviewCancelAndExplicitSave(t *testing.T) {
	u, v, r := modbusTestView(t)
	original := string(u.raw)
	v.modbusImportForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(1).(*tview.TextArea).SetText(`{"version":1,"device":{"host":"attacker.invalid"},"registers":{"holdings":[{"address":10,"label":"电压","pinned":true}]}}`, false)
	modbusTestClick(form, 0)
	page, p := u.pages.GetFrontPage()
	if page != "modbus-import-preview" || u.running {
		t.Fatal("import did not remain offline preview")
	}
	panel := p.(*tview.Flex)
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if string(u.raw) != original {
		t.Fatal("cancel persisted import")
	}
	imported, err := engine.ImportMTUIRegisters([]byte(`{"holdings":[{"address":10,"label":"电压","pinned":true}]}`), r.Action)
	if err != nil {
		t.Fatal(err)
	}
	source, err := v.modbusSavedRequest()
	if err != nil {
		t.Fatal(err)
	}
	merged, err := modbusMergeAnnotations(source, imported)
	if err != nil {
		t.Fatal(err)
	}
	v.modbusImportPreview(merged)
	_, p = u.pages.GetFrontPage()
	panel = p.(*tview.Flex)
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if !v.pins[10] || v.labels[10] != "电压" || u.running {
		t.Fatal("explicit local import save failed")
	}
	saved, err := v.modbusSavedRequest()
	if err != nil || saved.Endpoint != r.Endpoint {
		t.Fatal("import changed device target")
	}
}
func TestModbusPrivateExportAndCSV(t *testing.T) {
	_, v, _ := modbusTestView(t)
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 2, "u16": uint16(65535), "i16": int16(-1)}, {"address": 1, "u16": uint16(3)}}})
	v.labels[1] = "=HYPERLINK(\"bad\")"
	data, err := v.modbusDumpCSV()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[1][2] != "1" || rows[1][4] != "3" || !strings.HasPrefix(rows[1][5], "'=HYPERLINK") {
		t.Fatalf("CSV precision/order/formula defense: %#v", rows)
	}
	path := filepath.Join(t.TempDir(), "dump.csv")
	if err = modbusWritePrivate(path, data); err != nil {
		t.Fatal(err)
	}
	if err = modbusWritePrivate(path, []byte("overwrite")); err == nil {
		t.Fatal("export overwrote prior file")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatal("failed overwrite damaged original")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("export not private")
		}
	}
}
func TestModbusImportedPreferencesDoNotGrantDeviceWrites(t *testing.T) {
	u, v, _ := modbusTestView(t)
	u.readonly = true
	cfg := map[string]any{"keymap": map[string]any{"pin": "x"}}
	if err := v.modbusSaveParams(cfg); err != nil {
		t.Fatal("readonly should permit local presentation config", err)
	}
	if !u.readonly || u.running {
		t.Fatal("local preferences changed write capability")
	}
	u.running = true
	if err := v.modbusSaveParams(cfg); err == nil {
		t.Fatal("save during polling accepted")
	}
}

func TestModbusColumnsKeyboardReachesSaveAndPreservesCancel(t *testing.T) {
	u, v, _ := modbusTestView(t)
	v.modbusColumnsPanel()
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	table := panel.GetItem(1).(*tview.Table)
	table.Select(1, 0)
	table.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	for i := 0; i < 3; i++ {
		panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	}
	buttons := panel.GetItem(2).(*tview.Form)
	_, index := buttons.GetFocusedItemIndex()
	if index != 1 || !buttons.HasFocus() {
		t.Fatalf("Save button unreachable: %d", index)
	}
	u.App.GetFocus().InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if page, _ := u.pages.GetFrontPage(); page != "main" {
		t.Fatal("keyboard save did not close")
	}
	if len(v.modbus.columns) != 12 || v.modbus.columns[0] != "address" {
		t.Fatal("column toggle not saved")
	}
	if _, err := v.modbusSavedRequest(); err != nil {
		t.Fatal(err)
	}
}
