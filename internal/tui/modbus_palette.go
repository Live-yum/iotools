package tui

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"sort"
)

func (v *inspector) modbusActionMenu() {
	if v.modbus == nil {
		return
	}
	u := v.owner
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(" Modbus动作 · Enter执行所选入口 · 修改仍需确认 · Esc取消 ")
	actions := []string{}
	for action := range modbusActionLabels {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	close := func() { u.pages.RemovePage("modbus-action-menu"); u.App.SetFocus(v.table) }
	for _, action := range actions {
		selected := action
		key := v.modbus.keymap[action]
		list.AddItem(modbusActionLabels[action], fmt.Sprintf("%c · %s", key, action), 0, func() { close(); v.modbusDispatch(selected) })
	}
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		return e
	})
	u.pages.AddPage("modbus-action-menu", list, true, true)
	u.App.SetFocus(list)
}
