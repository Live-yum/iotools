package tui

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Keep the exact preview independent of the saved collection and subsequent
// navigation. This is still a write request, subject to confirmDerived's gate.
func mqttCleanupRequest(source config.Request, data map[string]any) (config.Request, error) {
	if source.Protocol != "mqtt" || source.Action != "preview-retained" {
		return config.Request{}, fmt.Errorf("清理只能来自 MQTT 保留预览")
	}
	topics, ok := data["topics"].([]string)
	if !ok {
		return config.Request{}, fmt.Errorf("预览缺少精确主题列表")
	}
	token, ok := data["confirm_token"].(string)
	decoded, err := hex.DecodeString(token)
	if !ok || err != nil || len(decoded) != 32 {
		return config.Request{}, fmt.Errorf("预览缺少有效确认值")
	}
	r := copyRequest(source)
	r.Action = "clean-retained"
	r.Params["confirm_topics"] = append([]string{}, topics...)
	r.Params["confirm_token"] = token
	switch filters := source.Params["topics"].(type) {
	case []string:
		r.Params["topics"] = append([]string(nil), filters...)
	case []any:
		r.Params["topics"] = append([]any(nil), filters...)
	}
	return r, nil
}

func (u *UI) retainedPreview(data map[string]any) {
	if u.lastRequest.Protocol != "mqtt" || u.lastRequest.Action != "preview-retained" {
		return
	}
	// The retained-preview event arrives before Run returns. Deferring the
	// panel avoids a fast confirmation being discarded by the running guard.
	if u.running {
		u.mqttPreviewPending = data
		return
	}
	r, err := mqttCleanupRequest(u.lastRequest, data)
	if err != nil {
		u.modal(err.Error())
		return
	}
	topics := r.Params["confirm_topics"].([]string)
	warning := "MQTT 没有完整快照或原子比较删除。仅清理已确认的精确主题，请先暂停相关保留消息发布者。"
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	view.SetText(display(fmt.Sprintf("目标：%s\n\n%d 个精确主题：\n%s\n\n%s\n清理前会重新扫描；观察到的快照变化时拒绝执行。", r.Endpoint, len(topics), strings.Join(topics, "\n"), warning)))
	view.SetBorder(true).SetTitle(" 保留消息预览 · ↑↓/PgUp/PgDn 滚动 · Tab 切换 · Esc 取消 ")
	close := func() { u.pages.RemovePage("mqtt-retained"); u.App.SetFocus(u.inspector.tree) }
	buttons := tview.NewForm().SetButtonsAlign(tview.AlignRight)
	buttons.AddButton("取消", close)
	if !u.readonly {
		buttons.AddButton("确认精确清理", func() { close(); u.confirmDerived(r) })
	} else {
		view.SetTitle(" 只读保留预览 · ↑↓/PgUp/PgDn 滚动 · Esc 关闭 ")
	}
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyTab:
			if view.HasFocus() {
				buttons.SetFocus(0)
				u.App.SetFocus(buttons)
			} else {
				_, index := buttons.GetFocusedItemIndex()
				if index+1 < buttons.GetButtonCount() {
					buttons.SetFocus(index + 1)
					u.App.SetFocus(buttons)
				} else {
					u.App.SetFocus(view)
				}
			}
			return nil
		case tcell.KeyBacktab:
			if view.HasFocus() {
				buttons.SetFocus(buttons.GetButtonCount() - 1)
				u.App.SetFocus(buttons)
			} else {
				_, index := buttons.GetFocusedItemIndex()
				if index > 0 {
					buttons.SetFocus(index - 1)
					u.App.SetFocus(buttons)
				} else {
					u.App.SetFocus(view)
				}
			}
			return nil
		}
		return e
	})
	u.pages.AddPage("mqtt-retained", panel, true, true)
	u.App.SetFocus(view)
}

func (u *UI) finishMQTTPreview(success bool) {
	pending := u.mqttPreviewPending
	u.mqttPreviewPending = nil
	if success && pending != nil {
		u.retainedPreview(pending)
	}
}

// The exact topic list has already been reviewed in the scrollable panel. Keep
// the final gate compact instead of repeating thousands of JSON topic entries.
func (u *UI) confirmMQTTCleanup(r config.Request) {
	topics := r.Strings("confirm_topics")
	text := fmt.Sprintf("清理已核对的 %d 个主题？\n%s\n确认值：%s\n列表见上一屏；非原子，先停发布者。", len(topics), r.Endpoint, r.String("confirm_token", ""))
	modal := tview.NewModal().SetText(display(text)).AddButtons([]string{"取消", "确认执行"})
	close := func() { u.pages.RemovePage("derived-confirm"); u.App.SetFocus(u.inspector.tree) }
	modal.SetDoneFunc(func(i int, _ string) {
		close()
		if i == 1 {
			// State may have changed while the confirmation was open.
			if u.running || u.readonly {
				u.modal("当前正在执行或为只读模式，未开始清理")
				return
			}
			u.start(r)
		}
	})
	modal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		return event
	})
	u.pages.AddPage("derived-confirm", modal, true, true)
	u.App.SetFocus(modal)
}
