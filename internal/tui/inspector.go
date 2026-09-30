package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/rivo/tview"
	"github.com/twmb/franz-go/pkg/kadm"
)

// inspector presents protocol-native results without changing the shared layout.
// Source recipes remain untouched by transient browse/navigation operations.
type inspector struct {
	mqttHistory     *mqttHistoryState
	modbus          *modbusAdvanced
	kafka           *kafkaView
	pages           *tview.Pages
	tree            *tview.TreeView
	table           *tview.Table
	root            *tview.TreeNode
	topics          map[string]*tview.TreeNode
	values          map[int]map[string]any
	history         map[int][]float64
	baseline        map[int]uint16
	pins            map[int]bool
	labels          map[int]string
	rows            []int
	annotationsSeen map[int]bool
	protocol, scope string
	filtered        bool
	matrix          bool
	matrixColumns   int
	owner           *UI
}

func newInspector(u *UI) *inspector {
	v := &inspector{owner: u, pages: tview.NewPages(), tree: tview.NewTreeView(), table: tview.NewTable().SetSelectable(true, true).SetFixed(1, 0), pins: map[int]bool{}, labels: map[int]string{}}
	v.tree.SetBorder(true)
	v.table.SetBorder(true)
	v.pages.AddPage("tree", v.tree, true, true).AddPage("table", v.table, true, false)
	v.tree.SetSelectedFunc(func(n *tview.TreeNode) {
		if id, ok := n.GetReference().(string); ok && v.protocol == "opcua" {
			u.derived("browse", id)
		} else {
			n.SetExpanded(!n.IsExpanded())
		}
	})
	v.tree.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if v.protocol == "mqtt" {
			e = v.mqttHistoryKey(e)
			if e == nil {
				return nil
			}
		}
		if v.protocol == "mqtt" && e.Rune() == 'v' {
			if node := v.tree.GetCurrentNode(); node != nil {
				if message, ok := node.GetReference().(map[string]any); ok {
					v.messageView(message)
				}
			}
			return nil
		}
		if v.protocol == "opcua" {
			n := v.tree.GetCurrentNode()
			if n != nil {
				if id, ok := n.GetReference().(string); ok {
					switch e.Rune() {
					case 'p':
						if !u.running {
							u.derived("node-path", id)
							u.uaCopyPending = true
						}
						return nil
					case 'v':
						if !u.running {
							u.derived("read", id)
							u.uaCopyPending = true
						}
						return nil
					case 'n':
						u.copyText(id)
						return nil
					case 'g':
						u.uaPathForm()
						return nil
					case 'c':
						u.derived("method-arguments", id)
						return nil
					case 'a':
						u.derived("attributes", id)
						return nil
					case 'f':
						u.derived("references", id)
						return nil
					case 'r':
						u.derived("read", id)
						return nil
					case 'S':
						u.unsubscribeUA(id)
						return nil
					case 's':
						u.subscribeUA(id)
						return nil
					}
				}
			}
			if e.Key() == tcell.KeyBackspace || e.Key() == tcell.KeyBackspace2 {
				u.back()
				return nil
			}
		}
		return e
	})
	v.table.SetSelectedFunc(func(row, col int) {
		if v.protocol == "opcua" && row > 0 {
			if data, ok := v.table.GetCell(row, 0).GetReference().(map[string]any); ok {
				if _, endpoint := data["url"]; endpoint {
					u.uaConnectionForm(u.lastRequest, data)
					return
				}
			}
		}
		if v.protocol == "kafka" {
			v.kafkaSelect(row, col)
			return
		}
	})
	v.table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyCtrlY {
			row, _ := v.table.GetSelection()
			if row > 0 {
				values := []string{}
				for c := 0; c < v.table.GetColumnCount(); c++ {
					values = append(values, tview.Unescape(v.table.GetCell(row, c).Text))
				}
				u.copyText(strings.Join(values, "\t"))
			}
			return nil
		}
		if v.protocol == "kafka" {
			return v.kafkaKey(e)
		}
		if v.protocol == "opcua" && (e.Key() == tcell.KeyBackspace || e.Key() == tcell.KeyBackspace2) {
			u.back()
			return nil
		}
		if v.protocol == "opcua" && e.Rune() == 'e' {
			row, _ := v.table.GetSelection()
			if row > 0 {
				if data, ok := v.table.GetCell(row, 0).GetReference().(map[string]any); ok {
					u.attributeForm(data)
				}
			}
			return nil
		}
		if v.protocol != "modbus" {
			return e
		}
		e = v.modbusAdvancedKey(e)
		if e == nil {
			return nil
		}
		if e.Rune() == 'm' {
			v.matrix = !v.matrix
			v.renderRegisters()
			return nil
		}
		if v.matrix && (e.Rune() == '+' || e.Rune() == '-') {
			if e.Rune() == '+' && v.matrixColumns < 16 {
				v.matrixColumns++
			}
			if e.Rune() == '-' && v.matrixColumns > 1 {
				v.matrixColumns--
			}
			v.renderRegisters()
			return nil
		}
		row, col := v.table.GetSelection()
		if row < 1 || (!v.matrix && row > len(v.rows)) {
			return e
		}
		address := 0
		if v.matrix {
			var ok bool
			address, ok = v.table.GetCell(row, col).GetReference().(int)
			if !ok {
				return e
			}
		} else {
			address = v.rows[row-1]
		}
		switch e.Rune() {
		case 'S':
			v.snapshot(false)
			return nil
		case 'O':
			v.snapshot(true)
			return nil
		case 'p':
			v.pins[address] = !v.pins[address]
			if e := v.persistAnnotations(); e != nil {
				v.owner.setStatus("固定项保存失败：" + e.Error())
			}
			v.renderRegisters()
			return nil
		case 'l':
			v.label(address)
			return nil
		case 'f':
			v.filtered = !v.filtered
			v.renderRegisters()
			return nil
		case 'd':
			v.baseline = map[int]uint16{}
			for a, r := range v.values {
				if n, ok := r["u16"].(uint16); ok {
					v.baseline[a] = n
				}
			}
			v.renderRegisters()
			return nil
		}
		return e
	})
	return v
}
func (v *inspector) reset(r config.Request) {
	defer v.mqttHistoryReset(r)
	defer v.modbusAdvancedReset(r)
	defer func() {
		if r.Protocol == "kafka" {
			v.kafkaReset(r)
		}
	}()
	v.protocol = r.Protocol
	v.matrixColumns = r.Int("matrix_columns", 8)
	if v.matrixColumns < 1 || v.matrixColumns > 16 {
		v.matrixColumns = 8
	}
	v.annotationsSeen = map[int]bool{}
	v.pins = map[int]bool{}
	v.labels = map[int]string{}
	scope := r.Endpoint + "/" + strconv.Itoa(r.Int("unit", 1))
	if scope != v.scope {
		v.pins = map[int]bool{}
		v.labels = map[int]string{}
		v.filtered = false
	}
	v.scope = scope
	if pins, ok := r.Params["pins"].([]any); ok {
		v.pins = map[int]bool{}
		for _, pin := range pins {
			if address, ok := pin.(int); ok {
				v.pins[address] = true
			}
		}
	}
	if labels, ok := r.Params["labels"].(map[string]any); ok {
		v.labels = map[int]string{}
		for key, value := range labels {
			if address, e := strconv.Atoi(key); e == nil {
				v.labels[address] = fmt.Sprint(value)
			}
		}
	}
	v.root = tview.NewTreeNode(display(strings.ToUpper(r.Protocol) + " · " + r.Action)).SetColor(tcell.ColorAqua)
	v.tree.SetRoot(v.root).SetCurrentNode(v.root)
	v.tree.SetTitle(" 结构化结果 Structured results · F2 原始 ")
	v.topics = map[string]*tview.TreeNode{}
	v.values = map[int]map[string]any{}
	v.history = map[int][]float64{}
	v.baseline = map[int]uint16{}
	v.rows = nil
	v.table.Clear()
	v.pages.SwitchToPage("tree")
	if r.Protocol == "opcua" {
		v.root.SetReference(r.String("node_id", "i=85"))
		v.tree.SetTitle(" OPC UA 节点 · Enter 浏览 · a 属性 · f 引用 · r 读 · s 订阅 ")
	}
	if r.Protocol == "mqtt" {
		v.tree.SetTitle(" MQTT topic tree · v载荷 · h历史 · g图表 · /搜索 · o/O展开/折叠 ")
	}
	if r.Protocol == "modbus" {
		v.pages.SwitchToPage("table")
		v.table.SetTitle(" 寄存器 · p 固定 · l 标签 · f 筛选 · d 快照 · S 保存 · O 对比 · F2 原始 ")
	}
}
func (v *inspector) add(e engine.Event) {
	if v.protocol == "modbus" {
		e = v.modbusMoreEvent(e)
	}
	if v.protocol == "kafka" && v.kafkaEvent(e) {
		return
	}
	switch e.Kind {
	case "browse-path":
		if m, ok := e.Data.(map[string]any); ok {
			v.owner.uaCopyValue = fmt.Sprint(m["path"])
		}
	case "value":
		if v.protocol == "opcua" {
			if m, ok := e.Data.(map[string]any); ok {
				b, _ := json.Marshal(m["value"])
				v.owner.uaCopyValue = string(b)
			}
		}
	case "endpoint":
		if v.protocol == "opcua" {
			if m, ok := e.Data.(map[string]any); ok {
				v.pages.SwitchToPage("table")
				v.table.SetTitle(" OPC UA 发现（未受信）· Enter 选择并编辑连接 · F9历史 ")
				if v.table.GetRowCount() == 0 {
					for i, h := range []string{"端点", "策略", "模式", "身份"} {
						v.table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false))
					}
				}
				row := v.table.GetRowCount()
				for i, key := range []string{"url", "security_policy", "security_mode", "identity_tokens"} {
					value := fmt.Sprint(m[key])
					if key == "security_policy" {
						value = strings.TrimPrefix(value, "http://opcfoundation.org/UA/SecurityPolicy#")
					}
					v.table.SetCell(row, i, tview.NewTableCell(display(value)).SetReference(m))
				}
				return
			}
		}
	case "path-resolved":
		if m, ok := e.Data.(map[string]any); ok {
			node := fmt.Sprint(m["node_id"])
			v.owner.lastRequest.Params["node_id"] = node
			v.root.SetReference(node)
			v.root.SetText(display(fmt.Sprint(m["path"]) + " · " + node))
			return
		}

	case "retained-preview":
		if m, ok := e.Data.(map[string]any); ok {
			v.owner.retainedPreview(m)
		}
		return
	case "message":
		if v.protocol == "mqtt" {
			m, ok := e.Data.(map[string]any)
			if !ok {
				return
			}
			v.mqttHistoryAdd(m)
			topic := fmt.Sprint(m["topic"])

			parent := v.root
			path := ""
			for _, part := range strings.Split(topic, "/") {
				path += "/" + part
				n, ok := v.topics[path]
				if !ok {
					if len(v.topics) >= 1024 {
						v.owner.setStatus("主题树已达到 1024 个节点上限，原始消息继续接收")
						return
					}
					n = tview.NewTreeNode(display(part)).SetSelectable(true)
					v.topics[path] = n
					parent.AddChild(n)
				}
				parent = n
			}
			payload := clean(fmt.Sprint(m["payload"]))
			if len(payload) > 120 {
				payload = payload[:120] + "…"
			}
			leaf := topic[strings.LastIndex(topic, "/")+1:]
			parent.SetText(display(fmt.Sprintf("%s = %s  [QoS %v, retain %v]", leaf, payload, m["qos"], m["retained"])))
			preview := map[string]any{}
			for _, key := range []string{"topic", "payload", "payload_hex", "payload_base64", "qos", "retained", "bytes", "payload_format"} {
				value := fmt.Sprint(m[key])
				if len(value) > 4096 {
					value = value[:4096] + "…（完整内容请用CLI）"
				}
				preview[key] = value
			}
			for _, key := range []string{"payload_json", "payload_messagepack"} {
				if value, ok := m[key]; ok {
					b, _ := json.MarshalIndent(value, "", "  ")
					preview[key] = mqttHistoryClip(string(b), 4096)
				}
			}
			parent.SetReference(preview)
			return
		}
	case "method-arguments":
		if m, ok := e.Data.(map[string]any); ok {
			v.owner.methodArgumentsPending = m
		}
		return
	case "attribute":
		if m, ok := e.Data.(map[string]any); ok {
			v.pages.SwitchToPage("table")
			v.table.SetTitle(" OPC UA 属性 · e 编辑 · F2 原始结果 · 退格返回 ")
			headers := []string{"节点", "属性", "值", "类型", "状态"}
			if v.table.GetRowCount() == 0 {
				for i, h := range headers {
					v.table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
				}
			}
			row := v.table.GetRowCount()
			values := []any{m["node_id"], m["attribute"], m["value"], m["value_type"], m["status"]}
			for i, x := range values {
				v.table.SetCell(row, i, tview.NewTableCell(display(fmt.Sprint(x))).SetReference(m))
			}
			return
		}
	case "reference":
		if ref, ok := e.Data.(*ua.ReferenceDescription); ok && ref.NodeID != nil && ref.NodeID.NodeID != nil {
			name := ref.NodeID.NodeID.String()
			if ref.DisplayName != nil && ref.DisplayName.Text != "" {
				name = ref.DisplayName.Text + "  (" + name + ")"
			}
			if len(v.root.GetChildren()) < 2000 {
				v.root.AddChild(tview.NewTreeNode(display(name)).SetReference(ref.NodeID.NodeID.String()).SetSelectable(true))
			}
			return
		}
	case "registers":
		if rows, ok := e.Data.([]map[string]any); ok {
			for _, row := range rows {
				address, ok := row["address"].(int)
				if !ok {
					continue
				}
				v.values[address] = row
				if !v.annotationsSeen[address] {
					if label, ok := row["label"].(string); ok {
						v.labels[address] = label
					}
					if pinned, ok := row["pinned"].(bool); ok {
						v.pins[address] = pinned
					}
					v.annotationsSeen[address] = true
				}
				if n, ok := row["u16"].(uint16); ok {
					if _, exists := v.baseline[address]; !exists {
						v.baseline[address] = n
					}
					h := append(v.history[address], float64(n))
					if len(h) > 32 {
						h = h[len(h)-32:]
					}
					v.history[address] = h
				}
			}
			v.renderRegisters()
			return
		}
	case "topics":
		if topics, ok := e.Data.(kadm.TopicDetails); ok {
			v.pages.SwitchToPage("table")
			v.table.SetTitle(" Kafka 主题 · Enter 只读消费 · F2 原始 ")
			headers := []string{"主题", "分区数", "内部主题", "错误"}
			for i, s := range headers {
				v.table.SetCell(0, i, tview.NewTableCell(s).SetSelectable(false).SetTextColor(tcell.ColorAqua))
			}
			names := make([]string, 0, len(topics))
			for name := range topics {
				names = append(names, name)
			}
			sort.Strings(names)
			for i, name := range names {
				d := topics[name]
				values := []string{name, strconv.Itoa(len(d.Partitions)), strconv.FormatBool(d.IsInternal), fmt.Sprint(d.Err)}
				for j, s := range values {
					v.table.SetCell(i+1, j, tview.NewTableCell(display(s)).SetReference(name))
				}
			}
			return
		}
	}
	b, err := json.Marshal(e.Data)
	if err != nil {
		return
	}
	if len(b) > 32768 {
		b = b[:32768]
	}
	if len(v.root.GetChildren()) >= 128 {
		children := v.root.GetChildren()
		v.root.SetChildren(children[1:])
	}
	var data any
	if json.Unmarshal(b, &data) == nil {
		budget := 300
		v.root.AddChild(valueTree(e.Kind, data, 0, &budget))
	} else {
		v.root.AddChild(tview.NewTreeNode(display(e.Kind + ": " + string(b))).SetSelectable(true))
	}
}
func (v *inspector) renderRegisters() {
	if v.renderModbusAdvanced() {
		return
	}
	v.table.Clear()
	v.table.SetTitle(" 寄存器 · m 矩阵 · p 固定 · l 标签 · f 筛选 · d 快照 · S 保存 · O 对比 " + v.modbusAdvancedHelp())
	headers := []string{"固定", "地址", "标签", "u16", "i16", "十六进制", "f32", "快照差值", "趋势(u16)", "f64", "u32 M10K", "i32 M10K", "规则结果"}
	for i, s := range headers {
		v.table.SetCell(0, i, tview.NewTableCell(s).SetTextColor(tcell.ColorAqua).SetSelectable(false))
	}
	v.rows = nil
	for address := range v.values {
		if !v.filtered || v.pins[address] {
			v.rows = append(v.rows, address)
		}
	}
	sort.Ints(v.rows)
	if v.matrix {
		v.renderRegisterMatrix()
		return
	}
	for i, address := range v.rows {
		r := v.values[address]
		pin := ""
		if v.pins[address] {
			pin = "*"
		}
		delta := ""
		if value, ok := r["u16"].(uint16); ok {
			if old, ok := v.baseline[address]; ok {
				delta = fmt.Sprintf("%+d", int(value)-int(old))
			} else {
				delta = "新增"
			}
		}
		f32 := ""
		if value, ok := r["f32"]; ok {
			f32 = fmt.Sprint(value)
		}
		cells := []string{pin, strconv.Itoa(address), v.labels[address], fmt.Sprint(r["u16"]), fmt.Sprint(r["i16"]), fmt.Sprint(r["hex"]), f32, delta, spark(v.history[address]), optional(r, "f64"), optional(r, "u32_m10k"), optional(r, "i32_m10k"), optional(r, "custom")}
		for col, s := range cells {
			v.table.SetCell(i+1, col, tview.NewTableCell(display(s)))
		}
	}
}
func spark(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	low, high := values[0], values[0]
	for _, v := range values {
		if v < low {
			low = v
		}
		if v > high {
			high = v
		}
	}
	runes := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range values {
		i := 0
		if high > low {
			i = int((v - low) / (high - low) * 7)
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}
func (v *inspector) label(address int) {
	f := tview.NewForm().AddInputField("标签", v.labels[address], 40, nil, nil)
	f.AddButton("保存", func() {
		v.labels[address] = f.GetFormItem(0).(*tview.InputField).GetText()
		if e := v.persistAnnotations(); e != nil {
			f.SetTitle("标签保存失败：" + clean(e.Error()))
			return
		}
		v.owner.pages.RemovePage("label")
		v.renderRegisters()
		v.owner.App.SetFocus(v.table)
	}).AddButton("取消", func() { v.owner.pages.RemovePage("label"); v.owner.App.SetFocus(v.table) })
	f.SetBorder(true).SetTitle(fmt.Sprintf(" 寄存器 %d 标签（保存到当前请求配置） ", address))
	f.SetCancelFunc(func() { v.owner.pages.RemovePage("label"); v.owner.App.SetFocus(v.table) })
	v.owner.pages.AddPage("label", f, true, true)
	v.owner.App.SetFocus(f)
}
func (u *UI) derived(action, target string) {
	if u.running {
		u.setStatus("请先按 F8 取消当前操作，再进行浏览")
		return
	}
	r := u.lastRequest
	params := map[string]any{}
	for k, v := range r.Params {
		params[k] = v
	}
	r.Params = params
	r.Action = action
	if r.Protocol == "opcua" {
		parent := r.String("node_id", "i=85")
		for _, key := range []string{"node_ids", "method_id", "object_id", "arguments", "attribute", "attributes", "value", "value_type", "browse_path", "direction", "reference_type"} {
			delete(r.Params, key)
		}
		if action == "method-arguments" {
			r.Params["object_id"] = parent
		}
		delete(r.Params, "node_ids")
		r.Params["node_id"] = target
		if action == "subscribe" {
			r.Timeout = "5m"
			r.Params["max_events"] = 1000
		}
		if !u.lastRequest.Mutates() {
			u.navigation = append(u.navigation, u.lastRequest)
		}
	} else if r.Protocol == "kafka" {
		r.Params["topic"] = target
		r.Params["limit"] = 100
		r.Timeout = "30s"
	}
	u.visual = true
	u.resultPages.SwitchToPage("visual")
	u.start(r)
	u.detail.SetText(clean("临时浏览（不会改写配置文件）\n协议：" + r.Protocol + "\n操作：" + r.Action + "\n服务地址：" + r.Endpoint + "\n目标：" + target))
}
func (u *UI) back() {
	if u.running || len(u.navigation) == 0 {
		return
	}
	r := u.navigation[len(u.navigation)-1]
	u.navigation = u.navigation[:len(u.navigation)-1]
	if r.Mutates() {
		u.setStatus("返回历史中的修改操作不会自动重放，请重新明确选择并确认")
		return
	}
	u.start(r)
}

func display(s string) string { return tview.Escape(clean(s)) }

func valueTree(label string, value any, depth int, budget *int) *tview.TreeNode {
	*budget--
	n := tview.NewTreeNode(display(label)).SetSelectable(true)
	if depth >= 8 || *budget <= 0 {
		n.SetText(display(label + "：…（请查看原始结果）"))
		return n
	}
	switch x := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if *budget <= 0 {
				break
			}
			n.AddChild(valueTree(k, x[k], depth+1, budget))
		}
	case []any:
		for i, v := range x {
			if *budget <= 0 {
				break
			}
			n.AddChild(valueTree(strconv.Itoa(i), v, depth+1, budget))
		}
	default:
		text := fmt.Sprint(value)
		if len(text) > 240 {
			text = text[:240] + "…"
		}
		n.SetText(display(label + ": " + text))
	}
	n.SetExpanded(depth < 2)
	return n
}

