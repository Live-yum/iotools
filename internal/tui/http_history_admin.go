package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (u *UI) historyAdmin() {
	if u.running {
		u.modal("请先停止当前请求")
		return
	}
	form := tview.NewForm().SetItemPadding(0)
	form.SetBorder(true).SetTitle(" 历史数据库管理 · 默认只读 · Esc取消 ")
	form.AddDropDown("操作", []string{"列出全部集合", "只读SQL/.tables/.schema", "可写SQL（先预览）", "删除集合历史（先预览）", "迁移合并集合（先预览）"}, 0, nil)
	form.AddInputField("现有数据库", u.HTTPHistoryPath, 0, nil, nil)
	form.AddInputField("来源集合", u.collection.SourcePath, 0, nil, nil)
	form.AddInputField("目标集合", "", 0, nil, nil)
	form.AddInputField("新备份文件", u.HTTPHistoryPath+".before-"+time.Now().UTC().Format("20060102T150405.000000000")+".sqlite", 0, nil, nil)
	form.AddTextArea("SQL（最多32句）", ".tables", 0, 5, 65536, nil)
	ctx, cancel := context.WithCancel(context.Background())
	closed, busy := false, false
	close := func() {
		closed = true
		cancel()
		u.pages.RemovePage("history-admin")
		u.pages.RemovePage("history-admin-preview")
		u.pages.RemovePage("history-admin-result")
		u.App.SetFocus(u.list)
	}
	resultView := func(value any) {
		b, _ := json.MarshalIndent(value, "", "  ")
		view := tview.NewTextView().SetText(clean(string(b))).SetWrap(true).SetScrollable(true)
		view.SetBorder(true).SetTitle(" 本机历史结果 · Esc返回编辑 ")
		view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			if e.Key() == tcell.KeyEscape {
				u.pages.RemovePage("history-admin-result")
				u.App.SetFocus(form)
				return nil
			}
			return e
		})
		u.pages.AddPage("history-admin-result", view, true, true)
		u.App.SetFocus(view)
	}
	run := func(work func(context.Context) (any, error), finish func(any)) {
		if busy || closed {
			return
		}
		busy = true
		form.SetTitle(" 本机数据库处理中 · Esc取消 ")
		if u.localCancels == nil {
			u.localCancels = map[uint64]context.CancelFunc{}
		}
		u.nextLocalWork++
		id := u.nextLocalWork
		u.localCancels[id] = cancel
		go func() {
			value, err := work(ctx)
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
				form.SetTitle(" 历史数据库管理 · 默认只读 · Esc取消 ")
				if err != nil {
					resultView(map[string]string{"错误": err.Error()})
					return
				}
				finish(value)
			})
		}()
	}
	form.AddButton("查询/预览", func() {
		if busy {
			return
		}
		mode, _ := form.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
		path := form.GetFormItem(1).(*tview.InputField).GetText()
		source := form.GetFormItem(2).(*tview.InputField).GetText()
		target := form.GetFormItem(3).(*tview.InputField).GetText()
		backup := form.GetFormItem(4).(*tview.InputField).GetText()
		script := form.GetFormItem(5).(*tview.TextArea).GetText()
		if mode == 0 {
			run(func(ctx context.Context) (any, error) { return engine.ListHTTPHistoryCollections(ctx, path) }, resultView)
			return
		}
		if mode == 1 {
			run(func(ctx context.Context) (any, error) { return engine.QueryHTTPHistoryScript(ctx, path, script) }, resultView)
			return
		}
		if u.readonly {
			form.SetTitle("只读模式禁止SQL/集合修改")
			return
		}
		if mode == 3 || mode == 4 {
			action := "delete"
			if mode == 4 {
				action = "migrate"
			}
			var err error
			script, err = engine.HTTPHistoryCollectionScript(action, source, target)
			if err != nil {
				form.SetTitle(err.Error())
				return
			}
		}
		if backup == "" {
			form.SetTitle("必须填写新的备份文件")
			return
		}
		run(func(ctx context.Context) (any, error) { return engine.PreviewHTTPHistoryScript(ctx, path, script) }, func(value any) {
			preview := value.(*engine.HistoryScriptPreview)
			text := fmt.Sprintf("目标数据库：%s\n新备份：%s\n一致快照：%d字节\n语句数：%d\n\n%s\n\n将原子执行以上全部SQL；先保存原数据库，不覆盖已有备份。数据库有变化则拒绝，需重新预览。修改可能影响所有集合及表。", preview.Database, backup, preview.Bytes, preview.Statements, script)
			view := tview.NewTextView().SetText(clean(text)).SetWrap(true).SetScrollable(true)
			view.SetBorder(true).SetTitle(" 明确修改预览 · 可滚动 · Tab选择按钮 ")
			dismiss := func() { u.pages.RemovePage("history-admin-preview"); u.App.SetFocus(form) }
			no := tview.NewButton("取消").SetSelectedFunc(dismiss)
			yes := tview.NewButton("备份并原子执行").SetSelectedFunc(func() {
				if u.readonly || busy {
					return
				}
				dismiss()
				run(func(ctx context.Context) (any, error) {
					return engine.ExecuteHTTPHistoryScript(ctx, preview.Database, script, preview.Token, backup, true)
				}, resultView)
			})
			buttons := tview.NewFlex().AddItem(no, 0, 1, false).AddItem(yes, 0, 1, false)
			layout := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 1, 0, false)
			widgets := []tview.Primitive{view, no, yes}
			focus := 0
			layout.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEscape {
					dismiss()
					return nil
				}
				if e.Key() == tcell.KeyTab {
					focus = (focus + 1) % 3
					u.App.SetFocus(widgets[focus])
					return nil
				}
				if e.Key() == tcell.KeyBacktab {
					focus = (focus + 2) % 3
					u.App.SetFocus(widgets[focus])
					return nil
				}
				return e
			})
			u.pages.AddPage("history-admin-preview", layout, true, true)
			u.App.SetFocus(view)
		})
	}).AddButton("关闭", close).SetCancelFunc(close)
	u.pages.AddPage("history-admin", form, true, true)
	u.App.SetFocus(form)
}
