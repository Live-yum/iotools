package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strconv"
)

func (u *UI) httpHistory() {
	if u.running {
		u.modal("请先等待当前请求结束或按F8取消")
		return
	}
	if u.HTTPHistoryPath == "" {
		u.modal("尚未启用历史；使用 --history-db 文件路径启动以明确保存响应历史")
		return
	}
	historyPath, collectionID := u.HTTPHistoryPath, u.collection.SourcePath
	busy := false
	table := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	table.SetBorder(true).SetTitle(" HTTP历史 · M集合/SQL管理 · Enter查看 · D明确删除 · Esc关闭 ")
	ctx, cancel := context.WithCancel(context.Background())
	closed := false
	close := func() { closed = true; cancel(); u.pages.RemovePage("http-history"); u.App.SetFocus(u.list) }
	run := func(work func(context.Context) (any, error), finish func(any)) {
		if busy {
			return
		}
		busy = true
		if u.localCancels == nil {
			u.localCancels = map[uint64]context.CancelFunc{}
		}
		u.nextLocalWork++
		id := u.nextLocalWork
		u.localCancels[id] = cancel
		go func() {
			result, err := work(ctx)
			u.App.QueueUpdateDraw(func() {
				delete(u.localCancels, id)
				busy = false
				if u.quitting {
					if !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
						u.App.Stop()
					}
					return
				}
				if closed {
					return
				}
				if err != nil {
					u.modal(err.Error())
					return
				}
				finish(result)
			})
		}()
	}
	var refresh func()
	refresh = func() {
		run(func(ctx context.Context) (any, error) {
			return engine.ListHTTPHistory(ctx, historyPath, collectionID, "")
		}, func(result any) {
			table.Clear()
			for i, h := range []string{"ID", "请求", "环境", "方法", "时间UTC", "状态", "正文大小"} {
				table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
			}
			for i, row := range result.([]map[string]any) {
				for j, key := range []string{"id", "recipe", "profile", "method", "created_at", "status", "body_bytes"} {
					table.SetCell(i+1, j, tview.NewTableCell(display(fmt.Sprint(row[key]))).SetReference(row["id"]))
				}
			}
		})
	}
	selected := func() int64 {
		row, _ := table.GetSelection()
		if row < 1 {
			return 0
		}
		id, _ := table.GetCell(row, 0).GetReference().(int64)
		return id
	}
	table.SetSelectedFunc(func(row, col int) {
		id := selected()
		if id == 0 {
			return
		}
		run(func(ctx context.Context) (any, error) {
			return engine.GetHTTPHistory(ctx, historyPath, collectionID, id)
		}, func(result any) {
			b, _ := json.MarshalIndent(result, "", "  ")
			view := tview.NewTextView().SetText(string(b)).SetScrollable(true).SetWrap(true)
			view.SetBorder(true).SetTitle(" 历史请求/响应 · Ctrl-Y复制 · Esc返回 ")
			view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEscape {
					u.pages.RemovePage("http-history-detail")
					u.App.SetFocus(table)
					return nil
				}
				if e.Key() == tcell.KeyCtrlY {
					u.copyText(string(b))
					return nil
				}
				return e
			})
			u.pages.AddPage("http-history-detail", view, true, true)
			u.App.SetFocus(view)
		})
	})
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Rune() == 'M' {
			close()
			u.historyAdmin()
			return nil
		}
		if e.Rune() == 'r' {
			refresh()
			return nil
		}
		if e.Rune() == 'D' {
			if u.readonly {
				u.modal("只读模式禁止删除历史")
				return nil
			}
			id := selected()
			if id == 0 {
				return nil
			}
			form := tview.NewForm()
			form.SetBorder(true).SetTitle(" 永久删除历史 · 不可恢复 · 只删除当前集合的指定ID ")
			form.AddInputField("输入ID确认", "", 20, nil, nil)
			dismiss := func() { u.pages.RemovePage("history-delete"); u.App.SetFocus(table) }
			form.AddButton("确认永久删除", func() {
				if form.GetFormItem(0).(*tview.InputField).GetText() != strconv.FormatInt(id, 10) {
					form.SetTitle("ID不匹配，未删除")
					return
				}
				dismiss()
				run(func(ctx context.Context) (any, error) {
					return engine.DeleteHTTPHistory(ctx, historyPath, collectionID, []int64{id}, true)
				}, func(any) { refresh() })
			}).AddButton("取消", dismiss).SetCancelFunc(dismiss)
			u.pages.AddPage("history-delete", form, true, true)
			u.App.SetFocus(form)
			return nil
		}
		return e
	})
	u.pages.AddPage("http-history", table, true, true)
	u.App.SetFocus(table)
	refresh()
}
