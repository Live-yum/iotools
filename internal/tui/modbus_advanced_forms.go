package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (v *inspector) modbusColumnsPanel() {
	if v.modbus == nil {
		return
	}
	order := append([]string{}, v.modbus.columns...)
	if len(order) == 0 {
		for _, c := range modbusColumns[:13] {
			order = append(order, c.key)
		}
	}
	visible := map[string]bool{}
	for _, key := range order {
		visible[key] = true
	}
	for _, c := range modbusColumns {
		if !visible[c.key] {
			order = append(order, c.key)
		}
	}
	widths := map[string]int{}
	for key, value := range v.modbus.widths {
		widths[key] = value
	}
	hex := v.modbus.hexAddress
	table := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	table.SetBorder(true)
	search := tview.NewInputField().SetLabel("筛选列名称: ")
	var render func()
	render = func() {
		row, _ := table.GetSelection()
		table.Clear()
		mode := "十进制"
		if hex {
			mode = "十六进制"
		}
		table.SetTitle(" 列设置 · Space显隐 </>排序 w宽度 a地址" + mode + " · Tab切换 · Esc取消 ")
		for col, text := range []string{"显示", "列名", "键", "宽度"} {
			table.SetCell(0, col, tview.NewTableCell(text).SetSelectable(false).SetTextColor(tcell.ColorAqua))
		}
		n := 1
		query := strings.ToLower(search.GetText())
		for _, key := range order {
			if query != "" && !strings.Contains(strings.ToLower(key+" "+modbusColumnLabel(key)), query) {
				continue
			}
			shown := ""
			if visible[key] {
				shown = "✓"
			}
			width := "自动"
			if widths[key] > 0 {
				width = strconv.Itoa(widths[key])
			}
			for col, text := range []string{shown, modbusColumnLabel(key), key, width} {
				table.SetCell(n, col, tview.NewTableCell(display(text)).SetReference(key))
			}
			n++
		}
		if row < 1 {
			row = 1
		}
		if row >= n {
			row = n - 1
		}
		if row > 0 {
			table.Select(row, 0)
		}
	}
	search.SetChangedFunc(func(string) { render() })
	close := func() { v.owner.pages.RemovePage("modbus-columns"); v.owner.App.SetFocus(v.table) }
	apply := func(save bool) {
		columns := []any{}
		rawWidths := map[string]any{}
		for _, key := range order {
			if visible[key] {
				columns = append(columns, key)
			}
		}
		for key, width := range widths {
			rawWidths[key] = width
		}
		mode := "decimal"
		if hex {
			mode = "hex"
		}
		cfg := map[string]any{"visible": columns, "widths": rawWidths, "address_mode": mode, "time_mode": v.modbus.timeMode}
		cols, parsedWidths, hex, err := modbusParseColumns(cfg)
		if err != nil {
			table.SetTitle("无法应用：" + display(err.Error()))
			return
		}
		if save {
			if err = v.modbusSaveParams(map[string]any{"columns": cfg}); err != nil {
				table.SetTitle("保存失败：" + display(err.Error()))
				return
			}
		}
		v.modbus.columns = cols
		v.modbus.widths = parsedWidths
		v.modbus.hexAddress = hex
		close()
		v.renderRegisters()
		if save {
			v.owner.setStatus("列顺序/显隐/宽度已保存到请求")
		}
	}
	buttons := tview.NewForm().AddButton("临时应用", func() { apply(false) }).AddButton("保存到请求", func() { apply(true) }).AddButton("取消", close)
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() != tcell.KeyRune {
			return e
		}
		row, _ := table.GetSelection()
		if row < 1 || row >= table.GetRowCount() {
			return e
		}
		key, _ := table.GetCell(row, 0).GetReference().(string)
		if key == "" {
			return e
		}
		switch e.Rune() {
		case ' ':
			visible[key] = !visible[key]
			render()
			return nil
		case '<', '>':
			index := 0
			for i, k := range order {
				if k == key {
					index = i
					break
				}
			}
			next := index - 1
			if e.Rune() == '>' {
				next = index + 1
			}
			if next >= 0 && next < len(order) {
				order[index], order[next] = order[next], order[index]
				render()
				for i := 1; i < table.GetRowCount(); i++ {
					if table.GetCell(i, 0).GetReference() == key {
						table.Select(i, 0)
						break
					}
				}
			}
			return nil
		case 'a':
			hex = !hex
			render()
			return nil
		case 'w':
			initial := ""
			if widths[key] > 0 {
				initial = strconv.Itoa(widths[key])
			}
			form := tview.NewForm().AddInputField("宽度1..120(空=自动)", initial, 10, nil, nil)
			end := func() { v.owner.pages.RemovePage("modbus-column-width"); v.owner.App.SetFocus(table) }
			form.AddButton("应用", func() {
				text := form.GetFormItem(0).(*tview.InputField).GetText()
				if text == "" {
					delete(widths, key)
				} else {
					n, err := strconv.Atoi(text)
					if err != nil || n < 1 || n > 120 {
						form.SetTitle("宽度必须1..120")
						return
					}
					widths[key] = n
				}
				end()
				render()
			}).AddButton("取消", end).SetCancelFunc(end)
			form.SetBorder(true).SetTitle(" " + display(key) + "列宽 ")
			v.owner.pages.AddPage("modbus-column-width", form, true, true)
			v.owner.App.SetFocus(form)
			return nil
		}
		return e
	})
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(search, 1, 0, false).AddItem(table, 0, 1, true).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(v.modbusPanelKeys(close, []tview.Primitive{table, search}, buttons))
	render()
	v.owner.pages.AddPage("modbus-columns", panel, true, true)
	v.owner.App.SetFocus(table)
}
func (v *inspector) modbusKeymapForm() {
	if v.modbus == nil {
		return
	}
	actions := []string{}
	for action := range modbusDefaultKeys {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	form := tview.NewForm()
	for _, action := range actions {
		form.AddInputField(modbusActionLabels[action]+" ("+action+")", string(v.modbus.keymap[action]), 3, nil, nil)
	}
	close := func() { v.owner.pages.RemovePage("modbus-keymap"); v.owner.App.SetFocus(v.table) }
	form.AddButton("验证并保存", func() {
		mapping := map[string]any{}
		for i, action := range actions {
			mapping[action] = form.GetFormItem(i).(*tview.InputField).GetText()
		}
		keys, err := modbusParseKeymap(mapping)
		if err != nil {
			form.SetTitle("快捷键无效：" + display(err.Error()))
			return
		}
		if err = v.modbusSaveParams(map[string]any{"keymap": mapping}); err != nil {
			form.SetTitle("保存失败：" + display(err.Error()))
			return
		}
		v.modbus.keymap = keys
		close()
		v.owner.setStatus("Modbus快捷键已保存；Ctrl-C/F8/Esc及弹窗操作保持安全边界")
	}).AddButton("恢复默认", func() {
		for i, action := range actions {
			form.GetFormItem(i).(*tview.InputField).SetText(string(modbusDefaultKeys[action]))
		}
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" Modbus表格快捷键 · 单字符 · 禁止重复/q/?/+/- · Esc取消 ")
	v.owner.pages.AddPage("modbus-keymap", form, true, true)
	v.owner.App.SetFocus(form)
}
func (v *inspector) modbusAdvancedHelp() string {
	if v.modbus == nil {
		return ""
	}
	actions := []string{"columns", "keymap", "import", "export", "dump", "more"}
	parts := []string{}
	for _, action := range actions {
		parts = append(parts, fmt.Sprintf("%c %s", v.modbus.keymap[action], modbusActionLabels[action]))
	}
	return strings.Join(parts, " · ")
}
