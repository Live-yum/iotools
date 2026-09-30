package tui

import (
	"encoding/base64"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"io"
	"os"
)

func writeTerminalClipboard(w io.Writer, text string) error {
	if len(text) > 1<<20 {
		return fmt.Errorf("复制内容超过1MiB，请使用文件导出")
	}
	_, e := io.WriteString(w, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(text))+"\x07")
	return e
}
func (u *UI) copyText(text string) {
	if len(text) > 1<<20 {
		u.modal("复制内容超过1MiB，请使用文件导出")
		return
	}
	previous := u.App.GetFocus()
	view := tview.NewTextView().SetText(text).SetScrollable(true).SetWrap(true)
	view.SetBorder(true).SetTitle(" 复制预览 · 不自动发送 · 终端可能将OSC52复制到本地/远程剪贴板 ")
	close := func() { u.pages.RemovePage("clipboard"); u.App.SetFocus(previous) }
	cancel := tview.NewButton("取消").SetSelectedFunc(close)
	copyButton := tview.NewButton("明确复制").SetSelectedFunc(func() {
		close()
		handled, e := setSystemClipboard(text)
		if !handled && e == nil {
			u.App.Suspend(func() { e = writeTerminalClipboard(os.Stdout, text) })
		}
		if e != nil {
			u.modal("复制失败：" + e.Error())
			return
		}
		if handled {
			u.setStatus("已复制到系统剪贴板")
		} else {
			u.setStatus("已发送OSC52复制请求；终端可拒绝，请检查剪贴板，或使用文件导出")
		}
	})
	buttons := tview.NewFlex().AddItem(cancel, 0, 1, false).AddItem(copyButton, 0, 1, false)
	layout := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 1, 0, false)
	focus := 0
	widgets := []tview.Primitive{view, cancel, copyButton}
	layout.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
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
	u.pages.AddPage("clipboard", layout, true, true)
	u.App.SetFocus(view)
}
