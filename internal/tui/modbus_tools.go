package tui

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type modbusToolResults struct {
	kind    string
	unknown map[int]bool
	objects map[int]string
	probes  []map[string]any
	raw     map[string]any
}

func (v *inspector) modbusToolEvent(e engine.Event) bool {
	if v.modbus == nil {
		return false
	}
	h := v.modbus.tools
	switch e.Kind {
	case "registers":
		if rows, ok := e.Data.([]map[string]any); ok {
			for _, row := range rows {
				if a, ok := row["address"].(int); ok {
					delete(h.unknown, a)
				}
			}
		}
		return false
	case "sweep-error":
		if m, ok := e.Data.(map[string]any); ok {
			a, aok := m["batch_address"].(int)
			n, nok := m["batch_count"].(int)
			if aok && nok && a >= 0 && n > 0 && n <= 125 && a+n <= 65536 {
				for i := 0; i < n; i++ {
					delete(v.values, a+i)
					h.unknown[a+i] = true
				}
				v.renderRegisters()
			}
		}
		return true
	case "device-identification":
		m, ok := e.Data.(map[string]any)
		if !ok {
			return false
		}
		objects, ok := m["objects"].(map[int]string)
		if !ok {
			return false
		}
		h.kind = e.Kind
		for id, value := range objects {
			if id >= 0 && id <= 255 {
				h.objects[id] = value
			}
		}
		v.modbusRenderTools()
		return true
	case "unit-probe":
		m, ok := e.Data.(map[string]any)
		if !ok {
			return false
		}
		h.kind = e.Kind
		if len(h.probes) < 32 {
			h.probes = append(h.probes, m)
		}
		v.modbusRenderTools()
		return true
	case "raw-pdu":
		m, ok := e.Data.(map[string]any)
		if !ok {
			return false
		}
		h.kind = e.Kind
		h.raw = m
		v.modbusRenderTools()
		return true
	case "sweep-progress":
		if m, ok := e.Data.(map[string]any); ok {
			v.owner.setStatus(fmt.Sprintf("扫描进度%v/%v 地址%v至%v · 失败批次:%v 跳过位置:%v 未知:%d · F8取消", m["cycle"], m["cycles"], m["through"], m["end_address"], m["failed_batches"], m["skipped_positions"], len(h.unknown)))
			if len(h.unknown) > 0 {
				v.table.SetTitle(fmt.Sprintf(" 扫描结果 · %d个地址仍未知（失败批次不证明单个地址故障） · F8取消 ", len(h.unknown)))
			}
		}
		return true
	}
	return false
}
func (v *inspector) modbusRenderTools() {
	h := v.modbus.tools
	v.table.Clear()
	v.rows = nil
	v.pages.SwitchToPage("table")
	var headers []string
	rows := [][]string{}
	refs := []any{}
	switch h.kind {
	case "device-identification":
		headers = []string{"对象ID", "值"}
		ids := []int{}
		for id := range h.objects {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			rows = append(rows, []string{fmt.Sprintf("0x%02X", id), h.objects[id]})
			refs = append(refs, id)
		}
		v.table.SetTitle(fmt.Sprintf(" 设备标识 · 访问级别%d · i重新选择/刷新 · Ctrl-Y复制 ", v.modbus.request.Int("read_code", 1)))
	case "unit-probe":
		headers = []string{"Unit", "结果", "空间", "地址/数量", "读取值/错误"}
		for _, m := range h.probes {
			status := "无有效响应"
			if m["responsive"] == true {
				status = "响应"
			} else if m["exception"] == true {
				status = "协议异常"
			}
			value := fmt.Sprint(m["values"])
			if status != "响应" {
				value = fmt.Sprint(m["error"])
			}
			rows = append(rows, []string{fmt.Sprint(m["unit"]), status, fmt.Sprint(m["type"]), fmt.Sprintf("%v/%v", m["address"], m["count"]), value})
			refs = append(refs, m)
		}
		v.table.SetTitle(" 单元探测 · Enter仅选择/预览，不自动连接 · U重设探测 · Ctrl-Y复制 ")
	case "raw-pdu":
		headers = []string{"方向", "字节数", "十六进制PDU"}
		for _, key := range []string{"request_hex", "response_hex"} {
			s, _ := h.raw[key].(string)
			rows = append(rows, []string{key, strconv.Itoa(len(s) / 2), s})
			refs = append(refs, s)
		}
		v.table.SetTitle(" 原始PDU结果 · j重新编辑 · 不会重放写入 · Ctrl-Y复制 ")
	}
	for col, name := range headers {
		v.table.SetCell(0, col, tview.NewTableCell(name).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	for row, values := range rows {
		for col, value := range values {
			v.table.SetCell(row+1, col, tview.NewTableCell(display(value)).SetReference(refs[row]))
		}
	}
}
func (v *inspector) modbusToolKey(e *tcell.EventKey) *tcell.EventKey {
	if v.modbus == nil || v.modbus.tools.kind == "" {
		return e
	}
	if e.Key() == tcell.KeyEnter && v.modbus.tools.kind == "unit-probe" {
		row, _ := v.table.GetSelection()
		if row > 0 && row <= len(v.modbus.tools.probes) {
			m := v.modbus.tools.probes[row-1]
			if m["responsive"] == true {
				r := modbusReadRequest(v.modbus.request)
				r.Action = fmt.Sprint(m["type"])
				r.Params["unit"] = m["unit"]
				r.Params["address"] = m["address"]
				r.Params["count"] = m["count"]
				v.modbusReadForm(&r)
			}
		}
		return nil
	}
	if e.Key() == tcell.KeyEnter {
		return nil
	}
	return e
}
func (v *inspector) modbusToolStart(r config.Request) error {
	if v.owner.running {
		return fmt.Errorf("请先F8停止当前请求")
	}
	resolved, err := v.owner.collection.Resolve(r, v.owner.profile)
	if err != nil {
		return err
	}
	if resolved.Mutates() {
		if v.owner.readonly {
			return fmt.Errorf("只读模式禁止写入")
		}
		v.owner.confirmDerived(resolved)
	} else {
		v.owner.start(resolved)
	}
	return nil
}
func (v *inspector) modbusDeviceIDForm() {
	if v.modbus == nil {
		return
	}
	r := modbusReadRequest(v.modbus.request)
	r.Action = "read-device-id"
	level := v.modbus.request.Int("read_code", 1) - 1
	if level < 0 || level > 3 {
		level = 0
	}
	object := v.modbus.request.Int("object_id", 0)
	form := tview.NewForm().AddInputField("目标Unit (1..247)", strconv.Itoa(r.Int("unit", 1)), 10, nil, nil).AddDropDown("访问级别", []string{"1基本", "2常规", "3扩展", "4单个对象"}, level, nil).AddInputField("起始对象ID (0..255)", strconv.Itoa(object), 10, nil, nil)
	close := v.modbusDialog("modbus-device-id", form)
	form.AddButton("明确读取", func() {
		unit, e := strconv.Atoi(form.GetFormItem(0).(*tview.InputField).GetText())
		object, oe := strconv.Atoi(form.GetFormItem(2).(*tview.InputField).GetText())
		if e != nil || unit < 1 || unit > 247 || oe != nil || object < 0 || object > 255 {
			form.SetTitle("Unit1..247，对象ID0..255")
			return
		}
		idx, _ := form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
		r.Params["unit"] = unit
		r.Params["read_code"] = idx + 1
		r.Params["object_id"] = object
		if err := v.modbusToolStart(r); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		close()
	}).AddButton("取消", close).SetBorder(true).SetTitle(" 设备标识FC43/14 · 只选择不会连接 · Esc取消 F8停止 ")
}
func modbusRawCandidate(base config.Request, unit, code int, data string) (config.Request, error) {
	r := modbusReadRequest(base)
	if unit < 1 || unit > 247 || code < 1 || code > 127 {
		return r, fmt.Errorf("Unit1..247，功能码1..127")
	}
	if len(data) > 2048 {
		return r, fmt.Errorf("十六进制载荷过长")
	}
	data = strings.Join(strings.Fields(data), "")
	b, err := hex.DecodeString(data)
	if err != nil {
		return r, fmt.Errorf("载荷必须是完整十六进制字节")
	}
	p := append([]byte{byte(code)}, b...)
	r.Params["unit"] = unit
	r.Params["pdu_hex"] = hex.EncodeToString(p)
	r.Params["address"] = 0
	r.Params["count"] = 1
	r.Action = "write-raw"
	if code >= 1 && code <= 4 || code == 43 {
		r.Action = "read-raw"
	}
	if len(p) >= 5 && code != 43 {
		r.Params["address"] = int(p[1])<<8 | int(p[2])
		if code == 1 || code == 2 || code == 3 || code == 4 || code == 15 || code == 16 {
			r.Params["count"] = int(p[3])<<8 | int(p[4])
		}
	}
	_, err = engine.ValidateModbusRaw(r)
	return r, err
}
func (v *inspector) modbusRawForm() {
	if v.modbus == nil {
		return
	}
	base := copyRequest(v.modbus.request)
	form := tview.NewForm().AddInputField("目标Unit (1..247)", strconv.Itoa(base.Int("unit", 1)), 10, nil, nil).AddInputField("功能码(十进制)", "3", 10, nil, nil).AddInputField("数据HEX(不含功能码)", "0000 0001", 64, nil, nil)
	close := v.modbusDialog("modbus-raw", form)
	form.AddButton("验证并预览", func() {
		unit, ue := strconv.Atoi(form.GetFormItem(0).(*tview.InputField).GetText())
		code, ce := strconv.Atoi(form.GetFormItem(1).(*tview.InputField).GetText())
		if ue != nil || ce != nil {
			form.SetTitle("Unit/功能码需整数")
			return
		}
		r, err := modbusRawCandidate(base, unit, code, form.GetFormItem(2).(*tview.InputField).GetText())
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		if r.Mutates() && v.owner.readonly {
			form.SetTitle("只读模式禁止原始写入")
			return
		}
		close()
		v.modbusRawPreview(r)
	}).AddButton("取消", close).SetBorder(true).SetTitle(" 原始请求 · 仅已校验FC1/2/3/4/43读、5/6/15/16写 · Esc取消 ")
}
func (v *inspector) modbusRawPreview(r config.Request) {
	form := tview.NewForm()
	close := v.modbusDialog("modbus-raw-preview", form)
	kind := "已验证只读PDU"
	if r.Mutates() {
		kind = "写入PDU，仍需完整修改确认"
	}
	text := fmt.Sprintf("%s\nUnit:%d 地址:%d 数量:%d\nPDU HEX:%s\n只有点击发送才访问配置目标；原始数据未执行。", kind, r.Int("unit", 1), r.Int("address", 0), r.Int("count", 1), r.String("pdu_hex", ""))
	form.AddTextView("审核报文", text, 70, 8, false, true).AddButton("明确发送/继续确认", func() {
		if err := v.modbusToolStart(r); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		close()
	}).AddButton("取消", close).SetBorder(true).SetTitle(" 原始PDU预览 · 未知功能码拒绝 · 不自动重试 ")
	form.SetFocus(1)
	v.owner.App.SetFocus(form)
}
func modbusUnits(text string) ([]any, error) {
	if len(text) > 512 {
		return nil, fmt.Errorf("单元列表过长")
	}
	units := []any{}
	seen := map[int]bool{}
	for _, part := range strings.Split(text, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 247 || seen[n] {
			return nil, fmt.Errorf("需不重复的Unit1..247，逗号分隔")
		}
		seen[n] = true
		units = append(units, n)
	}
	if len(units) < 1 || len(units) > 32 {
		return nil, fmt.Errorf("一次仅允许1..32个明确单元")
	}
	return units, nil
}
func (v *inspector) modbusRangeForm(scan bool) {
	if v.modbus == nil {
		return
	}
	base := modbusReadRequest(v.modbus.request)
	idx := 0
	for i, a := range modbusReadActions {
		if a == base.Action {
			idx = i
		}
	}
	form := tview.NewForm().AddDropDown("读取空间", modbusReadActions, idx, nil).AddInputField("起始地址", strconv.Itoa(base.Int("address", 0)), 16, nil, nil).AddInputField("每次读取数量 (1..125)", strconv.Itoa(base.Int("count", 1)), 10, nil, nil)
	name := "modbus-sweep"
	if scan {
		name = "modbus-unit-scan"
		form.AddInputField("明确Unit列表(最多32)", strconv.Itoa(base.Int("unit", 1)), 55, nil, nil).AddCheckbox("首个成功响应后停止", false, nil)
	} else {
		form.AddInputField("目标Unit (1..247)", strconv.Itoa(base.Int("unit", 1)), 10, nil, nil).AddInputField("结束地址(含,范围≤16000)", strconv.Itoa(base.Int("address", 0)), 16, nil, nil).AddInputField("循环次数 (1..1000)", "1", 10, nil, nil).AddInputField("循环间隔毫秒 (10..86400000)", "1000", 12, nil, nil).AddCheckbox("遇错逐地址恢复(额外读取，最多100000次)", false, nil)
	}
	close := v.modbusDialog(name, form)
	form.AddButton("范围预览", func() {
		r := modbusReadRequest(base)
		_, space := form.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
		a, err := modbusParseAddress(form.GetFormItem(1).(*tview.InputField).GetText(), 0)
		count, ce := strconv.Atoi(form.GetFormItem(2).(*tview.InputField).GetText())
		if err != nil || ce != nil || count < 1 || count > 125 || a+count > 65536 {
			form.SetTitle("读取地址/数量越界；未改变范围")
			return
		}
		r.Params["address"] = a
		r.Params["count"] = count
		if space != modbusReadAction(v.modbus.request.Action) {
			for _, key := range []string{"pins", "labels", "rules"} {
				delete(r.Params, key)
			}
		}
		if scan {
			units, e := modbusUnits(form.GetFormItem(3).(*tview.InputField).GetText())
			if e != nil {
				form.SetTitle(display(e.Error()))
				return
			}
			r.Action = "scan-units"
			r.Params["units"] = units
			r.Params["scan_type"] = space
			r.Params["stop_first"] = form.GetFormItem(4).(*tview.Checkbox).IsChecked()
		} else {
			unit, ue := strconv.Atoi(form.GetFormItem(3).(*tview.InputField).GetText())
			end, ee := modbusParseAddress(form.GetFormItem(4).(*tview.InputField).GetText(), 0)
			cycles, cye := strconv.Atoi(form.GetFormItem(5).(*tview.InputField).GetText())
			gap, ge := strconv.Atoi(form.GetFormItem(6).(*tview.InputField).GetText())
			if ue != nil || unit < 1 || unit > 247 || ee != nil || end < a || end-a+1 > 16000 || cye != nil || cycles < 1 || cycles > 1000 || ge != nil || gap < 10 || gap > 86400000 || (end-a+count)/count*cycles > 100000 {
				form.SetTitle("Unit/范围/次数无效，最多100000次通信；不自动扩展/交换范围")
				return
			}
			r.Action = "sweep-" + strings.TrimPrefix(space, "read-")
			r.Params["unit"] = unit
			r.Params["end_address"] = end
			r.Params["sweep_cycles"] = cycles
			r.Params["interval_ms"] = gap
			recoverReads := form.GetFormItem(7).(*tview.Checkbox).IsChecked()
			if recoverReads && (end-a+1)*cycles > 100000 {
				form.SetTitle("恢复模式最坏情况超过100000次读取")
				return
			}
			r.Params["sweep_recover"] = recoverReads
		}
		close()
		v.modbusRangePreview(r)
	}).AddButton("取消", close).SetBorder(true).SetTitle(" 有界探测/扫描 · 单一配置端点 · 无自动子网/广播扫描 · Esc取消 ")
}
func (v *inspector) modbusRangePreview(r config.Request) {
	b, _ := jsonMarshalSafe(r)
	form := tview.NewForm()
	close := v.modbusDialog("modbus-range-preview", form)
	form.AddTextView("待执行范围", string(b), 70, 14, false, true).AddButton("明确开始", func() {
		if err := v.modbusToolStart(r); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		close()
	}).AddButton("取消", close).SetBorder(true).SetTitle(" 扫描预览 · F8取消 · 超时总预算仍有效 · 默认遇错停止；恢复需显式勾选 ")
	form.SetFocus(1)
	v.owner.App.SetFocus(form)
}
func jsonMarshalSafe(r config.Request) ([]byte, error) {
	return json.MarshalIndent(redactPreview(r), "", "  ")
}
