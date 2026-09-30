package tui

import (
	"context"
	"fmt"
	"math"
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

func modbusFrontForm(t *testing.T, u *UI, name string) *tview.Form {
	t.Helper()
	n, p := u.pages.GetFrontPage()
	if n != name {
		t.Fatalf("front %s wanted %s", n, name)
	}
	f, ok := p.(*tview.Form)
	if !ok {
		t.Fatalf("not form: %T", p)
	}
	return f
}
func TestModbusInteractionAddressControlsCancelRepeatAndSave(t *testing.T) {
	for q, want := range map[string]int{"123": 123, "xFF": 255, "0X10": 16, "+2": 12, "- 2": 8, "+x10": 26} {
		got, err := modbusParseAddress(q, 10)
		if err != nil || got != want {
			t.Fatalf("%s %d %v", q, got, err)
		}
	}
	for _, q := range []string{"65536", "x10000", "-11", "", "+", "garbage", "+65535"} {
		if _, err := modbusParseAddress(q, 10); err == nil {
			t.Fatal(q)
		}
	}
	u, v, r := modbusTestView(t)
	before := string(u.raw)
	for i := 0; i < 2; i++ {
		v.modbusReadForm(nil)
		f := modbusFrontForm(t, u, "modbus-read-controls")
		f.GetFormItem(1).(*tview.InputField).SetText("65535")
		f.GetFormItem(3).(*tview.InputField).SetText("2")
		modbusTestClick(f, 0)
		if !strings.Contains(f.GetTitle(), "越过") {
			t.Fatal("window overflow not rejected")
		}
		f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
		if u.running || string(u.raw) != before {
			t.Fatal("cancel changed state")
		}
	}
	v.modbusReadForm(nil)
	f := modbusFrontForm(t, u, "modbus-read-controls")
	f.GetFormItem(1).(*tview.InputField).SetText("100")
	f.GetFormItem(3).(*tview.InputField).SetText("4")
	u.running = true
	modbusTestClick(f, 0)
	if v.modbus.request.Int("address", 0) == 100 {
		t.Fatal("busy applied")
	}
	u.running = false
	modbusTestClick(f, 0)
	if u.running || string(u.raw) != before || v.modbus.request.Int("address", 0) != 100 {
		t.Fatal("temporary controls changed source or ran")
	}
	v.modbusReadForm(nil)
	f = modbusFrontForm(t, u, "modbus-read-controls")
	f.GetFormItem(2).(*tview.InputField).SetText("12")
	u.readonly = true
	modbusTestClick(f, 2)
	if u.running {
		t.Fatal("save connected")
	}
	saved, _, err := config.Load(u.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range saved.Requests {
		if entry.ID == r.ID {
			if entry.Int("unit", 0) != 12 || entry.Endpoint != r.Endpoint {
				t.Fatal("wrong saved control/source endpoint")
			}
		}
	}
	v.modbusReadForm(nil)
	f = modbusFrontForm(t, u, "modbus-read-controls")
	if err = os.WriteFile(u.path, append(u.raw, []byte("\n#external\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	modbusTestClick(f, 2)
	if !strings.Contains(f.GetTitle(), "外部修改") {
		t.Fatal("external edit overwritten")
	}
}
func TestModbusInteractionGoToAndSpaceIsolation(t *testing.T) {
	u, v, r := modbusTestView(t)
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 12, "u16": uint16(2)}, {"address": 20, "u16": uint16(3)}}})
	v.labels[20] = "Pump A"
	v.table.Select(1, 0)
	v.modbusGoToForm()
	f := modbusFrontForm(t, u, "modbus-go-to")
	f.GetFormItem(0).(*tview.InputField).SetText("Pump")
	modbusTestClick(f, 0)
	a, ok := v.modbusSelectedAddress()
	if !ok || a != 20 || u.running {
		t.Fatal("label jump failed")
	}
	v.modbusGoToForm()
	f = modbusFrontForm(t, u, "modbus-go-to")
	f.GetFormItem(0).(*tview.InputField).SetText("c20")
	modbusTestClick(f, 0)
	f = modbusFrontForm(t, u, "modbus-read-controls")
	modbusTestClick(f, 0)
	if v.modbus.request.Action != "read-coils" || len(v.values) != 0 || len(v.labels) != 0 || u.running {
		t.Fatal("space aliased cached rows/labels")
	}
	v.reset(r)
	v.labels[12] = "same"
	v.labels[20] = "same"
	v.modbusGoToForm()
	f = modbusFrontForm(t, u, "modbus-go-to")
	f.GetFormItem(0).(*tview.InputField).SetText("same")
	modbusTestClick(f, 0)
	if !strings.Contains(f.GetTitle(), "2项") {
		t.Fatal("ambiguous label selected")
	}
	f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
}
func TestModbusInteractionHistoryCoherenceBoundsInspectAndGraph(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Params["rules"] = []any{map[string]any{"address": 10, "repr": "u32", "next": []any{20}}}
	v.reset(r)
	add := func(at int, words map[int]uint16) {
		rows := []map[string]any{}
		for a, n := range words {
			rows = append(rows, map[string]any{"address": a, "u16": n})
		}
		v.add(engine.Event{Time: time.Unix(int64(at), 0), Kind: "registers", Data: rows})
	}
	add(1, map[int]uint16{10: 1, 11: 2, 20: 3})
	add(2, map[int]uint16{10: 4})
	h := v.modbus.interaction
	latest, _ := h.latest(10)
	if _, ok := latest["u32"]; ok {
		t.Fatal("stale multiword combined")
	}
	if len(h.series(10, "u32")) != 1 || len(h.series(10, "custom_numeric")) != 1 {
		t.Fatal("incomplete samples accepted")
	}
	v.table.Select(1, 0)
	v.modbusInspect(false)
	n, p := u.pages.GetFrontPage()
	if n != "modbus-inspect" {
		t.Fatal(n)
	}
	view := p.(*tview.TextView)
	if !strings.Contains(view.GetText(false), "未收到完整同响应数据") {
		t.Fatal("missing data masked")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'f', 0))
	frozen := view.GetText(false)
	add(3, map[int]uint16{10: 12})
	if view.GetText(false) != frozen {
		t.Fatal("paused graph refreshed")
	}
	cancelled := false
	u.cancel = func() { cancelled = true }
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if cancelled {
		t.Fatal("close cancelled acquisition")
	}
	v.modbusInspect(true)
	_, p = u.pages.GetFrontPage()
	p.(*tview.TextView).GetInputCapture()(tcell.NewEventKey(tcell.KeyF8, 0, 0))
	if !cancelled {
		t.Fatal("F8 did not cancel")
	}
	p.(*tview.TextView).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	for i := 0; i < 140; i++ {
		add(i+10, map[int]uint16{10: uint16(i)})
	}
	if len(h.frames) != 128 || h.dropped == 0 {
		t.Fatal("history not bounded")
	}
	large := map[int]uint16{}
	for i := 0; i < 2000; i++ {
		large[i] = uint16(i)
	}
	for i := 0; i < 12; i++ {
		add(i+200, large)
	}
	if h.words > modbusWordLimit {
		t.Fatal("word bound exceeded")
	}
	plot := modbusPlot([]modbusPoint{{value: -math.MaxFloat64}, {value: math.MaxFloat64}})
	if strings.Contains(plot, "NaN") || strings.Contains(plot, "+Inf") {
		t.Fatal("plot range overflow")
	}
	v.reset(r)
	if len(v.modbus.interaction.frames) != 0 {
		t.Fatal("history survived request scope reset")
	}
}
func TestModbusInteractionTypedWritePreviewGatesAndMock(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Endpoint = "mock://local"
	r.Params["unit"] = 240
	r.Params["address"] = 500
	v.reset(r)
	u.readonly = true
	v.modbusWriteForm()
	if n, _ := u.pages.GetFrontPage(); n == "modbus-write" {
		t.Fatal("readonly write form")
	}
	u.pages.RemovePage("modal")
	u.readonly = false
	before := string(u.raw)
	for i := 0; i < 2; i++ {
		v.modbusWriteForm()
		f := modbusFrontForm(t, u, "modbus-write")
		f.GetFormItem(3).(*tview.InputField).SetText("65536")
		modbusTestClick(f, 0)
		if !strings.Contains(f.GetTitle(), "range") {
			t.Fatal("overflow accepted")
		}
		f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
		if string(u.raw) != before || u.running {
			t.Fatal("cancel mutated")
		}
	}
	v.modbusWriteForm()
	f := modbusFrontForm(t, u, "modbus-write")
	f.GetFormItem(2).(*tview.DropDown).SetCurrentOption(6)
	f.GetFormItem(3).(*tview.InputField).SetText("18446744073709551615")
	modbusTestClick(f, 0)
	f = modbusFrontForm(t, u, "modbus-write-preview")
	if !strings.Contains(f.GetFormItem(0).(*tview.TextView).GetText(false), "18446744073709551615") {
		t.Fatal("u64 preview rounded")
	}
	u.readonly = true
	modbusTestClick(f, 0)
	if n, _ := u.pages.GetFrontPage(); n != "modbus-write-preview" {
		t.Fatal("late readonly bypass")
	}
	u.readonly = false
	modbusTestClick(f, 0)
	n, p := u.pages.GetFrontPage()
	if n != "derived-confirm" {
		t.Fatal(n)
	}
	text := p.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
	for _, s := range []string{"mock://local", "240", "500", "65535"} {
		if !strings.Contains(text, s) {
			t.Fatal("confirmation lost " + s)
		}
	}
	p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if u.running {
		t.Fatal("confirmation cancel wrote")
	}
	for _, vector := range []struct{ kind, value, fc string }{{"u64", "18446744073709551615", "multiple"}, {"i16", "-32768", "single"}, {"coil", "true", "single"}, {"coil", "false", "multiple"}} {
		write, _, err := modbusBuildWrite(r, 500, 240, vector.kind, vector.value, "DCBA", vector.fc)
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.Run(context.Background(), write, false, nil); err == nil {
			t.Fatal("ungated write")
		}
		if err = engine.Run(context.Background(), write, true, nil); err != nil {
			t.Fatal(err)
		}
		read := modbusReadRequest(write)
		seen := false
		if err = engine.Run(context.Background(), read, false, func(e engine.Event) {
			if e.Kind == "registers" || e.Kind == "bits" {
				seen = true
			}
		}); err != nil || !seen {
			t.Fatalf("mock readback %v", err)
		}
	}
	for _, vector := range []struct {
		k, s, fc string
		a, u     int
	}{{"u64", "1", "multiple", 65535, 1}, {"u32", "1", "single", 0, 1}, {"coil", "1", "single", 0, 1}, {"u16", "1", "multiple", 0, 0}} {
		if _, _, err := modbusBuildWrite(r, vector.a, vector.u, vector.k, vector.s, "ABCD", vector.fc); err == nil {
			t.Fatal("invalid write accepted")
		}
	}
}
func TestModbusInteractionIntegerEditingAndDispatchSafety(t *testing.T) {
	for _, v := range []struct {
		k, s       string
		delta, bit int
		want       string
	}{{"u64", "18446744073709551615", -1, -1, "18446744073709551614"}, {"i16", "0", 0, 15, "-32768"}, {"i16", "-32768", 0, 15, "0"}, {"u32", "0", 0, 31, "2147483648"}} {
		got, err := modbusAdjustInteger(v.k, v.s, v.delta, v.bit)
		if err != nil || got != v.want {
			t.Fatalf("%+v %s %v", v, got, err)
		}
	}
	for _, v := range []struct {
		k, s       string
		delta, bit int
	}{{"u16", "65535", 1, -1}, {"i16", "-32768", -1, -1}, {"u16", "0", 0, 16}, {"f32", "1", 1, -1}} {
		if _, err := modbusAdjustInteger(v.k, v.s, v.delta, v.bit); err == nil {
			t.Fatal("overflow accepted")
		}
	}
	u, v, r := modbusTestView(t)
	r.Params["keymap"] = map[string]any{"write": "x"}
	v.reset(r)
	u.readonly = true
	for _, e := range []*tcell.EventKey{tcell.NewEventKey(tcell.KeyCtrlC, 0, 0), tcell.NewEventKey(tcell.KeyEscape, 0, 0), tcell.NewEventKey(tcell.KeyF8, 0, 0)} {
		if v.modbusAdvancedKey(e) != e {
			t.Fatal("global safety captured")
		}
	}
	if v.modbusAdvancedKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0)) != nil {
		t.Fatal("action leaked")
	}
	if n, _ := u.pages.GetFrontPage(); n == "derived-confirm" || n == "modbus-write" {
		t.Fatal("remap bypassed readonly")
	}
	u.pages.RemovePage("modal")
	v.modbusDispatch("page-up")
	if n, _ := u.pages.GetFrontPage(); n == "modbus-read-controls" {
		t.Fatal("negative window opened")
	}
	for key, action := range map[rune]string{'R': "read-controls", '/': "go-to"} {
		if v.modbus.keymap[action] != key {
			t.Fatal("wrong default")
		}
		v.modbusAdvancedKey(tcell.NewEventKey(tcell.KeyRune, key, 0))
		n, p := u.pages.GetFrontPage()
		if !strings.HasPrefix(n, "modbus-") {
			t.Fatal(n)
		}
		p.(*tview.Form).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	}
	if len(modbusDefaultKeys) != 38 {
		t.Fatal(fmt.Sprint("expected38 actual native actions, got ", len(modbusDefaultKeys)))
	}
}

