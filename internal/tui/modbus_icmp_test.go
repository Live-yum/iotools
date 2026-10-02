package tui

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
)

func TestModbusPingFormExplicitReviewAndIPv6Rejection(t *testing.T) {
	u, screen := newTestUI(t)
	v := u.inspector
	v.reset(config.Request{ID: "ping", Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://127.0.0.1:1502"})
	screen.SetSize(80, 24)
	v.modbusDeviceForm()
	f := modbusFrontForm(t, u, "modbus-device")
	v.modbusNetworkForm(f)
	nf := modbusFrontForm(t, u, "modbus-network-form")
	nf.GetFormItem(4).(*tview.DropDown).SetCurrentOption(1)
	nf.GetFormItem(0).(*tview.InputField).SetText("::1")
	modbusTestClick(nf, 0)
	if name, _ := u.pages.GetFrontPage(); name != "modbus-network-form" {
		t.Fatal("IPv6 Ping accepted")
	}
	nf.GetFormItem(0).(*tview.InputField).SetText("127.0.0.1")
	modbusTestClick(nf, 0)
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	text := panel.GetItem(0).(*tview.TextView).GetText(false)
	for _, want := range []string{"ICMP Echo", "不探测TCP端口", "32字节", "不提权", "数量：1", "并发：8", "500ms", "127.0.0.1"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
	if len(u.localCancels) != 0 || u.running {
		t.Fatal("review probed")
	}
	u.App.ForceDraw()
	if !strings.Contains(strings.ReplaceAll(snapshot(screen), " ", ""), "确认开始发现") {
		t.Fatal("start button clipped")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if name, _ := u.pages.GetFrontPage(); name != "modbus-network-form" {
		t.Fatal("cancel")
	}
}
func TestModbusPingUIUnavailableAndSelectionRetainsTCPPort(t *testing.T) {
	u, finish := modbusDeviceTestLoop(t)
	defer finish()
	plan, _ := engine.PrepareModbusDiscoveryMethod("127.0.0.1", 502, 100, 1, "ping")
	var device *tview.Form
	setup := func(scan func(context.Context, engine.ModbusDiscoveryPlan, func(engine.ModbusDiscoveryResult)) error) {
		u.App.QueueUpdateDraw(func() {
			v := u.inspector
			v.reset(config.Request{ID: "ping", Protocol: "modbus", Action: "read-holding", Endpoint: "tcp://127.0.0.1:1502"})
			v.modbusDeviceForm()
			device = modbusFrontForm(t, u, "modbus-device")
			v.modbusNetworkForm(device)
			nf := modbusFrontForm(t, u, "modbus-network-form")
			v.modbusNetworkReview(plan, nf, device, scan)
			_, p := u.pages.GetFrontPage()
			modbusTestClick(p.(*tview.Flex).GetItem(2).(*tview.Form), 0)
		})
	}
	setup(func(context.Context, engine.ModbusDiscoveryPlan, func(engine.ModbusDiscoveryResult)) error {
		return engine.ErrModbusICMPUnavailable
	})
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		if !strings.Contains(p.(*tview.Flex).GetTitle(), "ICMP Ping不可用") {
			t.Error("unavailable hidden")
		}
		if p.(*tview.Flex).GetItem(1).(*tview.Table).GetRowCount() != 0 {
			t.Error("invented unavailable results")
		}
	})
	setup(func(ctx context.Context, p engine.ModbusDiscoveryPlan, emit func(engine.ModbusDiscoveryResult)) error {
		if p.Method != "ping" {
			t.Error("method lost")
		}
		emit(engine.ModbusDiscoveryResult{Address: "127.0.0.1", Open: true, Completed: 1, Total: 1})
		return nil
	})
	modbusDeviceAwaitIdle(t, u)
	u.App.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		table := p.(*tview.Flex).GetItem(1).(*tview.Table)
		if !strings.Contains(table.GetCell(1, 1).Text, "ICMP可达") || strings.Contains(table.GetCell(1, 1).Text, "端口开放") {
			t.Error("ping claimed open port")
		}
		table.Select(1, 0)
		table.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		if device.GetFormItem(2).(*tview.InputField).GetText() != "1502" || u.running {
			t.Error("Ping result changed TCP port or connected")
		}
	})
}
