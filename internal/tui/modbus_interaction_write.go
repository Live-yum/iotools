package tui

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/rivo/tview"
)

// The exact words shown by confirmation are the exact words submitted, rather
// than a second independently parsed or rounded value.
func modbusBuildWrite(base config.Request, address, unit int, kind, value, order, fc string) (config.Request, string, error) {
	r := modbusReadRequest(base)
	r.Params["address"] = address
	r.Params["unit"] = unit
	r.Params["samples"] = 1
	r.Params["count"] = 1
	r.Params["word_order"] = order
	for _, key := range []string{"rules", "pins", "labels"} {
		delete(r.Params, key)
	}
	if address < 0 || address > 65535 || unit < 1 || unit > 247 {
		return r, "", fmt.Errorf("地址0..65535，Unit1..247")
	}
	if kind == "coil" {
		if value != "true" && value != "false" {
			return r, "", fmt.Errorf("线圈必须明确输入true/false")
		}
		bit := value == "true"
		if fc == "single" {
			r.Action = "write-coil"
			r.Params["value"] = bit
		} else if fc == "multiple" {
			r.Action = "write-coils"
			r.Params["values"] = []any{bit}
		} else {
			return r, "", fmt.Errorf("无效功能码")
		}
		return r, fmt.Sprintf("线圈 %d = %t", address, bit), nil
	}
	words, err := engine.EncodeModbusValue(kind, value, order, address)
	if err != nil {
		return r, "", err
	}
	r.Params["count"] = len(words)
	if fc == "single" {
		if len(words) != 1 {
			return r, "", fmt.Errorf("FC6只允许1个16位词；请选择multiple(FC16)")
		}
		r.Action = "write-register"
		r.Params["value"] = int(words[0])
	} else if fc == "multiple" {
		r.Action = "write-registers"
		values := make([]any, len(words))
		for i, n := range words {
			values[i] = int(n)
		}
		r.Params["values"] = values
	} else {
		return r, "", fmt.Errorf("无效功能码")
	}
	var preview strings.Builder
	fmt.Fprintf(&preview, "%s 输入精确文本: %s\n字序: %s\n", kind, value, order)
	for i, n := range words {
		fmt.Fprintf(&preview, "地址 %d → %d (0x%04X)\n", address+i, n, n)
	}
	decoded, _ := engine.NewModbusInterpreter(r)
	raw := map[int]uint16{}
	for i, n := range words {
		raw[address+i] = n
	}
	row, _ := decoded.Field(raw, address)
	fmt.Fprintf(&preview, "编码后%s: %v\n浮点编码可能舍入，请核对编码后的值。", kind, row[kind])
	return r, preview.String(), nil
}
func modbusAdjustInteger(kind, value string, delta int, bit int) (string, error) {
	widths := map[string]int{"u16": 16, "i16": 16, "u32": 32, "i32": 32, "u64": 64, "i64": 64}
	width, ok := widths[kind]
	if !ok {
		return "", fmt.Errorf("位操作/增减仅用于整数类型")
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return "", fmt.Errorf("请输入精确十进制整数")
	}
	limit := new(big.Int).Lsh(big.NewInt(1), uint(width))
	signed := kind[0] == 'i'
	min := big.NewInt(0)
	max := new(big.Int).Sub(new(big.Int).Set(limit), big.NewInt(1))
	if signed {
		max.Rsh(limit, 1)
		min.Neg(new(big.Int).Set(max))
		max.Sub(max, big.NewInt(1))
		limit.Lsh(big.NewInt(1), uint(width))
	}
	if n.Cmp(min) < 0 || n.Cmp(max) > 0 {
		return "", fmt.Errorf("输入超出类型范围")
	}
	if bit >= 0 {
		if bit >= width {
			return "", fmt.Errorf("位索引超出类型宽度")
		}
		if n.Sign() < 0 {
			n.Add(n, limit)
		}
		n.SetBit(n, bit, 1-n.Bit(bit))
		if signed && n.Bit(width-1) == 1 {
			n.Sub(n, limit)
		}
	} else {
		n.Add(n, big.NewInt(int64(delta)))
	}
	if n.Cmp(min) < 0 || n.Cmp(max) > 0 {
		return "", fmt.Errorf("增减溢出；数值未改变")
	}
	return n.String(), nil
}
func (v *inspector) modbusWriteForm() {
	if v.modbus == nil {
		return
	}
	u := v.owner
	if u.readonly || u.running {
		u.modal("只读模式或采样中禁止写入；先停止并明确启用写权限")
		return
	}
	base := copyRequest(v.modbus.request)
	space := modbusReadAction(base.Action)
	if space == "read-input" || space == "read-discrete" {
		u.modal("输入寄存器/离散输入不可写；请明确切换可写空间")
		return
	}
	address, ok := v.modbusSelectedAddress()
	if !ok {
		address = base.Int("address", 0)
	}
	kinds := []string{"u16", "i16", "f16", "u32", "i32", "f32", "u64", "i64", "f64"}
	value := ""
	if space == "read-coils" {
		kinds = []string{"coil"}
		if n, ok := v.values[address]["u16"].(uint16); ok {
			value = strconv.FormatBool(n != 0)
		}
	} else if n, ok := v.values[address]["u16"].(uint16); ok {
		value = strconv.Itoa(int(n))
	}
	form := tview.NewForm().AddInputField("目标地址", strconv.Itoa(address), 18, nil, nil).AddInputField("Unit (1..247)", strconv.Itoa(base.Int("unit", 1)), 8, nil, nil).AddDropDown("数值类型", kinds, 0, nil).AddInputField("精确值(线圈true/false)", value, 48, nil, nil)
	orders := []string{"ABCD", "BADC", "CDAB", "DCBA"}
	idx := 0
	for i, o := range orders {
		if o == base.String("word_order", "ABCD") {
			idx = i
		}
	}
	form.AddDropDown("字序", orders, idx, nil).AddDropDown("功能码", []string{"multiple", "single", "read-write(FC23)"}, 0, nil).AddInputField("位索引(最低位0)", "0", 8, nil, nil)
	form.AddInputField("FC23读取地址(仅FC23)", strconv.Itoa(address), 18, nil, nil).AddInputField("FC23读取数量(仅FC23)", "1", 8, nil, nil)
	close := v.modbusDialog("modbus-write", form)
	adjust := func(delta int, toggle bool) {
		_, kind := form.GetFormItem(2).(*tview.DropDown).GetCurrentOption()
		input := form.GetFormItem(3).(*tview.InputField)
		if kind == "coil" {
			if input.GetText() != "true" && input.GetText() != "false" {
				form.SetTitle("线圈值需true/false")
				return
			}
			input.SetText(strconv.FormatBool(input.GetText() != "true"))
			return
		}
		bit := -1
		if toggle {
			var err error
			bit, err = strconv.Atoi(form.GetFormItem(6).(*tview.InputField).GetText())
			if err != nil || bit < 0 {
				form.SetTitle("位索引无效")
				return
			}
		}
		next, err := modbusAdjustInteger(kind, input.GetText(), delta, bit)
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		input.SetText(next)
	}
	form.AddButton("预览", func() {
		if u.readonly || u.running {
			form.SetTitle("写入已被只读/运行门禁阻止")
			return
		}
		a, err := modbusParseAddress(form.GetFormItem(0).(*tview.InputField).GetText(), address)
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		unit, err := strconv.Atoi(form.GetFormItem(1).(*tview.InputField).GetText())
		if err != nil {
			form.SetTitle("Unit需整数")
			return
		}
		_, kind := form.GetFormItem(2).(*tview.DropDown).GetCurrentOption()
		_, order := form.GetFormItem(4).(*tview.DropDown).GetCurrentOption()
		_, fc := form.GetFormItem(5).(*tview.DropDown).GetCurrentOption()
		buildFC := fc
		if fc == "read-write(FC23)" {
			buildFC = "multiple"
		}
		r, text, err := modbusBuildWrite(base, a, unit, kind, form.GetFormItem(3).(*tview.InputField).GetText(), order, buildFC)
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		if fc == "read-write(FC23)" {
			if kind == "coil" {
				form.SetTitle("FC23仅适用于保持寄存器")
				return
			}
			ra, e := modbusParseAddress(form.GetFormItem(7).(*tview.InputField).GetText(), a)
			rc, ce := strconv.Atoi(form.GetFormItem(8).(*tview.InputField).GetText())
			if e != nil || ce != nil || rc < 1 || rc > 125 || ra+rc > 65536 {
				form.SetTitle("FC23需明确合法读取范围，数量1..125")
				return
			}
			r.Action = "read-write-registers"
			r.Params["read_address"] = ra
			r.Params["read_count"] = rc
			text += fmt.Sprintf("\nFC23同一事务读回：地址%d，数量%d（不额外读取/重试）", ra, rc)
		}
		close()
		v.modbusWritePreview(r, text)
	})
	form.AddButton("翻转位", func() { adjust(0, true) }).AddButton("-1", func() { adjust(-1, false) }).AddButton("+1", func() { adjust(1, false) }).AddButton("取消", close).SetBorder(true).SetTitle(" 写入编辑 · 所有操作仅本机；预览后仍需再次确认 · Esc取消 ")
}
func (v *inspector) modbusWritePreview(r config.Request, details string) {
	// Only same-scope cached values may serve as optional audit observations. A
	// missing word makes the prior value explicitly unknown, never an extra read.
	previous := []any{}
	if r.Int("unit", 1) == v.modbus.request.Int("unit", 1) {
		h := v.modbus.interaction
		for i := len(h.frames) - 1; i >= 0; i-- {
			frame := h.frames[i]
			if _, ok := frame.words[r.Int("address", 0)]; !ok {
				continue
			}
			for offset := 0; offset < r.Int("count", 1); offset++ {
				n, ok := frame.words[r.Int("address", 0)+offset]
				if !ok {
					previous = nil
					break
				}
				previous = append(previous, int(n))
			}
			break
		}
	}
	if len(previous) > 0 {
		r.Params["write_log_previous"] = previous
	}
	form := tview.NewForm()
	close := v.modbusDialog("modbus-write-preview", form)
	form.AddTextView("待写入原始词", details, 70, 10, false, true)
	form.AddButton("继续核对目标并确认", func() {
		if v.owner.readonly || v.owner.running {
			form.SetTitle("只读/运行门禁阻止写入")
			return
		}
		close()
		v.owner.confirmDerived(r)
	}).AddButton("取消", close).SetBorder(true).SetTitle(" 本机编码预览 · 下一步显示完整目标/Unit/地址/原始值 ")
	form.SetFocus(1)
	v.owner.App.SetFocus(form)
}
