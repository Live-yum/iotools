package tui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"go.yaml.in/yaml/v3"
)

// Sample times come from the engine event (response receipt), not export time
// or a device clock. Copy rows so UI metadata never changes engine event data.
func (v *inspector) modbusMoreEvent(e engine.Event) engine.Event {
	if v.protocol != "modbus" || v.modbus == nil {
		return e
	}
	at := e.Time
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var rows []map[string]any
	switch e.Kind {
	case "registers":
		original, ok := e.Data.([]map[string]any)
		if !ok || len(original) > 65536 {
			return e
		}
		for _, row := range original {
			address, ok := row["address"].(int)
			if !ok || address < 0 || address > 65535 {
				continue
			}
			copy := map[string]any{}
			for key, value := range row {
				copy[key] = value
			}
			copy["sampled_at"] = at
			rows = append(rows, copy)
		}
	case "bits":
		m, ok := e.Data.(map[string]any)
		if !ok {
			return e
		}
		address, ok := m["address"].(int)
		if !ok {
			return e
		}
		bits, ok := m["values"].([]bool)
		if !ok || len(bits) > 2000 || address < 0 || address+len(bits) > 65536 {
			return e
		}
		for i, bit := range bits {
			n := uint16(0)
			if bit {
				n = 1
			}
			a := address + i
			rows = append(rows, map[string]any{"address": a, "u16": n, "i16": int16(n), "hex": fmt.Sprintf("0x%04X", n), "binary": fmt.Sprintf("%016b", n), "bit": bit, "sampled_at": at, "label": v.labels[a], "pinned": v.pins[a]})
		}
	default:
		return e
	}
	e.Kind = "registers"
	e.Data = rows
	return e
}
func modbusSampleTime(row map[string]any, mode string, now time.Time) string {
	at, ok := row["sampled_at"].(time.Time)
	if !ok || at.IsZero() {
		return ""
	}
	if mode != "ago" {
		return at.UTC().Format("15:04:05.000")
	}
	d := now.Sub(at)
	if d < time.Second {
		return "刚刚"
	}
	if d < time.Minute {
		return fmt.Sprintf("%d秒前", int(d/time.Second))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d分钟前", int(d/time.Minute))
	}
	return fmt.Sprintf("%d小时前", int(d/time.Hour))
}
func (v *inspector) modbusMoreMenu() {
	list := tview.NewList().ShowSecondaryText(true)
	close := func() { v.owner.pages.RemovePage("modbus-more"); v.owner.App.SetFocus(v.table) }
	list.AddItem("地址/标签跳转与读取设置", "/定位；R预览空间、地址、unit、窗口、字序与采样；不自动连接", 'r', func() { close(); v.modbusReadForm(nil) })
	list.AddItem("寄存器详情/字段图", "v详情 g图表；同响应多词与规则，禁止混合旧值", 'v', func() { close(); v.modbusInspect(false) })
	list.AddItem("类型/线圈写入编辑", "原始词预览后再次核对目标；只读模式与运行中禁止", 'e', func() { close(); v.modbusWriteForm() })
	list.AddItem("结构化规则编辑 / 标注面板", "c规则、P标签/规则；同响应预览，明确保存/删除", 'k', func() { close(); v.modbusRuleForm(-1) })
	list.AddItem("设备标识", "i选择FC43/14访问级别及对象，明确读取", 'd', func() { close(); v.modbusDeviceIDForm() })
	list.AddItem("原始PDU请求", "j验证功能码/范围/十六进制；写入仍需确认", 'j', func() { close(); v.modbusRawForm() })
	list.AddItem("有界全空间扫描", "B四空间区间与有限循环，F8取消", 'b', func() { close(); v.modbusRangeForm(false) })
	list.AddItem("明确单元探测", "U最多32单元、四空间、首成功停止和结果选择", 'u', func() { close(); v.modbusRangeForm(true) })
	list.AddItem("导入完整MTUI配置", "只生成四种只读请求；预览后明确保存；不会连接或启用API", 'i', func() { close(); v.modbusConfigImportForm() })
	list.AddItem("CSV快照比较", "读取MTUI/原生CSV，与本次已收到数据对比；不自动读取设备", 'c', func() { close(); v.modbusCSVDiffForm() })
	list.AddItem("采样时间显示", "绝对UTC/相对时间；接收响应时刻，非设备时钟", 't', func() { close(); v.modbusTimeForm() })
	list.AddItem("写日志查看", "本机JSONL尝试/结果记录；只读打开，不执行设备动作", 'w', func() { close(); v.modbusWriteLogForm() })
	list.AddItem("通信统计", "读写成功/失败、延迟、最后错误与本机清空", 's', func() { close(); v.owner.modbusStatsPanel() })
	list.AddItem("活动日志", "独立有界日志、跟随/换行/明确复制导出", 'a', func() { close(); v.owner.modbusActivityPanel() })
	list.AddItem("轮换本机集合", "next_config或返回初始集合；预览后切换，不自动连接", 'n', func() { close(); v.owner.modbusRotationForm() })
	list.AddItem("关闭", "Esc返回", 0, close)
	list.SetBorder(true).SetTitle(" Modbus更多 · I标注 C列 D CSV · Esc返回 ")
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		return e
	})
	v.owner.pages.AddPage("modbus-more", list, true, true)
	v.owner.App.SetFocus(list)
}
func (v *inspector) modbusConfigImportForm() {
	if v.owner.running {
		v.owner.modal("请先停止采样再导入配置")
		return
	}
	form := tview.NewForm().AddInputField("JSON文件(空则使用粘贴)", "", 70, nil, nil).AddInputField("新请求ID前缀", "mtui", 30, nil, nil).AddTextArea("粘贴完整MTUI JSON", "", 75, 10, 4<<20, nil).AddInputField("可选本机写日志文件(空=关闭)", "", 70, nil, nil).AddCheckbox("明确启用此路径的后续写操作日志", false, nil)
	close := func() { v.owner.pages.RemovePage("modbus-config-import"); v.owner.App.SetFocus(v.table) }
	form.AddButton("只读预览", func() {
		data := []byte(form.GetFormItem(2).(*tview.TextArea).GetText())
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		source := "粘贴文本"
		var err error
		if path != "" {
			data, err = modbusReadImport(path)
			source = path
		}
		if err != nil {
			form.SetTitle("读取失败：" + display(err.Error()))
			return
		}
		plan, err := engine.ImportMTUIConfig(data, form.GetFormItem(1).(*tview.InputField).GetText())
		if err != nil {
			form.SetTitle("导入失败：" + display(err.Error()))
			return
		}
		logPath, err := modbusReviewedLogPath(form.GetFormItem(3).(*tview.InputField).GetText(), form.GetFormItem(4).(*tview.Checkbox).IsChecked())
		if err != nil {
			form.SetTitle("日志设置：" + display(err.Error()))
			return
		}
		if logPath != "" {
			for i := range plan.Requests {
				plan.Requests[i].Params["write_log_file"] = logPath
			}
			plan.Warnings = append(plan.Warnings, "用户另行明确选择写日志文件："+logPath+"；导入/读取不创建文件，仅后续明确写操作使用")
		}
		if _, err = engine.AppendMTUIRequests(v.owner.raw, plan.Requests); err != nil {
			form.SetTitle("无法追加：" + display(err.Error()))
			return
		}
		close()
		v.modbusConfigPreview(plan, source)
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" 完整配置导入 · 禁止自动连接/写入/API · Esc取消 ")
	v.owner.pages.AddPage("modbus-config-import", form, true, true)
	v.owner.App.SetFocus(form)
}
func (v *inspector) modbusConfigPreview(plan engine.MTUIConfigPlan, source string) {
	u := v.owner
	body, _ := yaml.Marshal(plan.Requests)
	text := "来源：" + source + "\n\n转换说明（原始JSON及未知字段的值不会保存）：\n- " + strings.Join(plan.Warnings, "\n- ") + "\n\n将追加的只读请求：\n" + string(body)
	view := tview.NewTextView().SetText(clean(text)).SetScrollable(true).SetWrap(true)
	view.SetBorder(true).SetTitle(" 完整配置转换预览 · 四个新ID · Tab到按钮 · Esc取消 ")
	close := func() { u.pages.RemovePage("modbus-config-preview"); u.App.SetFocus(v.table) }
	buttons := tview.NewForm().AddButton("明确保存到集合", func() {
		if err := v.modbusSaveConfigPlan(plan); err != nil {
			view.SetTitle("保存失败：" + display(err.Error()))
			return
		}
		close()
		u.setStatus("已追加四个只读请求；未运行。选择后按F5才会连接，API/写权限未启用")
	}).AddButton("取消", close)
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(v.modbusPanelKeys(close, []tview.Primitive{view}, buttons))
	u.pages.AddPage("modbus-config-preview", panel, true, true)
	u.App.SetFocus(view)
}
func (v *inspector) modbusSaveConfigPlan(plan engine.MTUIConfigPlan) error {
	u := v.owner
	if u.running {
		return fmt.Errorf("请先停止当前请求")
	}
	out, err := engine.AppendMTUIRequests(u.raw, plan.Requests)
	if err != nil {
		return err
	}
	current, err := modbusReadImport(u.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, u.raw) {
		return fmt.Errorf("配置已被外部修改，拒绝覆盖")
	}
	parsed, err := config.Parse(out)
	if err != nil {
		return err
	}
	if err = config.Save(u.path, out); err != nil {
		return err
	}
	parsed.SourcePath = u.path
	u.collection = parsed
	u.raw = out
	u.populate(u.search.GetText())
	return nil
}
func (v *inspector) modbusTimeForm() {
	if v.modbus == nil {
		return
	}
	mode := v.modbus.timeMode
	if mode == "" {
		mode = "read_at"
	}
	form := tview.NewForm().AddDropDown("采样时间", []string{"绝对UTC（时:分:秒.毫秒）", "相对当前时刻"}, 0, nil)
	if mode == "ago" {
		form.GetFormItem(0).(*tview.DropDown).SetCurrentOption(1)
	}
	close := func() { v.owner.pages.RemovePage("modbus-time"); v.owner.App.SetFocus(v.table) }
	apply := func(save bool) {
		choice, _ := form.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
		mode := "read_at"
		if choice == 1 {
			mode = "ago"
		}
		cols := append([]string{}, v.modbus.columns...)
		if len(cols) == 0 {
			for _, c := range modbusColumns[:13] {
				cols = append(cols, c.key)
			}
		}
		found := false
		visible := []any{}
		for _, c := range cols {
			if c == "time" {
				found = true
			}
			visible = append(visible, c)
		}
		if !found {
			cols = append(cols, "time")
			visible = append(visible, "time")
		}
		widths := map[string]any{}
		for k, w := range v.modbus.widths {
			widths[k] = w
		}
		address := "decimal"
		if v.modbus.hexAddress {
			address = "hex"
		}
		if save {
			if err := v.modbusSaveParams(map[string]any{"columns": map[string]any{"visible": visible, "widths": widths, "address_mode": address, "time_mode": mode}}); err != nil {
				form.SetTitle("保存失败：" + display(err.Error()))
				return
			}
		}
		v.modbus.timeMode = mode
		v.modbus.columns = cols
		close()
		v.renderRegisters()
	}
	form.AddButton("临时应用", func() { apply(false) }).AddButton("保存到请求", func() { apply(true) }).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" 采样时间 · 相对时间在收到数据/重绘时更新 · Esc取消 ")
	v.owner.pages.AddPage("modbus-time", form, true, true)
	v.owner.App.SetFocus(form)
}
func (v *inspector) modbusCSVDiffForm() {
	form := tview.NewForm().AddInputField("CSV文件(空则粘贴)", "", 70, nil, nil).AddDropDown("无0x前缀的地址模式", []string{"十进制", "十六进制"}, 0, nil).AddTextArea("粘贴CSV", "", 75, 12, 4<<20, nil).AddCheckbox("已核对CSV与当前设备/单元一致", false, nil)
	if v.modbus != nil && v.modbus.hexAddress {
		form.GetFormItem(1).(*tview.DropDown).SetCurrentOption(1)
	}
	close := func() { v.owner.pages.RemovePage("modbus-csv-import"); v.owner.App.SetFocus(v.table) }
	form.AddButton("离线比较", func() {
		if !form.GetFormItem(3).(*tview.Checkbox).IsChecked() {
			form.SetTitle("CSV没有设备/单元信息，请先核对并勾选")
			return
		}
		data := []byte(form.GetFormItem(2).(*tview.TextArea).GetText())
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		var err error
		if path != "" {
			data, err = modbusReadImport(path)
		}
		if err != nil {
			form.SetTitle("读取失败：" + display(err.Error()))
			return
		}
		mode, _ := form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
		before, err := engine.ParseMTUICSV(data, mode == 1)
		if err != nil {
			form.SetTitle("CSV无效：" + display(err.Error()))
			return
		}
		close()
		v.modbusCSVDiffPanel(before)
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" CSV快照 · 只比较已读数据 · 未读不等于删除 · Esc取消 ")
	v.owner.pages.AddPage("modbus-csv-import", form, true, true)
	v.owner.App.SetFocus(form)
}
func modbusCSVType(action string) string {
	switch action {
	case "read-holding", "sweep-holding", "search-holding", "read-write-registers":
		return "holding"
	case "read-input", "sweep-input":
		return "input"
	case "read-coils", "sweep-coils":
		return "coil"
	case "read-discrete", "sweep-discrete":
		return "discrete"
	}
	return ""
}
func (v *inspector) modbusCSVDiffPanel(before map[engine.ModbusCSVCell]engine.ModbusCSVValue) {
	current := map[engine.ModbusCSVCell]uint16{}
	kind := modbusCSVType(v.modbus.request.Action)
	for address, row := range v.values {
		if value, ok := row["u16"].(uint16); ok {
			current[engine.ModbusCSVCell{Type: kind, Address: address}] = value
		}
	}
	differences := engine.DiffMTUICSV(before, current)
	table := tview.NewTable().SetFixed(1, 0).SetSelectable(true, false)
	table.SetBorder(true)
	changedOnly := false
	render := func() {
		table.Clear()
		same, changed, unread := 0, 0, 0
		for col, name := range []string{"状态", "类型", "地址", "CSV原值", "当前已读值", "CSV时间"} {
			table.SetCell(0, col, tview.NewTableCell(name).SetSelectable(false).SetTextColor(tcell.ColorAqua))
		}
		row := 1
		for _, diff := range differences {
			mark, next := "未读", ""
			if diff.After == nil {
				unread++
			} else {
				next = strconv.Itoa(int(*diff.After))
				if *diff.After == diff.Before {
					mark = "相同"
					same++
				} else {
					mark = "变化"
					changed++
				}
			}
			if (changedOnly && mark != "变化") || row > 2000 {
				continue
			}
			for col, text := range []string{mark, diff.Cell.Type, strconv.Itoa(diff.Cell.Address), strconv.Itoa(int(diff.Before)), next, diff.Time} {
				table.SetCell(row, col, tview.NewTableCell(display(text)))
			}
			row++
		}
		mode := "全部"
		if changedOnly {
			mode = "仅变化"
		}
		table.SetTitle(fmt.Sprintf(" CSV离线比较 · 相同%d 变化%d 未读%d · f切换(%s) · 最多显示2000行 Esc返回 ", same, changed, unread, mode))
	}
	close := func() { v.owner.pages.RemovePage("modbus-csv-diff"); v.owner.App.SetFocus(v.table) }
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyRune && e.Rune() == 'f' {
			changedOnly = !changedOnly
			render()
			return nil
		}
		return e
	})
	render()
	v.owner.pages.AddPage("modbus-csv-diff", table, true, true)
	v.owner.App.SetFocus(table)
}