func optional(r map[string]any, key string) string {
	if value, ok := r[key]; ok {
		return fmt.Sprint(value)
	}
	return ""
}

func (v *inspector) messageView(m map[string]any) {
	text := fmt.Sprintf("主题：%v\n字节数：%v · QoS：%v · 保留：%v\n\n文本：\n%v\n\n十六进制：\n%v\n\nBase64：\n%v", m["topic"], m["bytes"], m["qos"], m["retained"], m["payload"], m["payload_hex"], m["payload_base64"])
	if value, ok := m["payload_json"]; ok {
		text += "\n\nJSON：\n" + fmt.Sprint(value)
	}
	if value, ok := m["payload_messagepack"]; ok {
		text += "\n\nMessagePack：\n" + fmt.Sprint(value)
	}
	view := tview.NewTextView().SetText(clean(text)).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" MQTT 多表示载荷 · Esc 返回 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			v.owner.pages.RemovePage("mqtt-payload")
			v.owner.App.SetFocus(v.tree)
			return nil
		}
		return e
	})
	v.owner.pages.AddPage("mqtt-payload", view, true, true)
	v.owner.App.SetFocus(view)
}

func (v *inspector) renderRegisterMatrix() {
	v.table.Clear()
	columns := v.matrixColumns
	if columns < 1 {
		columns = 8
		v.matrixColumns = columns
	}
	v.table.SetTitle(" 寄存器矩阵 · m 返回表格 · +/- 调整列数 · p 固定 · l 标签 ")
	for i := 0; i < columns; i++ {
		v.table.SetCell(0, i, tview.NewTableCell(fmt.Sprintf("列 %d", i+1)).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	for i, address := range v.rows {
		value := v.values[address]
		pin := ""
		if v.pins[address] {
			pin = "*"
		}
		label := v.labels[address]
		if label != "" {
			label = " " + label
		}
		text := fmt.Sprintf("%s%d%s = %v", pin, address, label, value["u16"])
		v.table.SetCell(i/columns+1, i%columns, tview.NewTableCell(display(text)).SetReference(address))
	}
}
