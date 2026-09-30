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
	protocol, scope string
	filtered        bool
	owner           *UI
}

func newInspector(u *UI) *inspector {
	v := &inspector{owner: u, pages: tview.NewPages(), tree: tview.NewTreeView(), table: tview.NewTable().SetSelectable(true, false).SetFixed(1, 0), pins: map[int]bool{}, labels: map[int]string{}}
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
		case 'p':
			v.pins[address] = !v.pins[address]
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
	scope := r.Endpoint + "/" + strconv.Itoa(r.Int("unit", 1))
	if scope != v.scope {
		v.pins = map[int]bool{}
		v.labels = map[int]string{}
		v.filtered = false
	}
	v.scope = scope
	v.root = tview.NewTreeNode(display(strings.ToUpper(r.Protocol) + " · " + r.Action)).SetColor(tcell.ColorAqua)
	v.tree.SetRoot(v.root).SetCurrentNode(v.root)
	v.tree.SetTitle(" Structured results • F2 raw ")
	v.topics = map[string]*tview.TreeNode{}
	v.values = map[int]map[string]any{}
	v.history = map[int][]float64{}
	v.baseline = map[int]uint16{}
	v.rows = nil
	v.table.Clear()
	v.pages.SwitchToPage("tree")
	if r.Protocol == "opcua" {
		v.root.SetReference(r.String("node_id", "i=85"))
		v.tree.SetTitle(" OPC UA nodes • Enter browse · r read · s watch · Backspace back ")
	}
	if r.Protocol == "mqtt" {
		v.tree.SetTitle(" MQTT topic tree • Enter expand/collapse · F2 raw ")
	}
	if r.Protocol == "modbus" {
		v.pages.SwitchToPage("table")
		v.table.SetTitle(" Registers • p pin · l label · f pins only · d snapshot · F2 raw ")
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
						v.owner.setStatus("Topic tree limit reached (1024 paths); raw events continue")
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
			v.table.SetTitle(" Kafka topics • Enter consumes read-only · F2 raw ")
			headers := []string{"Topic", "Partitions", "Internal", "Error"}
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
	headers := []string{"Pin", "Address", "Label", "u16", "i16", "Hex", "f32", "Δ snapshot", "Trend (u16)"}
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
			delta = fmt.Sprintf("%+d", int(value)-int(v.baseline[address]))
		}
		f32 := ""
		if value, ok := r["f32"]; ok {
			f32 = fmt.Sprint(value)
		}
		cells := []string{pin, strconv.Itoa(address), v.labels[address], fmt.Sprint(r["u16"]), fmt.Sprint(r["i16"]), fmt.Sprint(r["hex"]), f32, delta, spark(v.history[address])}
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
	f := tview.NewForm().AddInputField("Label", v.labels[address], 40, nil, nil)
	f.AddButton("Save", func() {
		v.labels[address] = f.GetFormItem(0).(*tview.InputField).GetText()
		v.owner.pages.RemovePage("label")
		v.renderRegisters()
		v.owner.App.SetFocus(v.table)
	}).AddButton("Cancel", func() { v.owner.pages.RemovePage("label"); v.owner.App.SetFocus(v.table) })
	f.SetBorder(true).SetTitle(fmt.Sprintf(" Label register %d (session only) ", address))
	f.SetCancelFunc(func() { v.owner.pages.RemovePage("label"); v.owner.App.SetFocus(v.table) })
	v.owner.pages.AddPage("label", f, true, true)
	v.owner.App.SetFocus(f)
}
func (u *UI) derived(action, target string) {
	if u.running {
		u.setStatus("Cancel the active operation with F8 before navigating")
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
	u.detail.SetText(clean("Transient navigation (collection unchanged)\nProtocol: " + r.Protocol + "\nAction: " + r.Action + "\nEndpoint: " + r.Endpoint + "\nTarget: " + target))
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
		n.SetText(display(label + ": … (use raw results)"))
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
