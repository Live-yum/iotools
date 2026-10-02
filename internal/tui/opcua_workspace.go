package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/rivo/tview"
	"sort"
	"strings"
)

type uaWorkspace struct {
	owner                                 *UI
	base                                  config.Request
	node                                  string
	history                               []string
	root                                  *uaWorkspaceLayout
	body                                  *tview.Flex
	header, footer                        *tview.TextView
	browse                                *tview.TreeView
	attributes, references, subscriptions *tview.Table
	widgets                               []tview.Primitive
	focus, width, height, layoutFocus     int
	compact, closed, loading              bool
	generation                            uint64
	cancel                                context.CancelFunc
	status                                string
	run                                   func(context.Context, config.Request, bool, engine.Emit) error
}

type uaWorkspaceLayout struct {
	*tview.Flex
	workspace *uaWorkspace
}

func (l *uaWorkspaceLayout) Draw(screen tcell.Screen) {
	_, _, width, height := l.GetRect()
	l.workspace.layout(width, height)
	l.Flex.Draw(screen)
}

func (u *UI) showUAWorkspace() {
	if u.running {
		u.modal("请先等待当前请求结束或F8取消")
		return
	}
	r := u.lastRequest
	if r.Protocol != "opcua" && u.selected >= 0 && u.selected < len(u.collection.Requests) {
		r = u.collection.Requests[u.selected]
	}
	if r.Protocol != "opcua" {
		u.modal("请选择OPC UA请求，或在F10选中订阅后按D")
		return
	}
	resolved, err := u.collection.Resolve(r, u.profile)
	if err != nil {
		u.modal(err.Error())
		return
	}
	u.showUAWorkspaceFor(resolved)
}
func (u *UI) showUAWorkspaceFor(base config.Request) {
	if u.running || u.quitting || base.Protocol != "opcua" {
		return
	}
	if u.uaWorkspace != nil {
		u.uaWorkspace.close()
	}
	base = copyRequest(base)
	base.Action = "browse"
	for _, key := range []string{"node_ids", "browse_path", "attributes", "attribute", "value", "value_type", "arguments", "method_id", "object_id", "max_events"} {
		delete(base.Params, key)
	}
	node := base.String("node_id", "i=85")
	w := &uaWorkspace{owner: u, base: base, node: node, run: engine.Run, status: "尚未读取 · r明确刷新", layoutFocus: -1}
	w.header = tview.NewTextView().SetWrap(true)
	w.footer = tview.NewTextView().SetText("Tab/1–4切换 · r刷新 · Enter浏览/详情 · 退格返回 · s订阅 S取消选中\nCtrl+Y复制 · g节点 · e属性编辑 · c方法 · F8停止读取/订阅 · Esc关闭但订阅继续")
	w.browse = tview.NewTreeView()
	w.browse.SetBorder(true).SetTitle(" 1 浏览 · Enter下钻 ")
	w.attributes = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	w.attributes.SetBorder(true).SetTitle(" 2 属性 · Enter详情 e编辑 ")
	w.references = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	w.references.SetBorder(true).SetTitle(" 3 引用 · Enter浏览 ")
	w.subscriptions = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	w.subscriptions.SetBorder(true).SetTitle(" 4 独立订阅 ")
	w.widgets = []tview.Primitive{w.browse, w.attributes, w.references, w.subscriptions}
	w.body = tview.NewFlex()
	w.root = &uaWorkspaceLayout{Flex: tview.NewFlex().SetDirection(tview.FlexRow).AddItem(w.header, 2, 0, false).AddItem(w.body, 0, 1, true).AddItem(w.footer, 2, 0, false), workspace: w}
	for i, widget := range w.widgets {
		index := i
		capture := func(e *tcell.EventKey) *tcell.EventKey { return w.key(index, e) }
		switch v := widget.(type) {
		case *tview.TreeView:
			v.SetInputCapture(capture)
		case *tview.Table:
			v.SetInputCapture(capture)
		}
	}
	w.browse.SetSelectedFunc(func(n *tview.TreeNode) {
		if id, ok := uaWorkspaceNodeID(n.GetReference()); ok {
			w.navigate(id, true)
		}
	})
	w.references.SetSelectedFunc(func(row, col int) {
		if row > 0 {
			if id, ok := uaWorkspaceNodeID(w.references.GetCell(row, 0).GetReference()); ok {
				w.navigate(id, true)
			}
		}
	})
	w.attributes.SetSelectedFunc(func(row, col int) {
		if row > 0 {
			w.detail(w.attributes.GetCell(row, 0).GetReference())
		}
	})
	w.subscriptions.SetSelectedFunc(func(row, col int) {
		if row > 0 {
			key, _ := w.subscriptions.GetCell(row, 0).GetReference().(string)
			if s := u.uaSubscriptions[key]; s != nil {
				w.navigate(s.Request.String("node_id", "i=85"), true)
			}
		}
	})
	u.uaWorkspace = w
	w.clearNodeViews()
	w.renderSubscriptions()
	w.layout(120, 40)
	w.renderHeader()
	u.pages.AddPage("ua-workspace", w.root, true, true)
	u.App.SetFocus(w.browse)
}
func uaWorkspaceNodeID(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, v != ""
	case *ua.ReferenceDescription:
		if v != nil && v.NodeID != nil && v.NodeID.NodeID != nil && v.NodeID.ServerIndex == 0 && v.NodeID.NamespaceURI == "" {
			return v.NodeID.NodeID.String(), true
		}
	}
	return "", false
}
func uaWorkspaceText(v any) string {
	if v == nil {
		return "—"
	}
	text := fmt.Sprint(v)
	r := []rune(clean(text))
	if len(r) > 1024 {
		text = string(r[:1024]) + "…"
	} else {
		text = string(r)
	}
	return display(text)
}
func (w *uaWorkspace) layout(width, height int) {
	compact := width < 110 || height < 32
	for i, widget := range w.widgets {
		if widget.HasFocus() {
			w.focus = i
		}
	}
	if width == w.width && height == w.height && compact == w.compact && (!compact || w.layoutFocus == w.focus) {
		return
	}
	w.width, w.height, w.compact, w.layoutFocus = width, height, compact, w.focus
	w.body.Clear()
	if compact {
		w.body.SetDirection(tview.FlexRow).AddItem(w.widgets[w.focus], 0, 1, true)
	} else {
		top := tview.NewFlex().AddItem(w.browse, 0, 1, w.focus == 0).AddItem(w.attributes, 0, 1, w.focus == 1)
		bottom := tview.NewFlex().AddItem(w.references, 0, 1, w.focus == 2).AddItem(w.subscriptions, 0, 1, w.focus == 3)
		w.body.SetDirection(tview.FlexRow).AddItem(top, 0, 1, w.focus < 2).AddItem(bottom, 0, 1, w.focus >= 2)
	}
	w.renderHeader()
}
func (w *uaWorkspace) renderHeader() {
	mode := "四窗同显"
	if w.compact {
		mode = fmt.Sprintf("窄屏分页 %d/4", w.focus+1)
	}
	w.header.SetText(clean(fmt.Sprintf("OPC UA %s · 1浏览 2属性 3引用 4订阅 · %s\n%s · 节点 %s", mode, w.status, w.base.Endpoint, w.node)))
}
func (w *uaWorkspace) clearNodeViews() {
	root := tview.NewTreeNode(display(w.node)).SetReference(w.node).SetSelectable(true)
	w.browse.SetRoot(root).SetCurrentNode(root)
	for table, headers := range map[*tview.Table][]string{w.attributes: {"属性", "值", "状态"}, w.references: {"名称", "方向/类型", "节点"}} {
		table.Clear()
		for col, h := range headers {
			table.SetCell(0, col, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
		}
	}
}
func (w *uaWorkspace) focusPane(index int) {
	w.focus = (index + 4) % 4
	w.owner.App.SetFocus(w.widgets[w.focus])
	w.renderHeader()
}
func (w *uaWorkspace) close() {
	if w.closed {
		return
	}
	w.closed = true
	w.generation++
	if w.cancel != nil {
		w.cancel()
	}
	w.owner.pages.RemovePage("ua-workspace")
	if w.owner.uaWorkspace == w {
		w.owner.uaWorkspace = nil
	}
	w.owner.App.SetFocus(w.owner.list)
}
func (w *uaWorkspace) navigate(node string, remember bool) {
	if _, err := ua.ParseNodeID(node); err != nil {
		w.status = "节点无效"
		w.renderHeader()
		return
	}
	if node != w.node && remember {
		w.history = append(w.history, w.node)
		if len(w.history) > 256 {
			w.history = w.history[1:]
		}
	}
	w.node = node
	w.refresh()
}
func (w *uaWorkspace) refresh() {
	if w.closed || w.owner.quitting {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	w.generation++
	generation := w.generation
	w.loading = true
	w.status = "读取中（只读）"
	w.clearNodeViews()
	w.renderHeader()
	r := copyRequest(w.base)
	r.Params["node_id"] = w.node
	r.Params["max_references"] = 2000
	duration, err := r.Duration()
	if err != nil {
		w.status = err.Error()
		w.loading = false
		w.renderHeader()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	w.cancel = cancel
	u := w.owner
	if u.localCancels == nil {
		u.localCancels = map[uint64]context.CancelFunc{}
	}
	u.nextLocalWork++
	id := u.nextLocalWork
	u.localCancels[id] = cancel
	go func() {
		failures := []string{}
		for _, phase := range []string{"browse", "attributes", "references"} {
			if ctx.Err() != nil {
				break
			}
			request := copyRequest(r)
			request.Action = phase
			if phase == "browse" {
				request.Params["direction"] = "forward"
				request.Params["reference_type"] = "i=33"
				request.Params["include_subtypes"] = true
			} else {
				delete(request.Params, "reference_type")
				request.Params["direction"] = "both"
			}
			err := w.run(ctx, request, false, func(event engine.Event) {
				if event.Kind != "reference" && event.Kind != "attribute" {
					return
				}
				u.App.QueueUpdateDraw(func() {
					if !w.closed && w.generation == generation {
						w.apply(phase, event)
					}
				})
			})
			if err != nil {
				failures = append(failures, phase+": "+err.Error())
			}
		}
		contextErr := ctx.Err()
		cancel()
		u.App.QueueUpdateDraw(func() {
			delete(u.localCancels, id)
			if u.quitting {
				if !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
					u.App.Stop()
				}
				return
			}
			if w.closed || w.generation != generation {
				return
			}
			w.loading = false
			w.cancel = nil
			w.status = "读取完成"
			if contextErr != nil {
				w.status = "已停止：" + contextErr.Error()
			} else if len(failures) > 0 {
				w.status = "部分失败：" + strings.Join(failures, "；")
			}
			w.renderHeader()
		})
	}()
}
func (w *uaWorkspace) apply(phase string, event engine.Event) {
	if event.Kind == "attribute" {
		m, ok := event.Data.(map[string]any)
		if !ok || fmt.Sprint(m["node_id"]) != w.node || w.attributes.GetRowCount() > 27 {
			return
		}
		row := w.attributes.GetRowCount()
		for col, key := range []string{"attribute", "value", "status"} {
			w.attributes.SetCell(row, col, tview.NewTableCell(uaWorkspaceText(m[key])).SetReference(m))
		}
		return
	}
	ref, ok := event.Data.(*ua.ReferenceDescription)
	if !ok || ref == nil || ref.NodeID == nil || ref.NodeID.NodeID == nil {
		return
	}
	name := ref.NodeID.NodeID.String()
	if ref.DisplayName != nil && ref.DisplayName.Text != "" {
		name = ref.DisplayName.Text
	}
	if phase == "browse" {
		if len(w.browse.GetRoot().GetChildren()) < 2000 {
			w.browse.GetRoot().AddChild(tview.NewTreeNode(display(name)).SetReference(ref).SetSelectable(true))
		}
		return
	}
	if w.references.GetRowCount() > 2000 {
		return
	}
	direction := "反向"
	if ref.IsForward {
		direction = "正向"
	}
	row := w.references.GetRowCount()
	for col, value := range []any{name, direction + " " + fmt.Sprint(ref.ReferenceTypeID), ref.NodeID.NodeID.String()} {
		w.references.SetCell(row, col, tview.NewTableCell(uaWorkspaceText(value)).SetReference(ref))
	}
}
func (w *uaWorkspace) renderSubscriptions() {
	if w.closed {
		return
	}
	table := w.subscriptions
	row, col := table.GetSelection()
	table.Clear()
	for i, h := range []string{"节点", "最新值", "次数", "状态"} {
		table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	keys := []string{}
	for key, s := range w.owner.uaSubscriptions {
		if s.Request.Endpoint == w.base.Endpoint {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for i, key := range keys {
		s := w.owner.uaSubscriptions[key]
		b, err := json.Marshal(s.Value)
		value := string(b)
		if err != nil {
			value = fmt.Sprint(s.Value)
		}
		for j, text := range []any{s.Request.String("node_id", ""), value, s.Count, s.Status} {
			table.SetCell(i+1, j, tview.NewTableCell(uaWorkspaceText(text)).SetReference(key))
		}
	}
	if row < table.GetRowCount() {
		table.Select(row, col)
	}
	table.SetTitle(fmt.Sprintf(" 4 独立订阅 · 当前端点%d项 ", len(keys)))
}
