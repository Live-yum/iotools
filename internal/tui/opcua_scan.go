package tui

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/rivo/tview"
)

// uaScanWorkspace is deliberately separate from the historical per-node subscriptions.
// One active operation owns one engine session, cancellation and generation at a time.
// All its view state is confined to tview's event goroutine.
type uaScanWorkspace struct {
	parent                                       *uaWorkspace
	base                                         config.Request
	root                                         *uaScanLayout
	body                                         *tview.Flex
	header, footer                               *tview.TextView
	tree                                         *tview.TreeView
	table                                        *tview.Table
	nodes                                        map[string]*uaScanNode
	variables                                    []string
	selected                                     map[string]bool
	focus, width, height, layoutFocus            int
	compact, closed, active, stopping            bool
	generation                                   uint64
	cancel                                       context.CancelFunc
	mode, status, rootNode                       string
	maxDepth, maxNodes, maxReferences, readBatch int
	complete                                     bool
	reason                                       string
	received, recorded                           int
	run                                          func(context.Context, config.Request, bool, engine.Emit) error
	newRecorder                                  func(string, string, string) (*uaScanRecorder, error)
	namespaceFingerprint                         string
	oneClickPending                              bool
	oneClickReport                               *uaOneClickReport
}
type uaScanNode struct {
	row      uaScanRow
	tree     *tview.TreeNode
	tableRow int
}
type uaScanLayout struct {
	*tview.Flex
	workspace *uaScanWorkspace
}

func (l *uaScanLayout) Draw(screen tcell.Screen) {
	_, _, width, height := l.GetRect()
	l.workspace.layout(width, height)
	l.Flex.Draw(screen)
}

// Only missing limits receive defaults. Match one-click validation rather than
// Request.Int, which truncates fractions and silently defaults malformed input.
func uaScanLimitValues(r config.Request) ([4]int, error) {
	var values [4]int
	for i, limit := range []struct {
		key           string
		def, min, max int
	}{
		{"max_depth", 0, 0, 256}, {"max_nodes", 10000, 1, 100000},
		{"max_references", 1000, 1, 100000}, {"read_batch_size", 100, 1, 1000},
	} {
		value := limit.def
		if raw, present := r.Params[limit.key]; present {
			var err error
			value, err = strconv.Atoi(fmt.Sprint(raw))
			if err != nil || value < limit.min || value > limit.max {
				return values, fmt.Errorf("%s 必须是 %d..%d 的整数", limit.key, limit.min, limit.max)
			}
		}
		values[i] = value
	}
	return values, nil
}

