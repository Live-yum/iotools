package tui

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type modbusColumn struct{ key, label string }

var modbusColumns = []modbusColumn{{"pin", "固定"}, {"address", "地址"}, {"label", "标签"}, {"u16", "u16"}, {"i16", "i16"}, {"hex", "十六进制"}, {"f32", "f32"}, {"delta", "快照差值"}, {"trend", "趋势(u16)"}, {"f64", "f64"}, {"u32_m10k", "u32 M10K"}, {"i32_m10k", "i32 M10K"}, {"custom", "规则结果"}, {"u8", "u8高/低"}, {"i8", "i8高/低"}, {"binary", "二进制"}, {"ascii", "ASCII"}, {"f16", "f16"}, {"bcd", "BCD"}, {"u32", "u32"}, {"i32", "i32"}, {"hex32", "十六进制32"}, {"bcd32", "BCD32"}, {"u64", "u64"}, {"i64", "i64"}, {"time", "采样时间(UTC)"}}

type modbusAdvanced struct {
	tools       *modbusToolResults
	interaction *modbusInteraction
	request     config.Request
	columns     []string
	widths      map[string]int
	hexAddress  bool
	timeMode    string
	keymap      map[string]rune
}

var modbusDefaultKeys = map[string]rune{"custom-rule": 'c', "annotations": 'P', "device-id": 'i', "raw": 'j', "sweep": 'B', "unit-scan": 'U', "matrix": 'm', "pin": 'p', "label": 'l', "filter": 'f', "baseline": 'd', "snapshot-save": 'S', "snapshot-open": 'O', "columns": 'C', "keymap": 'K', "import": 'I', "export": 'E', "dump": 'D', "more": 'M', "go-to": '/', "read-controls": 'R', "inspect": 'v', "graph": 'g', "write": 'w', "word-order": 'b', "unit": 'u', "register-type": 't', "page-up": '[', "page-down": ']', "batch-decrease": '{', "batch-increase": '}', "stats": 's', "activity": 'a', "rotation": 'N', "clear-session": 'X', "copy-column": 'y', "refresh": 'r', "pause": 'z'}
var modbusActionLabels = map[string]string{"custom-rule": "规则编辑", "annotations": "标签/规则面板", "device-id": "设备标识", "raw": "原始请求", "sweep": "有界扫描", "unit-scan": "明确单元探测", "matrix": "矩阵/表格", "pin": "固定寄存器", "label": "编辑标签", "filter": "仅固定项", "baseline": "差值基线", "snapshot-save": "保存快照", "snapshot-open": "快照对比", "columns": "列布局", "keymap": "快捷键", "import": "导入标注", "export": "导出标注", "dump": "导出CSV", "more": "更多操作", "go-to": "地址/标签跳转", "read-controls": "读取设置", "inspect": "寄存器详情", "graph": "字段/规则图", "write": "编辑写入", "word-order": "本机重解释字序", "unit": "Unit设置", "register-type": "预览下一空间", "page-up": "预览前一窗口", "page-down": "预览后一窗口", "batch-decrease": "预览减少读取数量", "batch-increase": "预览增加读取数量", "stats": "通信统计", "activity": "活动日志", "rotation": "集合轮换", "clear-session": "清本机会话", "copy-column": "复制当前列", "refresh": "明确读取", "pause": "取消采样"}

