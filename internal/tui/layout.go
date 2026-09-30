package tui

import (
	"github.com/gdamore/tcell/v2"
	"strings"
)

// Preserve deliberate log scrolling while still following a live tail.
func (u *UI) updateResult(text string) {
	row, column := u.result.GetScrollOffset()
	_, _, _, height := u.result.GetInnerRect()
	oldLines := strings.Count(u.result.GetText(false), "\n") + 1
	follow := row+height >= oldLines
	u.result.SetText(text)
	if follow {
		u.result.ScrollToEnd()
	} else {
		u.result.ScrollTo(row, column)
	}
}
func (u *UI) resizeLayout(e *tcell.EventKey) bool {
	if e.Modifiers()&tcell.ModAlt == 0 {
		return false
	}
	_, _, width, height := u.pages.GetRect()
	switch e.Key() {
	case tcell.KeyLeft:
		if u.listWidth > 16 {
			u.listWidth -= 2
		}
		u.topLayout.ResizeItem(u.list, u.listWidth, 0)
	case tcell.KeyRight:
		if u.listWidth < width-30 {
			u.listWidth += 2
		}
		u.topLayout.ResizeItem(u.list, u.listWidth, 0)
	case tcell.KeyUp:
		if u.resultHeight == 0 {
			u.resultHeight = max(5, height/2)
		}
		if u.resultHeight < height-10 {
			u.resultHeight++
		}
		u.mainLayout.ResizeItem(u.resultPages, u.resultHeight, 0)
	case tcell.KeyDown:
		if u.resultHeight == 0 {
			u.resultHeight = max(5, height/2)
		}
		if u.resultHeight > 5 {
			u.resultHeight--
		}
		u.mainLayout.ResizeItem(u.resultPages, u.resultHeight, 0)
	default:
		return false
	}
	return true
}
