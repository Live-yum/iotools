package tui

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const mqttHistoryTopicLimit = 256
const mqttHistoryPerTopic = 128
const mqttHistoryEntryLimit = 2048
const mqttHistoryByteLimit = 8 << 20
const mqttHistoryValueLimit = 16 << 10

type mqttHistoryEntry struct {
	seq          uint64
	received     time.Time
	retained     bool
	qos          string
	size         int
	format, text string
	json         []byte
	binary       []byte
	truncated    bool
	cost         int
}
type mqttTopicHistory struct {
	entries []*mqttHistoryEntry
	last    uint64
	dropped uint64
}
type mqttHistoryState struct {
	topics           map[string]*mqttTopicHistory
	sequence         uint64
	count, bytes     int
	removed          uint64
	evicted          uint64
	panelTopic, path string
	selectors        []mqttSelector
	binaryIndex      int
	rendering        bool
	graph, follow    bool
	table            *tview.Table
	chart            *tview.TextView
	panes            *tview.Pages
	panel            *tview.Flex
}
type mqttSelector struct {
	key   string
	index int
	array bool
}

func (v *inspector) mqttHistoryReset(r config.Request) {
	v.mqttHistory = nil
	v.owner.pages.RemovePage("mqtt-history")
	v.owner.pages.RemovePage("mqtt-history-selector")
	v.owner.pages.RemovePage("mqtt-history-detail")
	v.owner.pages.RemovePage("mqtt-topic-search")
	if r.Protocol == "mqtt" {
		v.mqttHistory = &mqttHistoryState{topics: map[string]*mqttTopicHistory{}, path: "$", binaryIndex: -1, follow: true}
	}
}
func mqttHistoryClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…（截断）"
}
func mqttHistorySnapshot(m map[string]any, seq uint64) *mqttHistoryEntry {
	entry := &mqttHistoryEntry{seq: seq, received: time.Now().UTC(), qos: fmt.Sprint(m["qos"]), format: fmt.Sprint(m["payload_format"])}
	if text, ok := m["received_at"].(string); ok {
		if stamp, err := time.Parse(time.RFC3339Nano, text); err == nil {
			entry.received = stamp
		}
	}
	entry.retained, _ = m["retained"].(bool)
	entry.size, _ = strconv.Atoi(fmt.Sprint(m["bytes"]))
	entry.text = mqttHistoryClip(fmt.Sprint(m["payload"]), 8192)
	raw, ok := m["payload_json"]
	if ok {
		entry.format = "json"
	} else if raw, ok = m["payload_messagepack"]; ok {
		entry.format = "messagepack"
	}
	if ok {
		if b, err := json.Marshal(raw); err == nil {
			if len(b) <= mqttHistoryValueLimit && mqttHistoryJSONDepth(b) {
				entry.json = append([]byte(nil), b...)
				entry.text = mqttHistoryClip(string(b), 8192)
			} else {
				entry.truncated = true
				entry.text = mqttHistoryClip(string(b), 8192)
			}
		}
	}
	if entry.format == "" || entry.format == "<nil>" {
		entry.format = "text"
		if valid, ok := m["payload_utf8"].(bool); ok && !valid {
			entry.format = "binary"
		}
	}
	if encoded, ok := m["payload_hex"].(string); ok {
		n := len(encoded)
		if n > 8192 {
			n = 8192
			entry.truncated = true
		}
		entry.binary, _ = hex.DecodeString(encoded[:n])
	}
	if text, ok := m["payload"].(string); ok && len(text) > 8192 {
		entry.truncated = true
	}
	entry.cost = len(entry.text) + len(entry.json) + len(entry.binary) + 256
	return entry
}
func (h *mqttHistoryState) dropFirst(topic string) {
	history := h.topics[topic]
	if history == nil || len(history.entries) == 0 {
		return
	}
	entry := history.entries[0]
	h.bytes -= entry.cost
	h.count--
	h.evicted++
	history.dropped++
	copy(history.entries, history.entries[1:])
	history.entries[len(history.entries)-1] = nil
	history.entries = history.entries[:len(history.entries)-1]
	if len(history.entries) == 0 {
		h.bytes -= len(topic)
		delete(h.topics, topic)
	}
}
func (h *mqttHistoryState) evictOldest() {
	var oldest string
	var seq uint64 = ^uint64(0)
	for topic, history := range h.topics {
		if len(history.entries) > 0 && history.entries[0].seq < seq {
			oldest = topic
			seq = history.entries[0].seq
		}
	}
	h.dropFirst(oldest)
}
func (v *inspector) mqttHistoryAdd(m map[string]any) {
	h := v.mqttHistory
	if h == nil {
		return
	}
	topic, ok := m["topic"].(string)
	if !ok || len(topic) > 65535 {
		return
	}
	topic = strings.Clone(topic)
	h.sequence++
	entry := mqttHistorySnapshot(m, h.sequence)
	for len(h.topics) >= mqttHistoryTopicLimit && h.topics[topic] == nil {
		h.evictOldest()
	}
	history := h.topics[topic]
	if history == nil {
		history = &mqttTopicHistory{}
		h.topics[topic] = history
		h.bytes += len(topic)
	}
	if len(history.entries) >= mqttHistoryPerTopic {
		h.dropFirst(topic)
		history = h.topics[topic]
		if history == nil {
			history = &mqttTopicHistory{}
			h.topics[topic] = history
			h.bytes += len(topic)
		}
	}
	history.entries = append(history.entries, entry)
	history.last = entry.seq
	h.count++
	h.bytes += entry.cost
	for h.count > mqttHistoryEntryLimit || h.bytes > mqttHistoryByteLimit {
		h.evictOldest()
	}
	if h.panel != nil && h.panelTopic == topic {
		v.mqttHistoryRender()
	}
}
func (v *inspector) mqttHistoryKey(e *tcell.EventKey) *tcell.EventKey {
	if v.protocol != "mqtt" || v.mqttHistory == nil || e.Key() != tcell.KeyRune {
		return e
	}
	switch e.Rune() {
	case '/':
		v.mqttTopicSearch()
		return nil
	case 'o':
		v.mqttExpandAll(true)
		return nil
	case 'O':
		v.mqttExpandAll(false)
		return nil
	case 'h', 'g':
	default:
		return e
	}
	node := v.tree.GetCurrentNode()
	if node != nil {
		if m, ok := node.GetReference().(map[string]any); ok {
			if topic, ok := m["topic"].(string); ok {
				v.mqttHistoryOpen(topic, e.Rune() == 'g')
			}
		}
	}
	return nil
}
func (v *inspector) mqttHistoryOpen(topic string, graph bool) {
	h := v.mqttHistory
	if h == nil {
		return
	}
	if h.topics[topic] == nil {
		v.owner.setStatus("该主题历史已被内存上限淘汰，等待新消息后可再次查看")
		return
	}
	if h.panelTopic != topic {
		h.path = "$"
		h.selectors = nil
		h.binaryIndex = -1
	}
	h.panelTopic = topic
	latest := h.topics[topic].entries
	if h.binaryIndex < 0 && len(latest) > 0 && latest[len(latest)-1].format == "binary" {
		h.binaryIndex = 0
	}
	h.graph = graph
	h.follow = true
	h.table = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	h.table.SetBorder(true)
	h.table.SetSelectionChangedFunc(func(row, col int) {
		if !h.rendering {
			h.follow = false
		}
	})
	h.chart = tview.NewTextView().SetWrap(false).SetScrollable(true)
	h.chart.SetBorder(true)
	h.panes = tview.NewPages().AddPage("table", h.table, true, true).AddPage("graph", h.chart, true, false)
	hint := tview.NewTextView().SetText("h历史 g图表 s选择JSONPath/字节 f跟随最新 · 上下浏览暂停跟随 · Enter详情 Delete仅移除旧缓存 · Ctrl-Y复制选中值 · Esc关闭 · F8取消订阅")
	h.panel = tview.NewFlex().SetDirection(tview.FlexRow).AddItem(h.panes, 0, 1, true).AddItem(hint, 2, 0, false)
	close := func() {
		v.owner.pages.RemovePage("mqtt-history")
		h.panel = nil
		h.table = nil
		h.chart = nil
		h.panes = nil
		v.owner.App.SetFocus(v.tree)
	}
	h.panel.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyDelete, tcell.KeyBackspace, tcell.KeyBackspace2:
			if !h.graph {
				if entry := v.mqttHistorySelected(); entry != nil {
					if h.uncache(h.panelTopic, entry.seq) {
						v.mqttHistoryRender()
					} else {
						v.owner.setStatus("最新消息保留；仅可移除较早的本机缓存，不影响broker")
					}
				}
			}
			return nil
		case tcell.KeyF8:
			v.owner.stop()
			return nil
		case tcell.KeyUp, tcell.KeyPgUp, tcell.KeyDown, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
			h.follow = false
		case tcell.KeyCtrlY:
			if entry := v.mqttHistorySelected(); entry != nil {
				v.owner.copyText(v.mqttHistoryValue(entry))
			}
			return nil
		}
		switch e.Rune() {
		case 'h':
			h.graph = false
			v.mqttHistoryRender()
			v.owner.App.SetFocus(h.table)
			return nil
		case 'g':
			h.graph = true
			v.mqttHistoryRender()
			v.owner.App.SetFocus(h.chart)
			return nil
		case 's':
			v.mqttHistorySelectorForm()
			return nil
		case 'f':
			h.follow = true
			v.mqttHistoryRender()
			return nil
		}
		return e
	})
	h.table.SetSelectedFunc(func(row, col int) {
		h.follow = false
		if entry := v.mqttHistorySelected(); entry != nil {
			v.mqttHistoryDetail(entry)
		}
	})
	v.owner.pages.AddPage("mqtt-history", h.panel, true, true)
	v.mqttHistoryRender()
	if graph {
		v.owner.App.SetFocus(h.chart)
	} else {
		v.owner.App.SetFocus(h.table)
	}
}
func (v *inspector) mqttHistorySelected() *mqttHistoryEntry {
	h := v.mqttHistory
	if h == nil || h.table == nil {
		return nil
	}
	row, _ := h.table.GetSelection()
	if row < 1 || row >= h.table.GetRowCount() {
		return nil
	}
	entry, _ := h.table.GetCell(row, 0).GetReference().(*mqttHistoryEntry)
	return entry
}
func (h *mqttHistoryState) selectedValue(entry *mqttHistoryEntry) (any, bool) {
	if h.binaryIndex >= 0 {
		if h.binaryIndex >= len(entry.binary) {
			return nil, false
		}
		return int(entry.binary[h.binaryIndex]), true
	}
	if len(entry.json) > 0 {
		var raw any
		d := json.NewDecoder(bytes.NewReader(entry.json))
		d.UseNumber()
		if d.Decode(&raw) != nil {
			return nil, false
		}
		for _, selector := range h.selectors {
			if selector.array {
				values, ok := raw.([]any)
				if !ok || selector.index >= len(values) {
					return nil, false
				}
				raw = values[selector.index]
			} else {
				values, ok := raw.(map[string]any)
				if !ok {
					return nil, false
				}
				var found bool
				raw, found = values[selector.key]
				if !found {
					return nil, false
				}
			}
		}
		return raw, true
	}
	if len(h.selectors) > 0 || entry.truncated {
		return nil, false
	}
	return entry.text, true
}
func (v *inspector) mqttHistoryValue(entry *mqttHistoryEntry) string {
	value, ok := v.mqttHistory.selectedValue(entry)
	if !ok {
		return "（字段缺失或结构预览被截断）"
	}
	if text, ok := value.(string); ok {
		return text
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "（不可显示）"
	}
	return string(b)
}
func (v *inspector) mqttHistoryRender() {
	h := v.mqttHistory
	if h == nil || h.panel == nil {
		return
	}
	h.rendering = true
	defer func() { h.rendering = false }()
	history := h.topics[h.panelTopic]
	var entries []*mqttHistoryEntry
	if history != nil {
		entries = history.entries
	}
	selector := h.path
	if h.binaryIndex >= 0 {
		selector = fmt.Sprintf("byte[%d]", h.binaryIndex)
	}
	title := fmt.Sprintf(" MQTT %s · %s · 最近%d条 · 全局淘汰%d条 ", mqttHistoryClip(h.panelTopic, 80), selector, len(entries), h.evicted)
	if rate, ok := mqttHistoryRate(entries); ok {
		title += fmt.Sprintf(" %.2f消息/s ", rate)
	}
	old := v.mqttHistorySelected()
	h.table.Clear()
	h.table.SetTitle(display(title))
	headers := []string{"接收时间(UTC)", "QoS", "Retain", "Bytes", "格式", "选中值"}
	for col, text := range headers {
		h.table.SetCell(0, col, tview.NewTableCell(text).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	selected := 1
	for i, entry := range entries {
		if old != nil && old.seq == entry.seq {
			selected = i + 1
		}
		for col, text := range []string{entry.received.UTC().Format("15:04:05.000"), entry.qos, strconv.FormatBool(entry.retained), strconv.Itoa(entry.size), entry.format, mqttHistoryClip(v.mqttHistoryValue(entry), 512)} {
			h.table.SetCell(i+1, col, tview.NewTableCell(display(text)).SetReference(entry))
		}
	}
	if len(entries) > 0 {
		if h.follow {
			selected = len(entries)
		}
		h.table.Select(selected, 0)
	}
	if h.graph {
		h.panes.SwitchToPage("graph")
		h.chart.SetTitle(display(title))
		h.chart.SetText(mqttRenderGraph(h, entries))
	} else {
		h.panes.SwitchToPage("table")
	}
}
func (v *inspector) mqttHistoryDetail(entry *mqttHistoryEntry) {
	text := fmt.Sprintf("主题：%s\n接收时间：%s\nQoS：%s · retained：%t · 原始字节：%d · 格式：%s\n\n所选值：\n%s\n\n载荷预览：\n%s\n\n二进制前缀HEX：\n%s", v.mqttHistory.panelTopic, entry.received.Format(time.RFC3339Nano), entry.qos, entry.retained, entry.size, entry.format, v.mqttHistoryValue(entry), entry.text, hex.EncodeToString(entry.binary))
	if entry.truncated {
		text += "\n\n界面保留内容已截断，完整事件请使用CLI输出"
	}
	view := tview.NewTextView().SetText(clean(text)).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" MQTT历史消息 · Esc返回 · Ctrl-Y复制当前值 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			v.owner.pages.RemovePage("mqtt-history-detail")
			v.owner.App.SetFocus(v.mqttHistory.table)
			return nil
		}
		if e.Key() == tcell.KeyCtrlY {
			v.owner.copyText(v.mqttHistoryValue(entry))
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	v.owner.pages.AddPage("mqtt-history-detail", view, true, true)
	v.owner.App.SetFocus(view)
}

// Local history removal never removes the latest message and never publishes.
func (h *mqttHistoryState) uncache(topic string, seq uint64) bool {
	history := h.topics[topic]
	if history == nil {
		return false
	}
	for i, entry := range history.entries {
		if entry.seq == seq {
			if i == len(history.entries)-1 {
				return false
			}
			h.bytes -= entry.cost
			h.count--
			h.removed++
			copy(history.entries[i:], history.entries[i+1:])
			history.entries[len(history.entries)-1] = nil
			history.entries = history.entries[:len(history.entries)-1]
			return true
		}
	}
	return false
}

func (v *inspector) mqttExpandAll(expand bool) {
	for _, node := range v.topics {
		node.SetExpanded(expand)
	}
	v.root.SetExpanded(true)
}
func (v *inspector) mqttExpandTopic(topic string) {
	path := ""
	for _, part := range strings.Split(topic, "/") {
		path += "/" + part
		if node := v.topics[path]; node != nil {
			node.SetExpanded(true)
		}
	}
	v.root.SetExpanded(true)
}
func (v *inspector) mqttSearchTopics(query string) []string {
	out := []string{}
	seen := map[string]bool{}
	query = strings.ToLower(query)
	for _, node := range v.topics {
		if m, ok := node.GetReference().(map[string]any); ok {
			if topic, ok := m["topic"].(string); ok && !seen[topic] && strings.Contains(strings.ToLower(topic), query) {
				out = append(out, topic)
				seen[topic] = true
			}
		}
	}
	sort.Strings(out)
	return out
}
func (v *inspector) mqttTopicSearch() {
	input := tview.NewInputField().SetLabel("主题名称搜索: ")
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" ↑↓选择 · Enter展开匹配路径 · Tab切换 · Esc取消 · F8取消订阅 ")
	matches := []string{}
	close := func() { v.owner.pages.RemovePage("mqtt-topic-search"); v.owner.App.SetFocus(v.tree) }
	selectMatch := func() {
		index := list.GetCurrentItem()
		if index < 0 || index >= len(matches) {
			return
		}
		v.mqttExpandAll(false)
		for _, topic := range matches {
			v.mqttExpandTopic(topic)
		}
		node := v.topics["/"+matches[index]]
		if node != nil {
			v.tree.SetCurrentNode(node)
		}
		close()
	}
	input.SetChangedFunc(func(query string) {
		matches = v.mqttSearchTopics(query)
		list.Clear()
		for _, topic := range matches {
			list.AddItem(display(topic), "", 0, selectMatch)
		}
	})
	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			v.owner.App.SetFocus(list)
		}
	})
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(input, 1, 0, true).AddItem(list, 0, 1, false)
	panel.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyF8:
			v.owner.stop()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			if input.HasFocus() {
				v.owner.App.SetFocus(list)
			} else {
				v.owner.App.SetFocus(input)
			}
			return nil
		}
		return e
	})
	matches = v.mqttSearchTopics("")
	for _, topic := range matches {
		list.AddItem(display(topic), "", 0, selectMatch)
	}
	v.owner.pages.AddPage("mqtt-topic-search", panel, true, true)
	v.owner.App.SetFocus(input)
}

func mqttHistoryRate(entries []*mqttHistoryEntry) (float64, bool) {
	var first, last time.Time
	count := 0
	for _, entry := range entries {
		if entry.retained {
			continue
		}
		if count == 0 {
			first = entry.received
		}
		last = entry.received
		count++
	}
	seconds := last.Sub(first).Seconds()
	if count < 2 || seconds <= 0 {
		return 0, false
	}
	return float64(count-1) / seconds, true
}

// Bound structured preview nesting independently of the broker payload limit.
func mqttHistoryJSONDepth(data []byte) bool {
	depth := 0
	quoted, escaped := false, false
	for _, b := range data {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 32 {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return depth == 0 && !quoted
}
