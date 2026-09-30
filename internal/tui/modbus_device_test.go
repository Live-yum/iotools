package tui

import (
	"context"
	"errors"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModbusDeviceFormCancelBoundsAndReview(t *testing.T) {
	u, v, _ := modbusTestView(t)
	before := string(u.raw)
	for n := 0; n < 2; n++ {
		v.modbusDeviceForm()
		f := modbusFrontForm(t, u, "modbus-device")
		f.GetFormItem(0).(*tview.DropDown).SetCurrentOption(2)
		f.GetFormItem(1).(*tview.InputField).SetText("user:password@host")
		modbusTestClick(f, 2)
		if name, _ := u.pages.GetFrontPage(); name != "modbus-device" {
			t.Fatal("unsafe target accepted")
		}
		f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
		if u.running || len(u.localCancels) != 0 || string(u.raw) != before {
			t.Fatal("opening/cancel caused work")
		}
	}
	v.modbusDeviceForm()
	f := modbusFrontForm(t, u, "modbus-device")
	f.GetFormItem(0).(*tview.DropDown).SetCurrentOption(3)
	f.GetFormItem(1).(*tview.InputField).SetText("::1")
	f.GetFormItem(2).(*tview.InputField).SetText("1502")
	f.GetFormItem(4).(*tview.InputField).SetText("247")
	modbusTestClick(f, 2)
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	text := panel.GetItem(0).(*tview.TextView).GetText(false)
	if !strings.Contains(text, "rtu+tcp://[::1]:1502") || !strings.Contains(text, "Unit：247") {
		t.Fatal(text)
	}
	u.running = true
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if string(u.raw) != before {
		t.Fatal("busy apply")
	}
	u.running = false
	u.readonly = true
	modbusTestClick(panel.GetItem(1).(*tview.Form), 0)
	if u.running || v.modbus.request.Endpoint != "rtu+tcp://[::1]:1502" || string(u.raw) != before {
		t.Fatal("temporary apply saved/connected")
	}
	v.modbusDeviceForm()
	f = modbusFrontForm(t, u, "modbus-device")
	f.GetFormItem(0).(*tview.DropDown).SetCurrentOption(1)
	f.GetFormItem(3).(*tview.InputField).SetText("COM7")
	f.GetFormItem(11).(*tview.InputField).SetText("5001")
	if _, e := modbusDeviceCandidate(v.modbus.request, f); e == nil {
		t.Fatal("serial timeout unbounded")
	}
	f.GetFormItem(11).(*tview.InputField).SetText("5000")
	f.GetFormItem(3).(*tview.InputField).SetText(`\\server\device`)
	if _, e := modbusDeviceCandidate(v.modbus.request, f); e == nil {
		t.Fatal("network share accepted")
	}
}
func TestModbusDeviceSavePreservesTemplateAndExternalConflict(t *testing.T) {
	u, v, r := modbusTestView(t)
	resolved, e := u.collection.Resolve(r, u.profile)
	if e != nil {
		t.Fatal(e)
	}
	candidate := modbusReadRequest(resolved)
	candidate.Params["connect_timeout_ms"] = 1234
	candidate.Params["request_timeout_ms"] = 2345
	candidate.Params["request_gap_ms"] = 12
	candidate.Timeout = "15s"
	if e = v.modbusSaveDevice(candidate); e != nil {
		t.Fatal(e)
	}
	saved, e := v.modbusSavedRequest()
	if e != nil || saved.Endpoint != r.Endpoint || saved.Int("request_timeout_ms", 0) != 2345 {
		t.Fatal(saved, e)
	}
	before := string(u.raw)
	if e = os.WriteFile(u.path, append(u.raw, []byte("\n# external\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	candidate.Endpoint = "mock://local"
	if e = v.modbusSaveDevice(candidate); e == nil || string(u.raw) != before {
		t.Fatal("external conflict ignored", e)
	}
}
func TestModbusDeviceDiscoveryPreviewNoAutomaticWork80x24(t *testing.T) {
	u, screen := newTestUI(t)
	v := u.inspector
	r := config.Request{ID: "device", Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://127.0.0.1:502", Params: map[string]any{"unit": 1, "address": 0, "count": 1}}
	v.reset(r)
	screen.SetSize(80, 24)
	v.modbusDeviceForm()
	f := modbusFrontForm(t, u, "modbus-device")
	u.App.ForceDraw()
	s := strings.ReplaceAll(snapshot(screen), " ", "")
	for _, want := range []string{"列串口", "网络发现", "预览", "取消"} {
		if !strings.Contains(s, want) {
			t.Fatalf("button clipped %s %s", want, s)
		}
	}
	modbusTestClick(f, 1)
	nf := modbusFrontForm(t, u, "modbus-network-form")
	nf.GetFormItem(0).(*tview.InputField).SetText("127.0.0.1/24")
	modbusTestClick(nf, 0)
	if name, _ := u.pages.GetFrontPage(); name != "modbus-network-form" {
		t.Fatal("unaligned prefix expanded")
	}
	nf.GetFormItem(0).(*tview.InputField).SetText("127.0.0.0/29")
	modbusTestClick(nf, 0)
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	text := panel.GetItem(0).(*tview.TextView).GetText(false)
	for _, want := range []string{"数量：6", "502", "500ms", "并发：8", "127.0.0.1", "127.0.0.6"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
	if u.running || len(u.localCancels) != 0 {
		t.Fatal("review started scan")
	}
	u.App.ForceDraw()
	if !strings.Contains(strings.ReplaceAll(snapshot(screen), " ", ""), "确认开始发现") {
		t.Fatal("review button clipped")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !nf.HasFocus() {
		t.Fatal("back focus lost")
	}
}
func modbusDeviceTestLoop(t *testing.T) (*UI, func()) {
	t.Helper()
	u, _ := newTestUI(t)
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("start")
	}
	return u, func() {
		u.App.QueueUpdateDraw(u.quit)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("loop did not quit")
		}
	}
}
func modbusDeviceAwaitIdle(t *testing.T, u *UI) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
		idle := false
		u.App.QueueUpdateDraw(func() { idle = len(u.localCancels) == 0 && !u.running })
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("local work did not settle")
}
func TestModbusDeviceSerialFixtureSelectionCancelAndLateResult(t *testing.T) {
	u, finish := modbusDeviceTestLoop(t)
	defer finish()
	var f *tview.Form
	var calls atomic.Int32
	u.App.QueueUpdateDraw(func() {
		v := u.inspector
		v.reset(config.Request{ID: "d", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local"})
		v.modbusDeviceForm()
		f = modbusFrontForm(t, u, "modbus-device")
		v.modbusSerialPicker(f, func(context.Context) (engine.ModbusSerialPorts, error) {
			calls.Add(1)
			return engine.ModbusSerialPorts{Names: []string{"COM7"}}, nil
		})
	})
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		table := p.(*tview.Table)
		table.Select(1, 0)
		table.GetInputCapture()(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
		if f.GetFormItem(3).(*tview.InputField).GetText() != "COM7" || u.running {
			t.Error("selection failed or connected")
		}
	})
	release := make(chan struct{})
	u.App.QueueUpdateDraw(func() {
		u.inspector.modbusSerialPicker(f, func(ctx context.Context) (engine.ModbusSerialPorts, error) {
			<-release
			return engine.ModbusSerialPorts{Names: []string{"late"}}, nil
		})
		_, p := u.pages.GetFrontPage()
		p.(*tview.Table).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	})
	close(release)
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		if name, _ := u.pages.GetFrontPage(); name != "modbus-device" || f.GetFormItem(3).(*tview.InputField).GetText() != "COM7" {
			t.Error("late result replaced form")
		}
	})
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestModbusDeviceNetworkStartRepeatCancelAndSelection(t *testing.T) {
	u, finish := modbusDeviceTestLoop(t)
	defer finish()
	plan, _ := engine.PrepareModbusDiscovery("127.0.0.1", 1502, 100, 1)
	var f *tview.Form
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	setup := func(scan func(context.Context, engine.ModbusDiscoveryPlan, func(engine.ModbusDiscoveryResult)) error) {
		u.App.QueueUpdateDraw(func() {
			v := u.inspector
			v.reset(config.Request{ID: "d", Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://127.0.0.1:502"})
			v.modbusDeviceForm()
			f = modbusFrontForm(t, u, "modbus-device")
			v.modbusNetworkForm(f)
			nf := modbusFrontForm(t, u, "modbus-network-form")
			v.modbusNetworkReview(plan, nf, f, scan)
		})
	}
	setup(func(ctx context.Context, p engine.ModbusDiscoveryPlan, emit func(engine.ModbusDiscoveryResult)) error {
		calls.Add(1)
		started <- struct{}{}
		emit(engine.ModbusDiscoveryResult{Address: "127.0.0.1", Open: true, Completed: 1, Total: 1})
		return nil
	})
	if calls.Load() != 0 {
		t.Fatal("preview called scan")
	}
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		b := p.(*tview.Flex).GetItem(2).(*tview.Form)
		modbusTestClick(b, 0)
		modbusTestClick(b, 0)
	})
	<-started
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		table := p.(*tview.Flex).GetItem(1).(*tview.Table)
		table.Select(1, 0)
		table.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		if name, _ := u.pages.GetFrontPage(); name != "modbus-device" || f.GetFormItem(2).(*tview.InputField).GetText() != "1502" || u.running {
			t.Error("open result did not fill form only")
		}
	})
	if calls.Load() != 1 {
		t.Fatal("duplicate scan", calls.Load())
	}
	setup(func(ctx context.Context, p engine.ModbusDiscoveryPlan, emit func(engine.ModbusDiscoveryResult)) error {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	u.App.QueueUpdateDraw(func() { _, p := u.pages.GetFrontPage(); modbusTestClick(p.(*tview.Flex).GetItem(2).(*tview.Form), 0) })
	<-started
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		p.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	})
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		if name, _ := u.pages.GetFrontPage(); name != "modbus-network-form" {
			t.Error("late completion reopened canceled view")
		}
	})
	setup(func(context.Context, engine.ModbusDiscoveryPlan, func(engine.ModbusDiscoveryResult)) error {
		return errors.New("fixture failure")
	})
	u.App.QueueUpdateDraw(func() { _, p := u.pages.GetFrontPage(); modbusTestClick(p.(*tview.Flex).GetItem(2).(*tview.Form), 0) })
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		if !strings.Contains(p.(*tview.Flex).GetTitle(), "fixture failure") {
			t.Error("error missing")
		}
	})
}
func TestModbusDeviceRotationDirtySavesEndpointWithoutMarkers(t *testing.T) {
	u, v, r := modbusTestView(t)
	active, e := u.collection.Resolve(r, u.profile)
	if e != nil {
		t.Fatal(e)
	}
	v.reset(active)
	changes, e := v.modbusUnsavedLayout()
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := changes["__device_endpoint"]; ok {
		t.Fatal("unchanged template marked dirty")
	}
	active = modbusReadRequest(active)
	active.Endpoint = "mock://local"
	active.Timeout = "29s"
	active.Params["request_timeout_ms"] = 1234
	v.reset(active)
	changes, e = v.modbusUnsavedLayout()
	if e != nil || changes["__device_endpoint"] != "mock://local" || changes["__device_timeout"] != "29s" {
		t.Fatal(changes, e)
	}
	if e = v.modbusSaveParams(changes); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(u.raw), "__device_") {
		t.Fatal("internal marker serialized")
	}
	saved, e := v.modbusSavedRequest()
	if e != nil || saved.Endpoint != "mock://local" || saved.Timeout != "29s" || saved.Int("request_timeout_ms", 0) != 1234 {
		t.Fatal(saved, e)
	}
}