func TestModbusInteractionFC23FormAndAnnotationScope(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Endpoint = "mock://local"
	r.Params["unit"] = 238
	v.reset(r)
	u.readonly = false
	v.modbusWriteForm()
	f := modbusFrontForm(t, u, "modbus-write")
	f.GetFormItem(3).(*tview.InputField).SetText("42")
	f.GetFormItem(5).(*tview.DropDown).SetCurrentOption(2)
	f.GetFormItem(7).(*tview.InputField).SetText("65535")
	f.GetFormItem(8).(*tview.InputField).SetText("2")
	modbusTestClick(f, 0)
	if !strings.Contains(f.GetTitle(), "FC23需") {
		t.Fatal("FC23 range overflow")
	}
	f.GetFormItem(7).(*tview.InputField).SetText("20")
	modbusTestClick(f, 0)
	f = modbusFrontForm(t, u, "modbus-write-preview")
	if !strings.Contains(f.GetFormItem(0).(*tview.TextView).GetText(false), "地址20，数量2") {
		t.Fatal("missing read range preview")
	}
	modbusTestClick(f, 0)
	_, p := u.pages.GetFrontPage()
	text := p.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
	for _, want := range []string{"read-write-registers", "read_address", "read_count"} {
		if !strings.Contains(text, want) {
			t.Fatal(want)
		}
	}
	p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	// Unit/space changes require an explicit read-settings save before annotations.
	if err := v.modbusAnnotationScope(); err == nil {
		t.Fatal("different mock endpoint accepted annotations")
	}
	_, v, r = modbusTestView(t)
	v.modbus.request.Action = "read-coils"
	if err := v.modbusAnnotationScope(); err == nil {
		t.Fatal("space mismatch")
	}
	v.modbus.request = r
	v.modbus.request = copyRequest(r)
	v.modbus.request.Params["unit"] = 77
	if err := v.modbusSaveParams(map[string]any{"labels": map[string]any{"20": "wrong unit"}}); err == nil {
		t.Fatal("unit mismatch saved labels")
	}
}
func TestModbusInteractionForms80x24Keyboard(t *testing.T) {
	u, screen := newTestUI(t)
	screen.SetSize(80, 24)
	var r config.Request
	for _, candidate := range u.collection.Requests {
		if candidate.Protocol == "modbus" {
			r = candidate
			break
		}
	}
	r.Action = "read-holding"
	u.lastRequest = r
	u.inspector.reset(r)
	u.readonly = false
	v := u.inspector
	for _, open := range []func(){func() { v.modbusReadForm(nil) }, v.modbusWriteForm, v.modbusKeymapForm} {
		open()
		_, p := u.pages.GetFrontPage()
		form := p.(*tview.Form)
		for i := 0; i < form.GetButtonCount(); i++ {
			form.SetFocus(form.GetFormItemCount() + i)
			u.App.SetFocus(form)
			u.App.ForceDraw()
			x, y, w, h := form.GetButton(i).GetRect()
			if x < 0 || y < 0 || w < 1 || h < 1 || x+w > 80 || y+h > 24 {
				t.Fatalf("button%d rect %d,%d %dx%d", i, x, y, w, h)
			}
		}
		if capture := form.GetInputCapture(); capture != nil {
			capture(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
		} else {
			form.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
		}
	}
}
func TestModbusInteractionDirtyControlsSaveAndLocalReinterpret(t *testing.T) {
	u, v, r := modbusTestView(t)
	r.Params["rules"] = []any{map[string]any{"address": 10, "repr": "u32"}}
	v.reset(r)
	v.add(engine.Event{Time: time.Unix(1, 0), Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(0x1234)}, {"address": 11, "u16": uint16(0x5678)}}})
	v.add(engine.Event{Time: time.Unix(2, 0), Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(1)}}})
	before := string(u.raw)
	v.modbusCycleOrder()
	if v.modbus.request.String("word_order", "") != "BADC" || string(u.raw) != before || u.running {
		t.Fatal("word order not local")
	}
	if _, ok := v.values[10]["u32"]; ok {
		t.Fatal("reinterpret reused stale adjacent word")
	}
	changes, err := v.modbusUnsavedLayout()
	if err != nil || changes["word_order"] != "BADC" {
		t.Fatalf("dirty order ignored %v %v", changes, err)
	}
	next := modbusReadRequest(v.modbus.request)
	next.Action = "read-coils"
	next.Params["unit"] = 76
	next.Params["address"] = 12
	if err = v.modbusApplyRead(next, false); err != nil {
		t.Fatal(err)
	}
	changes, err = v.modbusUnsavedLayout()
	if err != nil || changes["__read_action"] != "read-coils" || changes["unit"] != 76 {
		t.Fatalf("dirty controls ignored %v %v", changes, err)
	}
	if err = v.modbusSaveParams(changes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(u.raw), "__read_action") {
		t.Fatal("internal marker serialized")
	}
	saved, err := v.modbusSavedRequest()
	if err != nil || saved.Action != "read-coils" || saved.Int("unit", 0) != 76 {
		t.Fatalf("wrong control save %v", err)
	}
}
func TestModbusInteractionLiveReadCancelAndConfirmedFC23(t *testing.T) {
	u, _ := newTestUI(t)
	ready, read, write := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var readyOnce, readOnce, writeOnce sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) {
		readyOnce.Do(func() { close(ready) })
		if u.modbusSession != nil && u.modbusSession.stats.reads > 0 {
			readOnce.Do(func() { close(read) })
		}
		if u.modbusSession != nil && u.modbusSession.stats.writes > 0 && !u.running {
			writeOnce.Do(func() { close(write) })
		}
	})
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("start timeout")
	}
	defer func() {
		u.App.Stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("stop timeout")
		}
	}()
	r := config.Request{ID: "interactive-local", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Timeout: "5s", Params: map[string]any{"unit": 237, "address": 50, "count": 1, "samples": 1000, "interval_ms": 10}}
	u.App.QueueUpdateDraw(func() {
		u.lastRequest = r
		u.inspector.reset(r)
		u.inspector.modbusReadForm(nil)
		_, p := u.pages.GetFrontPage()
		form := p.(*tview.Form)
		modbusTestClick(form, 1)
		u.inspector.modbusDispatch("refresh")
	})
	select {
	case <-read:
	case <-time.After(3 * time.Second):
		t.Fatal("explicit read did not run")
	}
	stopped := make(chan struct{})
	u.App.QueueUpdateDraw(func() {
		u.inspector.modbusDispatch("pause")
		u.modbusSession.statsView = nil
		u.App.SetAfterDrawFunc(func(tcell.Screen) {
			if !u.running {
				select {
				case <-stopped:
				default:
					close(stopped)
				}
			}
		})
	})
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not settle")
	}
	u.App.QueueUpdateDraw(func() {
		if len(u.inspector.modbus.interaction.frames) == 0 {
			t.Error("cancel unexpectedly cleared coherent results")
		}
		u.readonly = false
		u.inspector.modbusWriteForm()
		_, p := u.pages.GetFrontPage()
		f := p.(*tview.Form)
		f.GetFormItem(3).(*tview.InputField).SetText("1234")
		f.GetFormItem(5).(*tview.DropDown).SetCurrentOption(2)
		modbusTestClick(f, 0)
		_, p = u.pages.GetFrontPage()
		modbusTestClick(p.(*tview.Form), 0)
		_, p = u.pages.GetFrontPage()
		button := p.(*tview.Flex).GetItem(1).(*tview.Flex).GetItem(1).(*tview.Button)
		button.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		button.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	u.App.QueueUpdateDraw(func() {
		u.App.SetAfterDrawFunc(func(tcell.Screen) {
			if u.modbusSession != nil && u.modbusSession.stats.writes > 0 && !u.running {
				writeOnce.Do(func() { close(write) })
			}
		})
	})
	select {
	case <-write:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmed FC23 did not finish")
	}
	u.App.QueueUpdateDraw(func() {
		if u.modbusSession.stats.writes != 1 || u.inspector.values[50]["u16"] != uint16(1234) {
			t.Error("duplicate write or wrong readback")
		}
		u.inspector.modbusDispatch("clear-session")
		if len(u.inspector.modbus.interaction.frames) != 0 {
			t.Error("session clear retained coherent samples")
		}
	})
}
func TestModbusInteractionCopyLiteralAndCoherentPrevious(t *testing.T) {
	u, v, r := modbusTestView(t)
	u.readonly = false
	r.Endpoint = "mock://local"
	r.Params["unit"] = 236
	r.Params["columns"] = map[string]any{"visible": []any{"address", "label", "u16"}}
	v.reset(r)
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(1)}, {"address": 11, "u16": uint16(2)}}})
	v.add(engine.Event{Kind: "registers", Data: []map[string]any{{"address": 10, "u16": uint16(3)}}})
	v.labels[10] = "tag[red]"
	v.renderRegisters()
	v.table.Select(1, 1)
	v.modbusDispatch("copy-column")
	n, p := u.pages.GetFrontPage()
	if n != "clipboard" {
		t.Fatal(n)
	}
	text := p.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
	if !strings.Contains(text, "tag[red]") || strings.Contains(text, "tag[red[]") {
		t.Fatal(text)
	}
	p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	write, preview, err := modbusBuildWrite(r, 10, 236, "u32", "42", "ABCD", "multiple")
	if err != nil {
		t.Fatal(err)
	}
	v.modbusWritePreview(write, preview)
	f := modbusFrontForm(t, u, "modbus-write-preview")
	modbusTestClick(f, 0)
	_, p = u.pages.GetFrontPage()
	text = p.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
	if strings.Contains(text, "write_log_previous") {
		t.Fatal("mixed previous words admitted")
	}
	p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
}
