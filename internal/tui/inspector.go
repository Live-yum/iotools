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
		if v.protocol == "opcua" {
			n := v.tree.GetCurrentNode()
			if n != nil {
				if id, ok := n.GetReference().(string); ok {
					switch e.Rune() {
					case 'r':
						u.derived("read", id)
						return nil
					case 's':
						u.derived("subscribe", id)
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
		if v.protocol == "kafka" && row > 0 {
			topic, ok := v.table.GetCell(row, 0).GetReference().(string)
			if !ok {
				return
			}
			u.derived("consume", topic)
		}
	})
	v.table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if v.protocol != "modbus" {
			return e
		}
		row, _ := v.table.GetSelection()
		if row < 1 || row > len(v.rows) {
			return e
		}
		address := v.rows[row-1]
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
	v.protocol = r.Protocol
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
		v.tree.SetTitle(" OPC UA 节点 · Enter 浏览 · r 读 · s 订阅 · 退格返回 ")
	}
	if r.Protocol == "mqtt" {
		v.tree.SetTitle(" MQTT 主题树 MQTT topic tree · Enter 展开/折叠 · F2 原始 ")
	}
	if r.Protocol == "modbus" {
		v.pages.SwitchToPage("table")
		v.table.SetTitle(" 寄存器 · p 固定 · l 标签 · f 筛选 · d 快照 · S 保存 · O 对比 · F2 原始 ")
	}
}
func (v *inspector) add(e engine.Event) {
	switch e.Kind {
	case "message":
		if v.protocol == "mqtt" {
			m, ok := e.Data.(map[string]any)
			if !ok {
				return
			}
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
	v.table.Clear()
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
		delete(r.Params, "node_ids")
		r.Params["node_id"] = target
		if action == "subscribe" {
			r.Timeout = "5m"
			r.Params["max_events"] = 1000
		}
		u.navigation = append(u.navigation, u.lastRequest)
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
