package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func modbusRuleJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func modbusRuleDecode(text string, value any) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if len(text) > 8192 {
		return fmt.Errorf("JSON字段最多8192字节")
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("只允许一个JSON值")
	}
	return nil
}
func modbusRulesParam(rules []engine.RegisterRule) []any {
	b, _ := json.Marshal(rules)
	var out []any
	// All numeric rule fields are bounded addresses/indices/decimals; enum keys
	// and operations stay strings, so generic JSON numbers remain exact here.
	_ = json.Unmarshal(b, &out)
	return out
}
func modbusRuleReplace(r config.Request, address int, rule *engine.RegisterRule) ([]any, error) {
	rules, err := engine.ModbusRules(r)
	if err != nil {
		return nil, err
	}
	out := []engine.RegisterRule{}
	for _, old := range rules {
		if *old.Address != address {
			out = append(out, old)
		}
	}
	if rule != nil {
		out = append(out, *rule)
	}
	sort.Slice(out, func(i, j int) bool { return *out[i].Address < *out[j].Address })
	param := modbusRulesParam(out)
	r = copyRequest(r)
	r.Params["rules"] = param
	_, err = engine.ModbusRules(r)
	return param, err
}
func (v *inspector) modbusRuleForm(address int) {
	if v.modbus == nil {
		return
	}
	if address < 0 {
		var ok bool
		address, ok = v.modbusSelectedAddress()
		if !ok {
			address = v.modbus.request.Int("address", 0)
		}
	}
	rules, err := engine.ModbusRules(v.modbus.request)
	if err != nil {
		v.owner.modal(err.Error())
		return
	}
	rule := engine.RegisterRule{Address: &address, Repr: "u16"}
	exists := false
	for _, candidate := range rules {
		if *candidate.Address == address {
			rule = candidate
			exists = true
			break
		}
	}
	kinds := []string{"u16", "i16", "f16", "u32", "i32", "f32", "u64", "i64", "f64"}
	orders := []string{"继承请求", "ABCD", "BADC", "CDAB", "DCBA"}
	ki, oi := 0, 0
	for i, k := range kinds {
		if k == rule.Repr {
			ki = i
		}
	}
	for i, o := range orders {
		if o == rule.WordOrder {
			oi = i
		}
	}
	next := []string{}
	for _, a := range rule.Next {
		next = append(next, strconv.Itoa(a))
	}
	decimals := ""
	if rule.Decimals != nil {
		decimals = strconv.Itoa(*rule.Decimals)
	}
	form := tview.NewForm().SetItemPadding(0).AddInputField("规则地址(固定)", strconv.Itoa(address), 12, nil, nil).AddDropDown("解释类型", kinds, ki, nil).AddDropDown("规则字序", orders, oi, nil).AddInputField("后续地址(逗号分隔)", strings.Join(next, ","), 42, nil, nil).AddInputField("运算JSON数组", modbusRuleJSON(rule.Ops), 52, nil, nil).AddInputField("枚举JSON对象", modbusRuleJSON(rule.Enum), 52, nil, nil).AddInputField("位名称JSON对象", modbusRuleJSON(rule.Bits), 52, nil, nil).AddInputField("小数位0..15(空=自动)", decimals, 8, nil, nil).AddInputField("前缀", rule.Prefix, 40, nil, nil).AddInputField("后缀", rule.Suffix, 40, nil, nil)
	form.GetFormItem(0).(*tview.InputField).SetDisabled(true)
	close := v.modbusDialog("modbus-rule", form)
	build := func() (*engine.RegisterRule, error) {
		_, repr := form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
		_, order := form.GetFormItem(2).(*tview.DropDown).GetCurrentOption()
		if order == orders[0] {
			order = ""
		}
		candidate := engine.RegisterRule{Address: &address, Repr: repr, WordOrder: order}
		n := strings.TrimSpace(form.GetFormItem(3).(*tview.InputField).GetText())
		if len(n) > 128 {
			return nil, fmt.Errorf("后续地址过长")
		}
		if n != "" {
			for _, part := range strings.Split(n, ",") {
				a, e := modbusParseAddress(part, 0)
				if e != nil || strings.HasPrefix(strings.TrimSpace(part), "+") || strings.HasPrefix(strings.TrimSpace(part), "-") {
					return nil, fmt.Errorf("后续地址需绝对0..65535")
				}
				candidate.Next = append(candidate.Next, a)
			}
		}
		for _, f := range []struct {
			i   int
			out any
		}{{4, &candidate.Ops}, {5, &candidate.Enum}, {6, &candidate.Bits}} {
			if err := modbusRuleDecode(form.GetFormItem(f.i).(*tview.InputField).GetText(), f.out); err != nil {
				return nil, err
			}
		}
		d := strings.TrimSpace(form.GetFormItem(7).(*tview.InputField).GetText())
		if d != "" {
			n, e := strconv.Atoi(d)
			if e != nil || n < 0 || n > 15 {
				return nil, fmt.Errorf("小数位需0..15")
			}
			candidate.Decimals = &n
		}
		candidate.Prefix = form.GetFormItem(8).(*tview.InputField).GetText()
		candidate.Suffix = form.GetFormItem(9).(*tview.InputField).GetText()
		if len(candidate.Prefix)+len(candidate.Suffix) > 2048 {
			return nil, fmt.Errorf("前后缀最多2048字节")
		}
		return &candidate, nil
	}
	preview := func(remove bool) {
		var candidate *engine.RegisterRule
		var err error
		if !remove {
			candidate, err = build()
			if err != nil {
				form.SetTitle(display(err.Error()))
				return
			}
		}
		source, err := v.modbusSavedRequest()
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		params, err := modbusRuleReplace(source, address, candidate)
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		close()
		v.modbusRulePreview(address, params, remove)
	}
	form.AddButton("本机预览", func() { preview(false) })
	if exists {
		form.AddButton("预览删除", func() { preview(true) })
	}
	form.AddButton("取消", close).SetBorder(true).SetTitle(" 规则编辑 · 运算如[\"/10\"] 枚举如{\"1\":\"运行\"} · Esc取消 F8停止 ")
}
func (v *inspector) modbusRulePreview(address int, rules []any, remove bool) {
	r := copyRequest(v.modbus.request)
	r.Params["rules"] = rules
	interpreter, err := engine.NewModbusInterpreter(r)
	if err != nil {
		v.owner.modal(err.Error())
		return
	}
	var out strings.Builder
	fmt.Fprintf(&out, "当前空间:%s Unit:%d 规则地址:%d\n只修改本机配置；不读取/写入设备。\n", modbusReadAction(r.Action), r.Int("unit", 1), address)
	if remove {
		out.WriteString("\n将删除此地址规则，其他规则保留。\n")
	} else {
		for _, raw := range rules {
			b, _ := json.MarshalIndent(raw, "", "  ")
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if fmt.Sprint(m["address"]) == strconv.Itoa(address) {
				out.WriteString(string(b))
			}
		}
	}
	h := v.modbus.interaction
	found := false
	for i := len(h.frames) - 1; i >= 0; i-- {
		frame := h.frames[i]
		if _, ok := frame.words[address]; !ok {
			continue
		}
		found = true
		row, _ := interpreter.Field(frame.words, address)
		fmt.Fprintf(&out, "\n\n同响应缓存预览 UTC:%s\n原始词:%v\n结果:%v\n数值:%v\n", frame.at.UTC().Format("15:04:05.000"), frame.words, row["custom"], row["custom_numeric"])
		break
	}
	if !found {
		out.WriteString("\n\n尚无当前地址的同响应缓存；只验证规则，不自动补读。")
	}
	view := tview.NewTextView().SetText(clean(out.String())).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 规则预览 · 保存才写配置文件 ")
	close := func() { v.owner.pages.RemovePage("modbus-rule-preview"); v.owner.App.SetFocus(v.table) }
	buttons := tview.NewForm().AddButton("明确保存", func() {
		if err := v.modbusSaveParams(map[string]any{"rules": rules}); err != nil {
			view.SetTitle(display(err.Error()))
			return
		}
		if err := v.modbusRebuild(); err != nil {
			view.SetTitle(display(err.Error()))
			return
		}
		close()
	}).AddButton("取消", close)
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(v.modbusPanelKeys(close, []tview.Primitive{view}, buttons))
	v.owner.pages.AddPage("modbus-rule-preview", panel, true, true)
	v.owner.App.SetFocus(view)
}
func (v *inspector) modbusRebuild() error {
	interpreter, err := engine.NewModbusInterpreter(v.modbus.request)
	if err != nil {
		return err
	}
	v.modbus.interaction.interpreter = interpreter
	v.values = map[int]map[string]any{}
	for _, frame := range v.modbus.interaction.frames {
		rows, err := interpreter.Interpret(frame.words)
		if err != nil {
			return err
		}
		for _, row := range rows {
			row["sampled_at"] = frame.at
			v.values[row["address"].(int)] = row
		}
	}
	v.renderRegisters()
	return nil
}
func (v *inspector) modbusAnnotationPanel(rulesOnly bool) {
	if v.modbus == nil {
		return
	}
	rules, err := engine.ModbusRules(v.modbus.request)
	if err != nil {
		v.owner.modal(err.Error())
		return
	}
	byAddress := map[int]string{}
	for _, rule := range rules {
		byAddress[*rule.Address] = rule.Repr
	}
	table := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	render := func() {
		table.Clear()
		addresses := map[int]bool{}
		for a := range byAddress {
			addresses[a] = true
		}
		if !rulesOnly {
			for a := range v.labels {
				addresses[a] = true
			}
		}
		sorted := []int{}
		for a := range addresses {
			sorted = append(sorted, a)
		}
		sort.Ints(sorted)
		for col, h := range []string{"地址", "标签", "规则", "缓存状态"} {
			table.SetCell(0, col, tview.NewTableCell(h).SetSelectable(false))
		}
		for i, a := range sorted {
			state := "未读取"
			if v.values[a] != nil {
				state = "已缓存"
			}
			for col, value := range []string{strconv.Itoa(a), v.labels[a], byAddress[a], state} {
				table.SetCell(i+1, col, tview.NewTableCell(display(value)).SetReference(a))
			}
		}
	}
	close := func() { v.owner.pages.RemovePage("modbus-annotations"); v.owner.App.SetFocus(v.table) }
	table.SetBorder(true).SetTitle(" 标注面板 · r仅规则/全部 e编辑规则 l标签 Enter定位/预览读取 Esc关闭 F8停止 ")
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		if e.Rune() == 'r' {
			rulesOnly = !rulesOnly
			render()
			return nil
		}
		row, _ := table.GetSelection()
		if row < 1 || row >= table.GetRowCount() {
			return e
		}
		a, _ := table.GetCell(row, 0).GetReference().(int)
		if e.Rune() == 'e' {
			close()
			v.modbusRuleForm(a)
			return nil
		}
		if e.Rune() == 'l' {
			close()
			v.label(a)
			return nil
		}
		if e.Key() == tcell.KeyEnter {
			close()
			v.filtered = false
			v.matrix = false
			v.renderRegisters()
			for i, address := range v.rows {
				if address == a {
					v.table.Select(i+1, 0)
					return nil
				}
			}
			r := modbusReadRequest(v.modbus.request)
			r.Params["address"] = a
			v.modbusReadForm(&r)
			return nil
		}
		return e
	})
	render()
	v.owner.pages.AddPage("modbus-annotations", table, true, true)
	v.owner.App.SetFocus(table)
}
