package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (u *UI) httpConsole() {
	if u.running {
		u.modal("请等待当前操作结束或先按 F8 取消")
		return
	}
	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" HTTP 查询控制台 · 不发送网络请求 · Esc 关闭 ")
	mode := 0
	form.AddDropDown("查询模式", []string{"当前响应 jq", "SQLite 历史只读 SQL", "生成所选请求 curl（不联网）"}, 0, func(_ string, index int) { mode = index })
	form.AddInputField("历史数据库", u.HTTPHistoryPath, 64, nil, nil)
	form.AddTextArea("表达式（SQL 不带分号）", ".", 70, 5, 65536, nil)
	close := func() { u.pages.RemovePage("http-console"); u.App.SetFocus(u.list) }
	form.AddButton("查询", func() {
		query := form.GetFormItem(2).(*tview.TextArea).GetText()
		path := form.GetFormItem(1).(*tview.InputField).GetText()
		var body []byte
		if mode == 0 {
			body = append([]byte(nil), u.lastHTTPBody...)
			if body == nil {
				form.SetTitle(" 尚无 HTTP 响应，请先执行请求 ")
				return
			}
		} else if mode == 1 && path == "" {
			form.SetTitle(" 请指定已存在的 SQLite 历史文件 ")
			return
		}
		queryMode := mode
		var selected config.Request
		if queryMode == 2 {
			if u.selected < 0 || u.selected >= len(u.collection.Requests) {
				return
			}
			selected = u.collection.Requests[u.selected]
			if selected.Protocol != "http" {
				form.SetTitle("curl生成只支持所选HTTP请求")
				return
			}
		}
		close()
		ctx, cancel := context.WithCancel(context.Background())
		u.mu.Lock()
		u.cancel = cancel
		u.mu.Unlock()
		u.running = true
		u.setStatus("正在本机查询 · F8 取消")
		go func() {
			var result any
			var err error
			if queryMode == 0 {
				result, err = engine.FilterJSON(ctx, query, body)
			} else if queryMode == 2 {
				result, err = engine.GenerateCurl(engine.WithHTTPWorkflowOptions(ctx, engine.HTTPWorkflowOptions{HistoryPath: u.HTTPHistoryPath, Prompt: u.workflowPrompt, Select: u.workflowSelect}), u.collection, selected, u.profile, false, false)
			} else {
				result, err = engine.QueryHTTPHistory(ctx, path, query)
			}
			cancel()
			u.App.QueueUpdateDraw(func() {
				u.running = false
				u.mu.Lock()
				u.cancel = nil
				u.mu.Unlock()
				if u.quitting {
					u.App.Stop()
					return
				}
				if err != nil {
					u.modal(err.Error())
					return
				}
				b, _ := json.MarshalIndent(result, "", "  ")
				if command, ok := result.(string); ok {
					b = []byte(command)
				}
				view := tview.NewTextView().SetText(string(b)).SetScrollable(true)
				view.SetBorder(true).SetTitle(fmt.Sprintf(" 查询结果 · %d 字节 · Esc 关闭 ", len(b)))
				view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
					if e.Key() == tcell.KeyCtrlY {
						u.copyText(string(b))
						return nil
					}
					if e.Key() == tcell.KeyEscape {
						u.pages.RemovePage("http-query-result")
						u.App.SetFocus(u.list)
						return nil
					}
					return e
				})
				u.pages.AddPage("http-query-result", view, true, true)
				u.App.SetFocus(view)
			})
		}()
	}).AddButton("关闭", close).SetCancelFunc(close)
	u.pages.AddPage("http-console", form, true, true)
	u.App.SetFocus(form)
}
