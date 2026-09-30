package tui

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"sort"
	"strconv"
	"strings"
)

func (u *UI) modbusLocalWork(cancel context.CancelFunc) uint64 {
	if u.localCancels == nil {
		u.localCancels = map[uint64]context.CancelFunc{}
	}
	u.nextLocalWork++
	u.localCancels[u.nextLocalWork] = cancel
	return u.nextLocalWork
}
func (u *UI) modbusFinishLocalWork(id uint64) bool {
	delete(u.localCancels, id)
	if u.quitting && !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
		u.App.Stop()
		return false
	}
	return true
}
func (v *inspector) modbusSerialPicker(previous *tview.Form, list func(context.Context) (engine.ModbusSerialPorts, error)) {
	u := v.owner
	if u.modbusRotationBusy() {
		u.setStatus("请先停止请求和发现")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := u.modbusLocalWork(cancel)
	open := true
	table := tview.NewTable().SetSelectable(true, false)
	table.SetBorder(true).SetTitle(" 只读枚举本机串口元数据 · 不打开端口 · Esc取消 ")
	close := func() { open = false; cancel(); u.pages.RemovePage("modbus-serial-ports"); u.App.SetFocus(previous) }
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyF8:
			cancel()
			table.SetTitle("已取消枚举 · Esc返回")
			return nil
		case tcell.KeyEnter:
			row, _ := table.GetSelection()
			name, ok := table.GetCell(row, 0).GetReference().(string)
			if ok && name != "" {
				previous.GetFormItem(3).(*tview.InputField).SetText(name)
				previous.GetFormItem(0).(*tview.DropDown).SetCurrentOption(1)
				close()
			}
			return nil
		}
		return e
	})
	u.pages.AddPage("modbus-serial-ports", table, true, true)
	u.App.SetFocus(table)
	go func() {
		ports, err := list(ctx)
		u.App.QueueUpdateDraw(func() {
			if !u.modbusFinishLocalWork(id) || !open || ctx.Err() != nil {
				return
			}
			if err != nil {
				table.SetTitle("枚举失败：" + display(err.Error()))
				return
			}
			table.SetCell(0, 0, tview.NewTableCell("选择路径仅填入表单；仍需预览/明确读取").SetSelectable(false))
			for i, n := range ports.Names {
				table.SetCell(i+1, 0, tview.NewTableCell(display(n)).SetReference(n))
			}
			if len(ports.Names) > 0 {
				table.Select(1, 0)
			} else {
				table.SetTitle("未找到串口 · 可返回手动输入本机路径")
			}
			if ports.Truncated {
				table.SetTitle("串口元数据已达数量上限 · 请手动输入未列出的路径")
			}
		})
		cancel()
	}()
}
func (v *inspector) modbusNetworkForm(previous *tview.Form) {
	f := tview.NewForm().SetItemPadding(0).AddInputField("明确IP列表或IPv4网段", previous.GetFormItem(1).(*tview.InputField).GetText(), 48, nil, nil).AddInputField("TCP端口", previous.GetFormItem(2).(*tview.InputField).GetText(), 8, nil, nil).AddInputField("每目标超时ms(100..2000)", "500", 8, nil, nil).AddInputField("并发(1..32)", "8", 8, nil, nil)
	f.AddDropDown("发现方式", []string{"TCP端口", "ICMP Ping (仅IPv4)"}, 0, nil)
	close := func() { v.owner.pages.RemovePage("modbus-network-form"); v.owner.App.SetFocus(previous) }
	f.AddButton("展开目标并预览", func() {
		nums := []int{}
		for _, i := range []int{1, 2, 3} {
			n, e := strconv.Atoi(f.GetFormItem(i).(*tview.InputField).GetText())
			if e != nil {
				f.SetTitle("参数必须为整数")
				return
			}
			nums = append(nums, n)
		}
		methodIndex, _ := f.GetFormItem(4).(*tview.DropDown).GetCurrentOption()
		method := "tcp"
		if methodIndex == 1 {
			method = "ping"
		}
		plan, err := engine.PrepareModbusDiscoveryMethod(f.GetFormItem(0).(*tview.InputField).GetText(), nums[0], nums[1], nums[2], method)
		if err != nil {
			f.SetTitle(display(err.Error()))
			return
		}
		v.modbusNetworkReview(plan, f, previous, engine.DiscoverModbusNetwork)
	}).AddButton("返回", close).SetCancelFunc(close)
	f.SetBorder(true).SetTitle(" 网络发现方式 · 不发送Modbus载荷 · 不自动探测 ")
	f.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	v.owner.pages.AddPage("modbus-network-form", f, true, true)
	v.owner.App.SetFocus(f)
}
func (v *inspector) modbusNetworkReview(plan engine.ModbusDiscoveryPlan, previous, device *tview.Form, scan func(context.Context, engine.ModbusDiscoveryPlan, func(engine.ModbusDiscoveryResult)) error) {
	u := v.owner
	plan.Targets = append([]string(nil), plan.Targets...)
	ctx, cancel := context.WithCancel(context.Background())
	open, busy, started := true, false, false
	view := tview.NewTextView().SetDynamicColors(false).SetWrap(true)
	view.SetText(fmt.Sprintf("明确目标数量：%d\n端口：%d / TCP connect only\n每目标超时：%dms；并发：%d；总时限：120s\n不发送协议载荷、不重试、不解析DNS。开放端口不能证明是Modbus设备。\n\n全部目标：\n%s", len(plan.Targets), plan.Port, plan.TimeoutMS, plan.Concurrency, strings.Join(plan.Targets, "\n")))
	methodName := "TCP"
	if plan.Method == "ping" {
		methodName = "ICMP Ping"
		view.SetText(fmt.Sprintf("明确目标数量：%d\n方式：ICMP Echo (IPv4)；不探测TCP端口\n每目标超时：%dms；并发：%d；总时限：120s\n仅非特权套接字/Windows系统API；不可用时停止，不提权。每目标一个32字节随机Echo载荷，无重试。来源/ID或系统API关联/nonce校验。可达不证明Modbus或TCP端口开放。Windows已发探测最多等当前超时后停止。\n\n全部目标：\n%s", len(plan.Targets), plan.TimeoutMS, plan.Concurrency, strings.Join(plan.Targets, "\n")))
	}
	results := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	found := map[string]engine.ModbusDiscoveryResult{}
	render := func() {
		results.Clear()
		for c, s := range []string{"IP", "探测状态"} {
			results.SetCell(0, c, tview.NewTableCell(s).SetSelectable(false))
		}
		ips := []string{}
		for ip := range found {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		for i, ip := range ips {
			r := found[ip]
			status := "未确认可达: " + r.Error
			if r.Open {
				status = "端口开放；未验证Modbus"
				if plan.Method == "ping" {
					status = "ICMP可达；未验证端口/Modbus"
				}
			}
			results.SetCell(i+1, 0, tview.NewTableCell(ip).SetReference(r))
			results.SetCell(i+1, 1, tview.NewTableCell(display(status)).SetReference(r))
		}
		if len(ips) > 0 {
			results.Select(1, 0)
		}
	}
	close := func() { open = false; cancel(); u.pages.RemovePage("modbus-network-review"); u.App.SetFocus(previous) }
	var panel *tview.Flex
	buttons := tview.NewForm()
	buttons.AddButton("确认开始发现", func() {
		if started || busy {
			return
		}
		if ctx.Err() != nil {
			panel.SetTitle("预览已停止；请返回重新审查目标")
			return
		}
		if u.modbusRotationBusy() {
			panel.SetTitle("请先停止其他请求或发现")
			return
		}
		started, busy = true, true
		id := u.modbusLocalWork(cancel)
		panel.SetTitle(methodName + "发现中 · F8停止 Esc取消并返回")
		go func() {
			err := scan(ctx, plan, func(r engine.ModbusDiscoveryResult) {
				u.App.QueueUpdateDraw(func() {
					if !open || ctx.Err() != nil {
						return
					}
					if len(found) < 254 {
						found[r.Address] = r
					}
					render()
					panel.SetTitle(fmt.Sprintf("%s发现 %d/%d · F8停止 · Enter填入已确认目标", methodName, r.Completed, r.Total))
				})
			})
			u.App.QueueUpdateDraw(func() {
				busy = false
				if !u.modbusFinishLocalWork(id) || !open {
					return
				}
				if err != nil {
					panel.SetTitle("发现停止：" + display(err.Error()) + " · 可选择已完成已确认目标")
				} else {
					panel.SetTitle("发现完成 · Enter填入已确认目标 · 不自动连接")
				}
			})
			cancel()
		}()
	}).AddButton("停止", func() { cancel() }).AddButton("返回", close)
	panel = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, false).AddItem(results, 0, 1, false).AddItem(buttons, 3, 0, true)
	panel.SetBorder(true).SetTitle(" 明确审查网络目标 · 确认前零探测 ")
	results.SetSelectedFunc(func(row, col int) {
		r, ok := results.GetCell(row, col).GetReference().(engine.ModbusDiscoveryResult)
		if !ok || !r.Open {
			return
		}
		if busy {
			panel.SetTitle("请先停止发现并等待完成，再选用目标")
			return
		}
		device.GetFormItem(1).(*tview.InputField).SetText(r.Address)
		if plan.Method != "ping" {
			device.GetFormItem(2).(*tview.InputField).SetText(strconv.Itoa(plan.Port))
		}
		open = false
		cancel()
		u.pages.RemovePage("modbus-network-review")
		u.pages.RemovePage("modbus-network-form")
		u.App.SetFocus(device)
	})
	panel.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			cancel()
			return nil
		}
		if e.Key() == tcell.KeyTab {
			switch u.App.GetFocus() {
			case view:
				u.App.SetFocus(results)
			case results:
				u.App.SetFocus(buttons)
			default:
				u.App.SetFocus(view)
			}
			return nil
		}
		return e
	})
	u.pages.AddPage("modbus-network-review", panel, true, true)
	u.App.SetFocus(buttons)
}
