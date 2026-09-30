package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Long targets and warnings remain scrollable rather than disappearing beyond
// a fixed modal. Focus starts in the text; entering the button row selects Cancel.
func (u *UI) httpConfirmation(title, text, approve string, done func(bool)) *tview.Flex {
	view := tview.NewTextView().SetText(clean(text)).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(title + " · Tab按钮 ShiftTab正文 Esc取消 ")
	buttons := tview.NewForm().AddButton("取消", func() { done(false) }).AddButton(approve, func() { done(true) })
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			done(false)
			return nil
		}
		if e.Key() == tcell.KeyTab && view.HasFocus() {
			u.App.SetFocus(buttons)
			return nil
		}
		if e.Key() == tcell.KeyBacktab && buttons.HasFocus() {
			u.App.SetFocus(view)
			return nil
		}
		return e
	})
	return panel
}
