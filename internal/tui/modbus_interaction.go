package tui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var modbusReadActions = []string{"read-holding", "read-input", "read-coils", "read-discrete"}

func modbusReadAction(action string) string {
	for _, s := range modbusReadActions {
		if s == action {
			return s
		}
	}
	if strings.Contains(action, "coil") {
		return "read-coils"
	}
	return "read-holding"
}
func modbusReadRequest(r config.Request) config.Request {
	r = copyRequest(r)
	r.Action = modbusReadAction(r.Action)
	for _, k := range []string{"value", "values", "value_type", "pdu_hex", "units", "end_address", "match_value", "read_code", "object_id", "write_log_previous", "read_address", "read_count"} {
		delete(r.Params, k)
	}
	return r
}
func (v *inspector) modbusSelectedAddress() (int, bool) {
	row, col := v.table.GetSelection()
	if row < 1 {
		return 0, false
	}
	if v.matrix {
		a, ok := v.table.GetCell(row, col).GetReference().(int)
		return a, ok
	}
	if row > len(v.rows) {
		return 0, false
	}
	return v.rows[row-1], true
}
func (v *inspector) modbusDialog(name string, form *tview.Form) func() {
	close := func() { v.owner.pages.RemovePage(name); v.owner.App.SetFocus(v.table) }
	form.SetCancelFunc(close).SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
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
	v.owner.pages.AddPage(name, form, true, true)
	v.owner.App.SetFocus(form)
	return close
}
func modbusParseAddress(text string, current int) (int, error) {
	s := strings.TrimSpace(text)
	if len(s) == 0 || len(s) > 32 {
		return 0, fmt.Errorf("地址不能为空且最长32字符")
	}
	sign := 0
	if s[0] == '+' || s[0] == '-' {
		sign = 1
		if s[0] == '-' {
			sign = -1
		}
		s = strings.TrimSpace(s[1:])
	}
	base := 10
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "0x") {
		base = 16
		s = s[2:]
	} else if strings.HasPrefix(lower, "x") {
		base = 16
		s = s[1:]
	}
	n, err := strconv.ParseUint(s, base, 16)
	if err != nil {
		return 0, fmt.Errorf("地址需十进制/x十六进制，范围0..65535")
	}
	a := int(n)
	if sign != 0 {
		a = current + sign*a
	}
	if a < 0 || a > 65535 {
		return 0, fmt.Errorf("相对地址越界；未截断或移动")
	}
	return a, nil
}
func (v *inspector) modbusSaveRead(r config.Request) error {
	u := v.owner
	if u.running {
		return fmt.Errorf("请先停止采样")
	}
	saved, err := v.modbusSavedRequest()
	if err != nil {
		return err
	}
	// Replace only the reviewed read controls. Preserve unrelated source fields.
	oldSpace, oldUnit := modbusReadAction(saved.Action), saved.Int("unit", 1)
	saved.Action = r.Action
	for _, k := range []string{"address", "unit", "count", "word_order", "samples", "interval_ms"} {
		saved.Params[k] = r.Params[k]
	}
	if oldSpace != r.Action || oldUnit != r.Int("unit", 1) {
		for _, k := range []string{"pins", "labels", "rules"} {
			delete(saved.Params, k)
		}
	}
	for _, k := range []string{"value", "values", "value_type", "pdu_hex", "units", "end_address", "match_value", "read_code", "object_id", "write_log_previous", "read_address", "read_count"} {
		delete(saved.Params, k)
	}
	b, err := config.ReplaceRequest(u.raw, saved.ID, saved)
	if err != nil {
		return err
	}
	c, err := config.Parse(b)
	if err != nil {
		return err
	}
	old, err := modbusReadImport(u.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(old, u.raw) {
		return fmt.Errorf("文件已被外部修改，未覆盖")
	}
	if err = config.Save(u.path, b); err != nil {
		return err
	}
	c.SourcePath = u.path
	u.collection = c
	u.raw = b
	u.preview()
	return nil
}
func (v *inspector) modbusApplyRead(r config.Request, save bool) error {
	if v.owner.running {
		return fmt.Errorf("请先按F8停止采样")
	}
	if save {
		if err := v.modbusSaveRead(r); err != nil {
			return err
		}
	}
	v.owner.lastRequest = copyRequest(r)
	v.reset(r)
	return nil
}
func (v *inspector) modbusReadForm(candidate *config.Request) {
	if v.modbus == nil {
		return
	}
	r := modbusReadRequest(v.modbus.request)
	if candidate != nil {
		r = modbusReadRequest(*candidate)
	}
	form := tview.NewForm()
	idx := 0
	for i, a := range modbusReadActions {
		if a == r.Action {
			idx = i
		}
	}
	form.AddDropDown("寄存器空间", []string{"保持寄存器(h)", "输入寄存器(i)", "线圈(c)", "离散输入(d)"}, idx, nil)
	form.AddInputField("起始地址", strconv.Itoa(r.Int("address", 0)), 20, nil, nil).AddInputField("Unit (1..247)", strconv.Itoa(r.Int("unit", 1)), 10, nil, nil).AddInputField("读取数量 (1..125)", strconv.Itoa(r.Int("count", 1)), 10, nil, nil)
	orders := []string{"ABCD", "BADC", "CDAB", "DCBA"}
	idx = 0
	for i, o := range orders {
		if o == r.String("word_order", "ABCD") {
			idx = i
		}
	}
	form.AddDropDown("字序", orders, idx, nil).AddInputField("采样次数 (1..100000)", strconv.Itoa(r.Int("samples", 1)), 10, nil, nil).AddInputField("间隔毫秒 (10..86400000)", strconv.Itoa(r.Int("interval_ms", 1000)), 10, nil, nil)
	close := v.modbusDialog("modbus-read-controls", form)
	apply := func(save, run bool) {
		next := copyRequest(r)
		space, _ := form.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
		next.Action = modbusReadActions[space]
		a, err := modbusParseAddress(form.GetFormItem(1).(*tview.InputField).GetText(), r.Int("address", 0))
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		next.Params["address"] = a
		for _, f := range []struct {
			i      int
			key    string
			lo, hi int
		}{{2, "unit", 1, 247}, {3, "count", 1, 125}, {5, "samples", 1, 100000}, {6, "interval_ms", 10, 86400000}} {
			n, e := strconv.Atoi(form.GetFormItem(f.i).(*tview.InputField).GetText())
			if e != nil || n < f.lo || n > f.hi {
				form.SetTitle("无效参数：" + f.key)
				return
			}
			next.Params[f.key] = n
		}
		if a+next.Int("count", 1) > 65536 {
			form.SetTitle("读取窗口越过65535；请调整数量")
			return
		}
		_, next.Params["word_order"] = form.GetFormItem(4).(*tview.DropDown).GetCurrentOption()
		if next.Action != modbusReadAction(v.modbus.request.Action) || next.Int("unit", 1) != v.modbus.request.Int("unit", 1) {
			for _, k := range []string{"pins", "labels", "rules"} {
				delete(next.Params, k)
			}
		}
		if err = v.modbusApplyRead(next, save); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		close()
		if run {
			v.owner.start(next)
		} else {
			v.owner.setStatus("读取设置已应用；尚未连接，按r读取")
		}
	}
	form.AddButton("临时应用", func() { apply(false, false) }).AddButton("明确读取", func() { apply(false, true) }).AddButton("保存设置", func() { apply(true, false) }).AddButton("取消", close)
	form.SetBorder(true).SetTitle(" 读取设置 · 改空间清除该请求临时标注 · Esc取消 F8停止 ")
}
func (v *inspector) modbusGoToForm() {
	if v.modbus == nil {
		return
	}
	r := modbusReadRequest(v.modbus.request)
	current, ok := v.modbusSelectedAddress()
	if !ok {
		current = r.Int("address", 0)
	}
	form := tview.NewForm().AddInputField("地址/标签 (h/i/c/d, +/-, x/0x)", "", 55, nil, nil)
	close := v.modbusDialog("modbus-go-to", form)
	form.AddButton("查找并预览", func() {
		q := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if q == "" || len(q) > 1024 {
			form.SetTitle("请输入有界地址或标签")
			return
		}
		next := copyRequest(r)
		numeric := q
		if len(q) > 1 {
			if i := strings.IndexByte("hicd", strings.ToLower(q)[0]); i >= 0 {
				next.Action = modbusReadActions[i]
				numeric = q[1:]
			}
		}
		address, err := modbusParseAddress(numeric, current)
		if err != nil {
			hits := []int{}
			for a, label := range v.labels {
				if strings.Contains(strings.ToLower(label), strings.ToLower(q)) {
					hits = append(hits, a)
				}
			}
			if len(hits) != 1 {
				form.SetTitle(fmt.Sprintf("地址无效或标签匹配%d项，请输入唯一标签", len(hits)))
				return
			}
			address = hits[0]
			next.Action = r.Action
		}
		next.Params["address"] = address
		if next.Action == r.Action {
			for row, a := range v.rows {
				if a == address {
					v.filtered = false
					v.matrix = false
					v.renderRegisters()
					for i, b := range v.rows {
						if b == address {
							row = i
							break
						}
					}
					v.table.Select(row+1, 0)
					close()
					return
				}
			}
		}
		close()
		v.modbusReadForm(&next)
	}).AddButton("取消", close)
	form.SetBorder(true).SetTitle(" 跳转 · 已缓存则定位；未缓存先预览读取设置 · 不自动读取 ")
}

// Local annotations must never leak into another space/unit via a temporary
// read configuration that happens to retain the same collection request ID.
func (v *inspector) modbusAnnotationScope() error {
	if v.modbus == nil {
		return nil
	}
	saved, err := v.modbusSavedRequest()
	if err != nil {
		return err
	}
	resolved, err := v.owner.collection.Resolve(saved, v.owner.profile)
	if err != nil {
		return err
	}
	active, err := v.owner.collection.Resolve(v.modbus.request, v.owner.profile)
	if err != nil {
		return err
	}
	if modbusReadAction(resolved.Action) != modbusReadAction(active.Action) || resolved.Int("unit", 1) != active.Int("unit", 1) || resolved.Endpoint != active.Endpoint {
		return fmt.Errorf("当前临时空间/Unit/目标不同；请先用R保存读取设置，再保存标注")
	}
	return nil
}
