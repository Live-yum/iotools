package tui

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// authorizeChainWrite runs on the engine goroutine and grants only one resolved
// request. No chain step starts until the user approves its method and endpoint.
func (u *UI) authorizeChainWrite(ctx context.Context, r config.Request) (bool, error) {
	if u.readonly {
		return false, nil
	}
	answer := make(chan bool, 1)
	u.App.QueueUpdateDraw(func() {
		fileNotice := ""
		if file := r.String("body_file", ""); file != "" {
			fileNotice = "\n将发送文件正文：" + file
		}

		finish := func(allowed bool) {
			u.pages.RemovePage("chain-confirm")
			u.App.SetFocus(u.list)
			select {
			case answer <- allowed:
			default:
			}
		}
		m := u.httpConfirmation(" 修改步骤确认 ", "请求："+r.ID+"\n方法："+r.Action+"\n目标："+r.Endpoint+fileNotice+"\n\n只批准本次步骤；取消会停止请求链。", "批准此步骤", finish)
		u.pages.AddPage("chain-confirm", m, true, true)
		u.App.SetFocus(m)
	})
	select {
	case accepted := <-answer:
		return accepted, nil
	case <-ctx.Done():
		u.App.QueueUpdateDraw(func() { u.pages.RemovePage("chain-confirm") })
		return false, nil
	}
}

func (u *UI) workflowPrompt(ctx context.Context, message, initial string, sensitive bool) (string, error) {
	type result struct {
		value string
		err   error
	}
	answers := make(chan result, 1)
	u.App.QueueUpdateDraw(func() {
		form := tview.NewForm()
		form.SetBorder(true).SetTitle(" 模板输入 · Esc 取消 ")
		form.AddInputField(clean(message), initial, 64, nil, nil)
		if sensitive {
			form.GetFormItem(0).(*tview.InputField).SetMaskCharacter('*')
		}
		done := func(value string, err error) {
			u.pages.RemovePage("workflow-prompt")
			u.App.SetFocus(u.list)
			select {
			case answers <- result{value, err}:
			default:
			}
		}
		form.AddButton("继续", func() { done(form.GetFormItem(0).(*tview.InputField).GetText(), nil) }).AddButton("取消", func() { done("", fmt.Errorf("用户取消模板输入")) }).SetCancelFunc(func() { done("", fmt.Errorf("用户取消模板输入")) })
		u.pages.AddPage("workflow-prompt", form, true, true)
		u.App.SetFocus(form)
	})
	select {
	case r := <-answers:
		return r.value, r.err
	case <-ctx.Done():
		u.App.QueueUpdateDraw(func() { u.pages.RemovePage("workflow-prompt") })
		return "", ctx.Err()
	}
}
func (u *UI) workflowSelect(ctx context.Context, message string, options []any) (any, error) {
	type result struct {
		value any
		err   error
	}
	answers := make(chan result, 1)
	u.App.QueueUpdateDraw(func() {
		list := tview.NewList()
		list.SetBorder(true).SetTitle(" " + display(message) + " · Esc 取消 ")
		done := func(value any, err error) {
			u.pages.RemovePage("workflow-select")
			u.App.SetFocus(u.list)
			select {
			case answers <- result{value, err}:
			default:
			}
		}
		for _, option := range options {
			value := option
			list.AddItem(clean(fmt.Sprint(value)), "", 0, func() { done(value, nil) })
		}
		list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			if e.Key() == tcell.KeyEscape {
				done(nil, fmt.Errorf("用户取消模板选择"))
				return nil
			}
			return e
		})
		u.pages.AddPage("workflow-select", list, true, true)
		u.App.SetFocus(list)
	})
	select {
	case r := <-answers:
		return r.value, r.err
	case <-ctx.Done():
		u.App.QueueUpdateDraw(func() { u.pages.RemovePage("workflow-select") })
		return nil, ctx.Err()
	}
}