func modbusColumnExists(key string) bool {
	for _, c := range modbusColumns {
		if c.key == key {
			return true
		}
	}
	return false
}
func modbusColumnLabel(key string) string {
	for _, c := range modbusColumns {
		if c.key == key {
			return c.label
		}
	}
	return key
}
func modbusParseColumns(raw any) ([]string, map[string]int, bool, error) {
	widths := map[string]int{}
	if raw == nil {
		return nil, widths, false, nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("columns必须是mapping")
	}
	for key := range cfg {
		if key != "visible" && key != "widths" && key != "address_mode" && key != "time_mode" {
			return nil, nil, false, fmt.Errorf("未知columns选项:%s", key)
		}
	}
	list, ok := cfg["visible"].([]any)
	if !ok || len(list) < 1 || len(list) > len(modbusColumns) {
		return nil, nil, false, fmt.Errorf("visible需非空且有界的列列表")
	}
	columns := []string{}
	seen := map[string]bool{}
	for _, item := range list {
		key, ok := item.(string)
		if !ok || !modbusColumnExists(key) || seen[key] {
			return nil, nil, false, fmt.Errorf("未知或重复的列")
		}
		seen[key] = true
		columns = append(columns, key)
	}
	if raw, exists := cfg["widths"]; exists {
		values, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("widths必须是mapping")
		}
		for key, value := range values {
			n, e := strconv.Atoi(fmt.Sprint(value))
			if !modbusColumnExists(key) || e != nil || n < 1 || n > 120 {
				return nil, nil, false, fmt.Errorf("列宽应为1..120")
			}
			widths[key] = n
		}
	}
	if value, exists := cfg["time_mode"]; exists && value != "read_at" && value != "ago" {
		return nil, nil, false, fmt.Errorf("time_mode需read_at/ago")
	}
	mode := "decimal"
	if value, ok := cfg["address_mode"]; ok {
		mode, ok = value.(string)
		if !ok || (mode != "decimal" && mode != "hex") {
			return nil, nil, false, fmt.Errorf("address_mode需decimal/hex")
		}
	}
	return columns, widths, mode == "hex", nil
}
func modbusParseKeymap(raw any) (map[string]rune, error) {
	keys := map[string]rune{}
	for action, key := range modbusDefaultKeys {
		keys[action] = key
	}
	if raw != nil {
		mapping, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("keymap必须是mapping")
		}
		for action, value := range mapping {
			if _, ok := keys[action]; !ok {
				return nil, fmt.Errorf("未知快捷键动作:%s", action)
			}
			text, ok := value.(string)
			runes := []rune(text)
			if !ok || len(runes) != 1 || !unicode.IsPrint(runes[0]) || unicode.IsSpace(runes[0]) || strings.ContainsRune("q?+-", runes[0]) {
				return nil, fmt.Errorf("快捷键需单个可打印字符，q ? + -为保留键")
			}
			keys[action] = runes[0]
		}
	}
	seen := map[rune]bool{}
	for _, key := range keys {
		if seen[key] {
			return nil, fmt.Errorf("快捷键不可重复（含默认键）")
		}
		seen[key] = true
	}
	return keys, nil
}
func (v *inspector) modbusAdvancedReset(r config.Request) {
	v.modbus = nil
	if r.Protocol != "modbus" {
		return
	}
	cols, widths, hex, err := modbusParseColumns(r.Params["columns"])
	if err != nil {
		v.owner.setStatus("列配置无效，使用默认布局：" + err.Error())
		cols = nil
		widths = map[string]int{}
	}
	keys, keyErr := modbusParseKeymap(r.Params["keymap"])
	if keyErr != nil {
		v.owner.setStatus("快捷键配置无效，使用默认键：" + keyErr.Error())
		keys, _ = modbusParseKeymap(nil)
	}
	timeMode := "read_at"
	if columns, ok := r.Params["columns"].(map[string]any); ok && columns["time_mode"] == "ago" {
		timeMode = "ago"
	}
	interpreter, _ := engine.NewModbusInterpreter(r)
	v.modbus = &modbusAdvanced{tools: &modbusToolResults{objects: map[int]string{}, unknown: map[int]bool{}}, interaction: &modbusInteraction{interpreter: interpreter}, request: copyRequest(r), columns: cols, widths: widths, hexAddress: hex, keymap: keys, timeMode: timeMode}
}
func (v *inspector) modbusAdvancedKey(e *tcell.EventKey) *tcell.EventKey {
	if v.protocol != "modbus" || v.modbus == nil || e.Key() != tcell.KeyRune || e.Modifiers() != tcell.ModNone {
		return e
	}
	var action string
	for name, key := range v.modbus.keymap {
		if key == e.Rune() {
			action = name
			break
		}
	}
	if action == "" {
		for _, key := range modbusDefaultKeys {
			if key == e.Rune() {
				return nil
			}
		}
		return e
	}
	v.modbusDispatch(action)
	return nil
}