func (p *uaWorkspace) showScan() {
	if p.closed || p.owner.quitting {
		return
	}
	if p.scan != nil && !p.scan.closed {
		p.owner.pages.SwitchToPage("ua-scan")
		p.scan.focusPane(p.scan.focus)
		return
	}
	limits, err := uaScanLimitValues(p.base)
	if err != nil {
		p.owner.modal("扫描参数无效：" + err.Error() + "；请编辑请求配置后重试")
		return
	}
	if p.cancel != nil {
		p.cancel()
		p.generation++
		p.cancel = nil
		p.loading = false
	}
	w := &uaScanWorkspace{parent: p, base: copyRequest(p.base), rootNode: p.node, maxDepth: limits[0], maxNodes: limits[1], maxReferences: limits[2], readBatch: limits[3], run: p.run, layoutFocus: -1, status: "尚未连接 · r配置并显式开始扫描", reason: "尚未扫描"}
	w.newRecorder = newUAScanRecorder
	if root, err := ua.ParseNodeID(w.rootNode); err == nil {
		w.rootNode = root.String()
	}
	w.header = tview.NewTextView().SetWrap(false)
	w.footer = tview.NewTextView().SetWrap(false).SetText("Tab/1–2切换 · r配置/扫描 · 空格选择 · a全选已发现 · n清空\nv批量读值 · e导出CSV/JSON · x当前/X集合一键导出\ns订阅 l记录CSV/JSONL · S/F8停止 · i完整状态\nEnter节点详情 · Esc关闭并停止 · Ctrl+C退出")
	w.tree = tview.NewTreeView()
	w.tree.SetBorder(true).SetTitle(" 1 地址空间树 · 空格选择变量 ")
	w.table = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	w.table.SetBorder(true).SetTitle(" 2 已发现变量 · 空格选中 · a全选 ")
	w.body = tview.NewFlex()
	w.root = &uaScanLayout{Flex: tview.NewFlex().SetDirection(tview.FlexRow).AddItem(w.header, 4, 0, false).AddItem(w.body, 0, 1, true).AddItem(w.footer, 4, 0, false), workspace: w}
	w.tree.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey { return w.key(0, e) })
	w.table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey { return w.key(1, e) })
	w.table.SetSelectedFunc(func(row, col int) {
		if row > 0 && row <= len(w.variables) {
			w.detail(w.variables[row-1])
		}
	})
	w.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if id, ok := node.GetReference().(string); ok {
			w.detail(id)
		}
	})
	p.scan = w
	w.reset()
	w.layout(120, 40)
	p.owner.pages.AddPage("ua-scan", w.root, true, true)
	w.focusPane(0)
}
func (w *uaScanWorkspace) owner() *UI { return w.parent.owner }
func (w *uaScanWorkspace) reset() {
	w.nodes = map[string]*uaScanNode{}
	w.variables = nil
	w.selected = map[string]bool{}
	w.received, w.recorded = 0, 0
	w.complete = false
	w.namespaceFingerprint = ""
	w.oneClickReport = nil
	w.reason = "尚未完成扫描"
	w.tree.SetRoot(tview.NewTreeNode(display(w.rootNode)).SetReference(w.rootNode)).SetCurrentNode(nil)
	w.table.Clear()
	for col, h := range []string{"选", "名称", "NodeId", "值", "类型", "状态", "源时间戳", "服务端时间戳"} {
		w.table.SetCell(0, col, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	w.renderHeader()
}
func (w *uaScanWorkspace) layout(width, height int) {
	compact := width < 110 || height < 30
	if w.tree.HasFocus() {
		w.focus = 0
	} else if w.table.HasFocus() {
		w.focus = 1
	}
	if width == w.width && height == w.height && compact == w.compact && (!compact || w.focus == w.layoutFocus) {
		return
	}
	w.width, w.height, w.compact, w.layoutFocus = width, height, compact, w.focus
	w.body.Clear()
	if compact {
		if w.focus == 0 {
			w.body.AddItem(w.tree, 0, 1, true)
		} else {
			w.body.AddItem(w.table, 0, 1, true)
		}
	} else {
		w.body.SetDirection(tview.FlexColumn).AddItem(w.tree, 0, 1, w.focus == 0).AddItem(w.table, 0, 3, w.focus == 1)
	}
	w.renderHeader()
}
func (w *uaScanWorkspace) renderHeader() {
	mode := "树/变量双窗"
	if w.compact {
		mode = fmt.Sprintf("窄屏分页 %d/2", w.focus+1)
	}
	scope := "扫描不完整：" + w.reason
	if w.complete {
		scope = "配置范围内扫描完成"
	}
	// Keep the actual operation/error first, even on 80x24 terminals. Full
	// diagnostics and endpoint are always available with i, without clipping.
	w.header.SetText(clean(fmt.Sprintf("%s\nOPC UA 扫描 · %s · 节点%d / 变量%d / 选中%d · 读/通知%d / 记录%d\n%s\n%s · 根 %s · i查看完整状态/限制", w.status, mode, len(w.nodes), len(w.variables), len(w.selected), w.received, w.recorded, scope, w.base.Endpoint, w.rootNode)))
}
func (w *uaScanWorkspace) focusPane(pane int) {
	w.focus = (pane + 2) % 2
	if w.focus == 0 {
		w.owner().App.SetFocus(w.tree)
	} else {
		w.owner().App.SetFocus(w.table)
	}
	w.renderHeader()
}
func (w *uaScanWorkspace) close() {
	if w.closed {
		return
	}
	w.closed = true
	w.generation++
	if w.cancel != nil {
		w.cancel()
	}
	for _, name := range []string{"ua-scan", "ua-scan-form", "ua-scan-detail", "ua-oneclick-form", "ua-oneclick-confirm", "ua-oneclick-result"} {
		w.owner().pages.RemovePage(name)
	}
	if w.parent.scan == w {
		w.parent.scan = nil
	}
	if !w.parent.closed {
		w.parent.focusPane(w.parent.focus)
	}
}
func (w *uaScanWorkspace) stop() {
	if !w.active {
		return
	}
	w.stopping = true
	w.status = "正在停止并关闭连接/记录文件…"
	if w.cancel != nil {
		w.cancel()
	}
	w.renderHeader()
}
func (w *uaScanWorkspace) key(pane int, e *tcell.EventKey) *tcell.EventKey {
	w.focus = pane
	switch e.Key() {
	case tcell.KeyEscape:
		w.close()
		return nil
	case tcell.KeyTab, tcell.KeyBacktab:
		w.focusPane(pane + 1)
		return nil
	case tcell.KeyF8:
		w.stop()
		return nil
	}
	if e.Key() != tcell.KeyRune {
		return e
	}
	switch e.Rune() {
	case '1', '2':
		w.focusPane(int(e.Rune() - '1'))
		return nil
	case 'r':
		if !w.active {
			w.scanForm()
		} else {
			w.setStatus("请先S/F8停止当前操作")
		}
		return nil
	case ' ':
		if !w.allowSelection() {
			return nil
		}
		w.toggleSelected()
		return nil
	case 'a':
		if !w.allowSelection() {
			return nil
		}
		for _, id := range w.variables {
			w.selected[id] = true
			w.renderVariable(w.nodes[id])
		}
		w.renderHeader()
		return nil
	case 'n':
		if !w.allowSelection() {
			return nil
		}
		w.selected = map[string]bool{}
		for _, id := range w.variables {
			w.renderVariable(w.nodes[id])
		}
		w.renderHeader()
		return nil
	case 'x', 'X':
		w.oneClickForm(e.Rune() == 'X')
		return nil
	case 'i':
		w.oneClickResult()
		return nil
	case 'v':
		w.readSelected()
		return nil
	case 's':
		w.subscribeSelected("", "")
		return nil
	case 'l':
		if !w.active {
			w.fileForm(true)
		} else {
			w.setStatus("请先S/F8停止当前操作")
		}
		return nil
	case 'e':
		if !w.active {
			w.fileForm(false)
		} else {
			w.setStatus("请先S/F8停止，再导出最终快照")
		}
		return nil
	case 'S':
		w.stop()
		return nil
	}
	return e
}
func (w *uaScanWorkspace) setStatus(s string) { w.status = s; w.renderHeader() }
func (w *uaScanWorkspace) allowSelection() bool {
	if w.active && w.mode != "扫描" {
		w.setStatus("当前操作的变量集合已固定；请先S/F8停止再更改选择")
		return false
	}
	return true
}
func (w *uaScanWorkspace) toggleSelected() {
	id := ""
	if w.focus == 0 {
		if node := w.tree.GetCurrentNode(); node != nil {
			id, _ = node.GetReference().(string)
		}
	} else {
		row, _ := w.table.GetSelection()
		if row > 0 && row <= len(w.variables) {
			id = w.variables[row-1]
		}
	}
	n := w.nodes[id]
	if n == nil || n.row.NodeClass != "Variable" {
		w.setStatus("只能选择Variable变量节点")
		return
	}
	if w.selected[id] {
		delete(w.selected, id)
	} else {
		w.selected[id] = true
	}
	w.renderVariable(n)
	w.renderHeader()
}
func (w *uaScanWorkspace) selectedRows(limit int) ([]uaScanRow, bool) {
	if w.active || w.closed || w.owner().quitting {
		w.setStatus("请先S/F8停止当前操作")
		return nil, false
	}
	if len(w.selected) == 0 {
		w.setStatus("请先空格选择变量，或a全选已发现变量")
		return nil, false
	}
	if limit > 0 && len(w.selected) > limit {
		w.setStatus(fmt.Sprintf("选中%d变量，超过本操作%d上限；请缩小选择。未连接、未截断。", len(w.selected), limit))
		return nil, false
	}
	rows := make([]uaScanRow, 0, len(w.selected))
	for _, id := range w.variables {
		if w.selected[id] {
			rows = append(rows, w.nodes[id].row)
		}
	}
	return rows, true
}
func (w *uaScanWorkspace) renderVariable(n *uaScanNode) {
	mark := "[ ]"
	if w.selected[n.row.NodeID] {
		mark = "[x]"
	}
	// Tree labels never interpret server text as tview markup.
	n.tree.SetText(display(mark + " " + n.row.DisplayName + " (" + n.row.NodeID + ")"))
	value := any("未读取")
	if n.row.HasValue {
		value = n.row.Value
	}
	kind := n.row.ValueTypeName
	if kind == "" && n.row.ValueType != nil {
		kind = fmt.Sprint(n.row.ValueType)
	}
	values := []any{mark, n.row.DisplayName, n.row.NodeID, value, kind, n.row.Status, n.row.SourceTimestamp, n.row.ServerTimestamp}
	for col, v := range values {
		w.table.SetCell(n.tableRow, col, tview.NewTableCell(uaWorkspaceText(v)).SetReference(n.row.NodeID))
	}
}
func uaScanString(m map[string]any, key string) string {
	if m[key] == nil {
		return ""
	}
	return fmt.Sprint(m[key])
}
func uaScanInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case uint32:
		return int(x)
	case float64:
		return int(x)
	}
	n, _ := strconv.Atoi(fmt.Sprint(v))
	return n
}
func (w *uaScanWorkspace) apply(event engine.Event) {
	m, ok := event.Data.(map[string]any)
	switch event.Kind {
	case "scan-namespace":
		fingerprint := uaScanString(m, "fingerprint")
		if decoded, err := hex.DecodeString(fingerprint); ok && err == nil && len(decoded) == 32 {
			w.namespaceFingerprint = fingerprint
		}
	case "scan-node":
		if !ok {
			return
		}
		id := uaScanString(m, "node_id")
		if id == "" || w.nodes[id] != nil {
			return
		}
		// The engine enforces this bound; retain a defensive bound for injected/malformed events too.
		if len(w.nodes) >= w.maxNodes {
			w.reason = "达到UI节点上限"
			w.complete = false
			w.stop()
			return
		}
		row := uaScanRow{NodeID: id, ParentID: uaScanString(m, "parent_id"), DisplayName: uaScanString(m, "display_name"), BrowseName: uaScanString(m, "browse_name"), NodeClass: uaScanString(m, "node_class"), Depth: uaScanInt(m["depth"]), Status: "未读取"}
		if row.DisplayName == "" {
			row.DisplayName = row.BrowseName
		}
		if row.DisplayName == "" {
			row.DisplayName = id
		}
		node := tview.NewTreeNode(display(row.DisplayName + " (" + id + ")")).SetReference(id).SetSelectable(true)
		n := &uaScanNode{row: row, tree: node}
		w.nodes[id] = n
		if id == w.rootNode {
			w.tree.SetRoot(node).SetCurrentNode(node)
		} else if parent := w.nodes[row.ParentID]; parent != nil {
			parent.tree.AddChild(node)
		} else {
			w.tree.GetRoot().AddChild(node)
		}
		if row.NodeClass == "Variable" {
			w.variables = append(w.variables, id)
			n.tableRow = len(w.variables)
			w.renderVariable(n)
			if n.tableRow == 1 {
				w.table.Select(1, 0)
			}
		}
	case "scan-error":
		if ok {
			w.status = "扫描节点 " + uaScanString(m, "node_id") + " 失败：" + uaScanString(m, "error")
		}
	case "scan-summary":
		if !ok {
			return
		}
		w.complete, _ = m["complete"].(bool)
		w.reason = uaScanString(m, "reason")
		if !w.complete && w.reason == "" {
			w.reason = "服务器扫描未完整完成"
		}
	case "value", "notification":
		if !ok {
			return
		}
		id := uaScanString(m, "node_id")
		n := w.nodes[id]
		if n == nil || n.row.NodeClass != "Variable" {
			return
		}
		n.row = uaScanValueRow(n.row, event)
		w.received++
		w.renderVariable(n)
	case "connected":
		w.status = "已连接 · " + w.mode
	case "reconnecting":
		w.status = "连接中断，正在重连 · " + w.mode
	case "error":
		w.status = "连接/操作错误：" + uaWorkspaceText(event.Data)
	}
	w.renderHeader()
}
func uaScanValueRow(row uaScanRow, event engine.Event) uaScanRow {
	m, ok := event.Data.(map[string]any)
	if !ok {
		return row
	}
	// A bad DataValue may have no Variant. Conversely a present UA Null
	// Variant has a nil payload, so payload != nil is not a presence test.
	row.HasValue, ok = m["has_value"].(bool)
	if !ok {
		// Older/injected engine events use an explicit wire type when a
		// Variant exists, including TypeIDNull (0).
		row.HasValue = m["value_type"] != nil
	}
	row.Error = uaScanString(m, "error")
	row.Value = m["value"]
	row.ValueType = m["value_type"]
	row.ValueTypeName = uaScanString(m, "value_type_name")
	row.ValueIsArray, _ = m["value_is_array"].(bool)
	row.Status = uaScanString(m, "status")
	row.SourceTimestamp = m["source_timestamp"]
	row.ServerTimestamp = m["server_timestamp"]
	row.Kind = event.Kind
	row.ObservedAt = event.Time
	if row.ObservedAt.IsZero() {
		row.ObservedAt = time.Now().UTC()
	}
	return row
}
func (w *uaScanWorkspace) scanForm() {
	form := tview.NewForm().AddInputField("根NodeId（大小写敏感）", w.rootNode, 0, nil, nil).AddInputField("最大深度 0不限深度 / 1..256", strconv.Itoa(w.maxDepth), 0, nil, nil).AddInputField("最多节点 1..100000", strconv.Itoa(w.maxNodes), 0, nil, nil).AddInputField("每节点引用上限", strconv.Itoa(w.maxReferences), 0, nil, nil).AddInputField("读值批次 1..1000", strconv.Itoa(w.readBatch), 0, nil, nil)
	close := func() { w.owner().pages.RemovePage("ua-scan-form"); w.focusPane(w.focus) }
	form.AddButton("开始只读扫描", func() {
		root := form.GetFormItem(0).(*tview.InputField).GetText()
		parsed, err := ua.ParseNodeID(root)
		if err != nil {
			form.SetTitle("NodeId无效")
			return
		}
		limits := []int{256, 100000, 100000, 1000}
		values := make([]int, 4)
		for i, limit := range limits {
			v, e := strconv.Atoi(strings.TrimSpace(form.GetFormItem(i + 1).(*tview.InputField).GetText()))
			min := 1
			if i == 0 {
				min = 0
			}
			if e != nil || v < min || v > limit {
				form.SetTitle("扫描/批次参数超出范围")
				return
			}
			values[i] = v
		}
		w.rootNode = parsed.String()
		w.maxDepth, w.maxNodes, w.maxReferences, w.readBatch = values[0], values[1], values[2], values[3]
		close()
		w.startScan()
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" 显式扫描 · 确认将清空之前结果/选择 · Esc取消 ")
	w.owner().pages.AddPage("ua-scan-form", form, true, true)
	w.owner().App.SetFocus(form)
}
