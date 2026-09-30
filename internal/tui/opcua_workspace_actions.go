package tui

import (
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/rivo/tview"
)

func (w *uaWorkspace) selectedNode() (string, any) {
	switch w.focus {
	case 0:
		if n := w.browse.GetCurrentNode(); n != nil {
			if id, ok := uaWorkspaceNodeID(n.GetReference()); ok {
				return id, n.GetReference()
			}
		}
	case 2:
		row, _ := w.references.GetSelection()
		if row > 0 {
			value := w.references.GetCell(row, 0).GetReference()
			if id, ok := uaWorkspaceNodeID(value); ok {
				return id, value
			}
		}
	case 3:
		row, _ := w.subscriptions.GetSelection()
		if row > 0 {
			key, _ := w.subscriptions.GetCell(row, 0).GetReference().(string)
			if s := w.owner.uaSubscriptions[key]; s != nil {
				return s.Request.String("node_id", w.node), s
			}
		}
	}
	return w.node, nil
}
func (w *uaWorkspace) key(pane int, e *tcell.EventKey) *tcell.EventKey {
	w.focus = pane
	switch e.Key() {
	case tcell.KeyEscape:
		w.close()
		return nil
	case tcell.KeyTab:
		w.focusPane(pane + 1)
		return nil
	case tcell.KeyBacktab:
		w.focusPane(pane - 1)
		return nil
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(w.history) > 0 {
			node := w.history[len(w.history)-1]
			w.history = w.history[:len(w.history)-1]
			w.navigate(node, false)
		}
		return nil
	case tcell.KeyF8:
		if w.cancel != nil {
			w.cancel()
		}
		w.owner.stopUASubscriptions()
		return nil
	case tcell.KeyCtrlY:
		node, _ := w.selectedNode()
		w.owner.copyText(node)
		return nil
	}
	if e.Key() == tcell.KeyRune {
		switch e.Rune() {
		case '1', '2', '3', '4':
			w.focusPane(int(e.Rune() - '1'))
			return nil
		case 'r':
			if !w.loading {
				w.refresh()
			}
			return nil
		case 'g':
			w.nodeForm()
			return nil
		case 's':
			node, _ := w.selectedNode()
			w.owner.subscribeUAFrom(w.base, node)
			return nil
		case 'S':
			node, _ := w.selectedNode()
			if s := w.owner.uaSubscriptions[uaSubscriptionKey(w.base, node)]; s != nil {
				s.Cancel()
			}
			return nil
		case 'e':
			if pane == 1 && !w.loading {
				row, _ := w.attributes.GetSelection()
				if row > 0 {
					if data, ok := w.attributes.GetCell(row, 0).GetReference().(map[string]any); ok {
						r := copyRequest(w.base)
						r.Action = "attributes"
						r.Params["node_id"] = w.node
						events := []engine.Event{}
						for i := 1; i < w.attributes.GetRowCount(); i++ {
							events = append(events, engine.Event{Kind: "attribute", Data: w.attributes.GetCell(i, 0).GetReference()})
						}
						w.close()
						w.owner.lastRequest = r
						w.owner.inspector.reset(r)
						for _, event := range events {
							w.owner.inspector.add(event)
						}
						w.owner.visual = true
						w.owner.resultPages.SwitchToPage("visual")
						w.owner.attributeForm(data)
					}
				}
			}
			return nil
		case 'c':
			if !w.loading {
				node, value := w.selectedNode()
				if ref, ok := value.(*ua.ReferenceDescription); ok && ref.NodeClass == ua.NodeClassMethod {
					r := copyRequest(w.base)
					r.Action = "method-arguments"
					r.Params["method_id"] = node
					r.Params["object_id"] = w.node
					w.close()
					w.owner.start(r)
				} else {
					w.status = "请从浏览/引用中选Method节点"
					w.renderHeader()
				}
			}
			return nil
		}
	}
	return e
}
func (w *uaWorkspace) detail(value any) {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		b = []byte(fmt.Sprint(value))
	}
	view := tview.NewTextView().SetText(clean(string(b))).SetScrollable(true).SetWrap(true)
	view.SetBorder(true).SetTitle(" 属性详情 · Esc返回四窗 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			w.owner.pages.RemovePage("ua-workspace-detail")
			w.owner.App.SetFocus(w.widgets[w.focus])
			return nil
		}
		return e
	})
	w.owner.pages.AddPage("ua-workspace-detail", view, true, true)
	w.owner.App.SetFocus(view)
}
func (w *uaWorkspace) nodeForm() {
	form := tview.NewForm().AddInputField("明确NodeId", w.node, 0, nil, nil)
	close := func() { w.owner.pages.RemovePage("ua-workspace-node"); w.owner.App.SetFocus(w.widgets[w.focus]) }
	form.AddButton("浏览", func() {
		node := form.GetFormItem(0).(*tview.InputField).GetText()
		if _, err := ua.ParseNodeID(node); err != nil {
			form.SetTitle("NodeId无效")
			return
		}
		close()
		w.navigate(node, true)
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" 节点跳转 · 确认后只读刷新 · Esc取消 ")
	w.owner.pages.AddPage("ua-workspace-node", form, true, true)
	w.owner.App.SetFocus(form)
}