func (v *inspector) modbusCell(address int, key string) string {
	row := v.values[address]
	switch key {
	case "time":
		return modbusSampleTime(row, v.modbus.timeMode, time.Now())
	case "pin":
		if v.pins[address] {
			return "*"
		}
		return ""
	case "address":
		if v.modbus != nil && v.modbus.hexAddress {
			return fmt.Sprintf("0x%04X", address)
		}
		return strconv.Itoa(address)
	case "label":
		return v.labels[address]
	case "trend":
		return spark(v.history[address])
	case "delta":
		if n, ok := row["u16"].(uint16); ok {
			if before, ok := v.baseline[address]; ok {
				return fmt.Sprintf("%+d", int(n)-int(before))
			}
			return "新增"
		}
		return ""
	default:
		return optional(row, key)
	}
}
func (v *inspector) renderModbusAdvanced() bool {
	if v.modbus == nil || len(v.modbus.columns) == 0 || v.matrix {
		return false
	}
	v.table.Clear()
	v.table.SetTitle(" 寄存器 · C列 K快捷键 I导入 E导出 D CSV · p固定 l标签 ")
	v.rows = nil
	for address := range v.values {
		if !v.filtered || v.pins[address] {
			v.rows = append(v.rows, address)
		}
	}
	sort.Ints(v.rows)
	for col, key := range v.modbus.columns {
		v.table.SetCell(0, col, tview.NewTableCell(modbusColumnLabel(key)).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	for row, address := range v.rows {
		for col, key := range v.modbus.columns {
			cell := tview.NewTableCell(display(v.modbusCell(address, key))).SetReference(address)
			if w := v.modbus.widths[key]; w > 0 {
				cell.SetMaxWidth(w)
			}
			v.table.SetCell(row+1, col, cell)
		}
	}
	return true
}
func (v *inspector) modbusSavedRequest() (config.Request, error) {
	if v.modbus == nil {
		return config.Request{}, fmt.Errorf("没有Modbus请求")
	}
	id := v.modbus.request.ID
	for _, r := range v.owner.collection.Requests {
		if r.ID == id && r.Protocol == "modbus" {
			return copyRequest(r), nil
		}
	}
	return config.Request{}, fmt.Errorf("找不到源请求，未写入文件")
}
func (v *inspector) modbusSaveParams(changes map[string]any) error {
	for _, key := range []string{"pins", "labels", "rules"} {
		if _, exists := changes[key]; exists {
			if err := v.modbusAnnotationScope(); err != nil {
				return err
			}
			break
		}
	}
	u := v.owner
	if u.running {
		return fmt.Errorf("请先停止采样再保存配置")
	}
	r, err := v.modbusSavedRequest()
	if err != nil {
		return err
	}
	oldSpace, oldUnit := modbusReadAction(r.Action), r.Int("unit", 1)
	for key, value := range changes {
		if key == "__read_action" {
			action, ok := value.(string)
			if !ok || modbusReadAction(action) != action {
				return fmt.Errorf("只能保存明确的读取空间")
			}
			r.Action = action
			continue
		}
		r.Params[key] = value
	}
	if modbusReadAction(r.Action) != oldSpace || r.Int("unit", 1) != oldUnit {
		for _, key := range []string{"pins", "labels", "rules"} {
			delete(r.Params, key)
		}
	}
	b, err := config.ReplaceRequest(u.raw, r.ID, r)
	if err != nil {
		return err
	}
	current, err := modbusReadImport(u.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, u.raw) {
		return fmt.Errorf("配置已被外部修改，已停止覆盖")
	}
	if err = config.Save(u.path, b); err != nil {
		return err
	}
	c, err := config.Parse(b)
	if err != nil {
		return err
	}
	c.SourcePath = u.path
	u.collection = c
	u.raw = b
	for key, value := range changes {
		if key == "__read_action" {
			v.modbus.request.Action = value.(string)
			if u.lastRequest.ID == r.ID {
				u.lastRequest.Action = value.(string)
			}
			continue
		}
		v.modbus.request.Params[key] = value
		if u.lastRequest.ID == r.ID {
			u.lastRequest = copyRequest(u.lastRequest)
			u.lastRequest.Params[key] = value
		}
	}
	u.preview()
	return nil
}
func modbusWritePrivate(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("文件路径不能为空")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
func modbusReadImport(path string) ([]byte, error) {
	initial, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !initial.Mode().IsRegular() {
		return nil, fmt.Errorf("只允许读取普通文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("只允许读取普通文件")
	}
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("导入文件超过4MiB")
	}
	return b, nil
}
func modbusMergeAnnotations(source config.Request, incoming map[string]any) (map[string]any, error) {
	// Reuse the independent codec to validate both sides and normalize address maps.
	old, err := engine.ExportMTUIRegisters(source)
	if err != nil {
		return nil, err
	}
	base, err := engine.ImportMTUIRegisters(old, source.Action)
	if err != nil {
		return nil, err
	}
	pins := map[int]bool{}
	for _, set := range []map[string]any{base, incoming} {
		for _, p := range set["pins"].([]any) {
			n, ok := p.(int)
			if !ok {
				return nil, fmt.Errorf("固定地址无效")
			}
			pins[n] = true
		}
	}
	addresses := []int{}
	for p := range pins {
		addresses = append(addresses, p)
	}
	sort.Ints(addresses)
	pinList := []any{}
	for _, p := range addresses {
		pinList = append(pinList, p)
	}
	labels := map[string]any{}
	rules := map[int]any{}
	for _, set := range []map[string]any{base, incoming} {
		for key, value := range set["labels"].(map[string]any) {
			labels[key] = value
		}
		for _, raw := range set["rules"].([]any) {
			r, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("规则无效")
			}
			address, e := strconv.Atoi(fmt.Sprint(r["address"]))
			if e != nil {
				return nil, e
			}
			rules[address] = r
		}
	}
	ruleAddresses := []int{}
	for p := range rules {
		ruleAddresses = append(ruleAddresses, p)
	}
	sort.Ints(ruleAddresses)
	ruleList := []any{}
	for _, p := range ruleAddresses {
		ruleList = append(ruleList, rules[p])
	}
	out := map[string]any{"pins": pinList, "labels": labels, "rules": ruleList}
	check := copyRequest(source)
	for key, value := range out {
		check.Params[key] = value
	}
	if _, err := engine.ExportMTUIRegisters(check); err != nil {
		return nil, err
	}
	return out, nil
}
func (v *inspector) modbusImportForm() {
	if v.owner.running {
		v.owner.modal("请先停止采样再导入标注")
		return
	}
	form := tview.NewForm().AddInputField("JSON文件(留空使用粘贴)", "", 70, nil, nil).AddTextArea("粘贴MTUI JSON", "", 75, 15, 4<<20, nil)
	close := func() { v.owner.pages.RemovePage("modbus-import"); v.owner.App.SetFocus(v.table) }
	form.AddButton("读取并预览", func() {
		data := []byte(form.GetFormItem(1).(*tview.TextArea).GetText())
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		var err error
		if path != "" {
			data, err = modbusReadImport(path)
		}
		if err != nil {
			form.SetTitle("读取失败：" + display(err.Error()))
			return
		}
		r, err := v.modbusSavedRequest()
		if err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		incoming, err := engine.ImportMTUIRegisters(data, r.Action)
		if err != nil {
			form.SetTitle("导入失败：" + display(err.Error()))
			return
		}
		merged, err := modbusMergeAnnotations(r, incoming)
		if err != nil {
			form.SetTitle("合并失败：" + display(err.Error()))
			return
		}
		close()
		v.modbusImportPreview(merged)
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" 导入固定项/标签/规则 · 仅当前寄存器空间 · 不连接设备 ")
	v.owner.pages.AddPage("modbus-import", form, true, true)
	v.owner.App.SetFocus(form)
}
func (v *inspector) modbusImportPreview(merged map[string]any) {
	body, _ := json.MarshalIndent(merged, "", "  ")
	view := tview.NewTextView().SetText(clean(string(body))).SetScrollable(true).SetWrap(true)
	view.SetBorder(true).SetTitle(" 合并预览 · 同地址标签/规则覆盖，其他保留 · 保存仅改本机请求文件 ")
	close := func() { v.owner.pages.RemovePage("modbus-import-preview"); v.owner.App.SetFocus(v.table) }
	buttons := tview.NewForm().AddButton("保存合并", func() {
		if err := v.modbusSaveParams(merged); err != nil {
			view.SetTitle("保存失败：" + display(err.Error()))
			return
		}
		v.pins = map[int]bool{}
		for _, p := range merged["pins"].([]any) {
			v.pins[p.(int)] = true
		}
		v.labels = map[int]string{}
		for key, value := range merged["labels"].(map[string]any) {
			p, _ := strconv.Atoi(key)
			v.labels[p] = fmt.Sprint(value)
		}
		for address := range v.values {
			v.annotationsSeen[address] = true
		}
		close()
		v.renderRegisters()
		v.owner.setStatus("标注已合并保存；新规则在下次读取时生效，未写入设备")
	}).AddButton("取消", close)
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(v.modbusPanelKeys(close, []tview.Primitive{view}, buttons))
	v.owner.pages.AddPage("modbus-import-preview", panel, true, true)
	v.owner.App.SetFocus(view)
}
func (v *inspector) modbusDumpCSV() ([]byte, error) {
	if len(v.values) == 0 {
		return nil, fmt.Errorf("没有已读取数据")
	}
	columns := []string{"address", "time", "u16", "label", "custom"}
	if v.modbus != nil && len(v.modbus.columns) > 0 {
		columns = append([]string{}, v.modbus.columns...)
		seen := map[string]bool{}
		for _, key := range columns {
			seen[key] = true
		}
		for _, key := range []string{"address", "u16", "time"} {
			if !seen[key] {
				columns = append(columns, key)
			}
		}
	}
	var b bytes.Buffer
	writer := csv.NewWriter(&b)
	header := append([]string{"type", "captured_utc"}, columns...)
	if err := writer.Write(header); err != nil {
		return nil, err
	}
	addresses := []int{}
	for address := range v.values {
		addresses = append(addresses, address)
	}
	sort.Ints(addresses)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	kind := modbusCSVType(v.modbus.request.Action)
	if kind == "" {
		return nil, fmt.Errorf("当前动作没有可导出的读取空间")
	}
	for _, address := range addresses {
		row := []string{kind, now}
		for _, key := range columns {
			value := v.modbusCell(address, key)
			if key == "time" {
				if at, ok := v.values[address]["sampled_at"].(time.Time); ok {
					value = at.UTC().Format(time.RFC3339Nano)
				}
			}
			if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
				if _, err := strconv.ParseFloat(value, 64); err != nil {
					value = "'" + value
				}
			}
			row = append(row, value)
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return b.Bytes(), writer.Error()
}
func (v *inspector) modbusExportForm(csvDump bool) {
	title, path := "导出MTUI标注JSON", "modbus-annotations.json"
	if csvDump {
		title, path = "导出已读取数据CSV", "modbus-dump.csv"
	}
	form := tview.NewForm().AddInputField("新建私有文件路径", path, 70, nil, nil)
	close := func() { v.owner.pages.RemovePage("modbus-export"); v.owner.App.SetFocus(v.table) }
	form.AddButton("导出", func() {
		var data []byte
		var err error
		if csvDump {
			data, err = v.modbusDumpCSV()
		} else {
			var r config.Request
			r, err = v.modbusSavedRequest()
			if err == nil {
				data, err = engine.ExportMTUIRegisters(r)
			}
		}
		if err == nil {
			err = modbusWritePrivate(form.GetFormItem(0).(*tview.InputField).GetText(), data)
		}
		if err != nil {
			form.SetTitle("导出失败（不会覆盖）：" + display(err.Error()))
			return
		}
		close()
		v.owner.setStatus(title + "完成；文件仅在本机，不包含设备连接配置")
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" " + title + " · 不覆盖已有文件 · Esc取消 ")
	v.owner.pages.AddPage("modbus-export", form, true, true)
	v.owner.App.SetFocus(form)
}

// Keep every button reachable by keyboard, including after mouse focus changes.
func (v *inspector) modbusPanelKeys(close func(), panes []tview.Primitive, buttons *tview.Form) func(*tcell.EventKey) *tcell.EventKey {
	return func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() != tcell.KeyTab && e.Key() != tcell.KeyBacktab {
			return e
		}
		current := 0
		for i, pane := range panes {
			if pane.HasFocus() {
				current = i
			}
		}
		if buttons.HasFocus() {
			_, index := buttons.GetFocusedItemIndex()
			if index < 0 {
				index = 0
			}
			current = len(panes) + index
		}
		total := len(panes) + buttons.GetButtonCount()
		delta := 1
		if e.Key() == tcell.KeyBacktab {
			delta = total - 1
		}
		next := (current + delta) % total
		if next < len(panes) {
			v.owner.App.SetFocus(panes[next])
		} else {
			buttons.SetFocus(next - len(panes))
			v.owner.App.SetFocus(buttons)
		}
		return nil
	}
}
