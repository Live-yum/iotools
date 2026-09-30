package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"sort"
)

type liveUASubscription struct {
	Request config.Request
	Cancel  context.CancelFunc
	Active  bool
	Status  string
	Value   any
	Count   int
}

func uaSubscriptionKey(r config.Request, node string) string { return r.Endpoint + "\x00" + node }
func (u *UI) activeUASubscriptions() int {
	n := 0
	for _, s := range u.uaSubscriptions {
		if s.Active {
			n++
		}
	}
	return n
}
func (u *UI) subscribeUA(node string) {
	if u.quitting || u.lastRequest.Protocol != "opcua" {
		return
	}
	if u.uaSubscriptions == nil {
		u.uaSubscriptions = map[string]*liveUASubscription{}
	}
	source := copyRequest(u.lastRequest)
	key := uaSubscriptionKey(source, node)
	if old := u.uaSubscriptions[key]; old != nil && old.Active {
		u.setStatus("此节点已经订阅 · Shift+S取消 · F10实时面板")
		return
	}
	if u.activeUASubscriptions() >= 16 {
		u.modal("最多同时订阅16个节点，请先取消部分订阅")
		return
	}
	for k, old := range u.uaSubscriptions {
		if !old.Active {
			delete(u.uaSubscriptions, k)
		}
	}
	source.Action = "subscribe"
	source.Timeout = "24h"
	delete(source.Params, "node_ids")
	delete(source.Params, "browse_path")
	source.Params["node_id"] = node
	source.Params["max_events"] = 100000
	ctx, cancel := context.WithCancel(context.Background())
	entry := &liveUASubscription{Request: source, Cancel: cancel, Active: true, Status: "正在连接"}
	u.uaSubscriptions[key] = entry
	u.setStatus("已启动独立订阅，可以继续浏览 · F10实时面板 · Shift+S单节点取消 · F8全部取消")
	go func() {
		err := engine.Run(ctx, source, false, func(event engine.Event) {
			if event.Kind != "notification" && event.Kind != "reconnecting" && event.Kind != "connected" {
				return
			}
			u.App.QueueUpdateDraw(func() {
				if event.Kind == "notification" {
					if m, ok := event.Data.(map[string]any); ok {
						entry.Value = m["value"]
						entry.Count++
						entry.Status = fmt.Sprint(m["status"])
					}
				} else if event.Kind == "reconnecting" {
					entry.Status = "重连并恢复订阅"
				} else {
					entry.Status = "已连接，等待通知"
				}
				u.renderUASubscriptions()
			})
		})
		cancel()
		u.App.QueueUpdateDraw(func() {
			entry.Active = false
			if err != nil {
				entry.Status = "已停止：" + err.Error()
			} else {
				entry.Status = "已完成"
			}
			u.renderUASubscriptions()
			if u.quitting && !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
				u.App.Stop()
			}
		})
	}()
}
func (u *UI) unsubscribeUA(node string) {
	if s := u.uaSubscriptions[uaSubscriptionKey(u.lastRequest, node)]; s != nil && s.Active {
		s.Status = "正在取消"
		s.Cancel()
		u.renderUASubscriptions()
	}
}
func (u *UI) stopUASubscriptions() {
	for _, s := range u.uaSubscriptions {
		if s.Active {
			s.Cancel()
		}
	}
}
func (u *UI) showUASubscriptions() {
	table := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	table.SetBorder(true).SetTitle(" OPC UA 后台订阅 · Enter读取 · S取消选中 · Esc关闭（订阅继续） ")
	u.uaSubTable = table
	close := func() { u.pages.RemovePage("ua-live"); u.uaSubTable = nil; u.App.SetFocus(u.inspector.tree) }
	table.SetSelectedFunc(func(row, col int) {
		if row < 1 || u.running {
			return
		}
		key, ok := table.GetCell(row, 0).GetReference().(string)
		if !ok {
			return
		}
		s := u.uaSubscriptions[key]
		if s == nil {
			return
		}
		r := copyRequest(s.Request)
		r.Action = "read"
		delete(r.Params, "max_events")
		r.Timeout = "15s"
		close()
		u.start(r)
	})
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Rune() == 'S' {
			row, _ := table.GetSelection()
			if row > 0 {
				key, _ := table.GetCell(row, 0).GetReference().(string)
				if s := u.uaSubscriptions[key]; s != nil {
					s.Cancel()
				}
			}
			return nil
		}
		return e
	})
	u.renderUASubscriptions()
	u.pages.AddPage("ua-live", table, true, true)
	u.App.SetFocus(table)
}
func (u *UI) renderUASubscriptions() {
	if u.uaSubTable == nil {
		return
	}
	table := u.uaSubTable
	row, col := table.GetSelection()
	table.Clear()
	for i, h := range []string{"节点", "端点", "最新值", "通知数", "状态"} {
		table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	keys := []string{}
	for key := range u.uaSubscriptions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		s := u.uaSubscriptions[key]
		value, _ := json.Marshal(s.Value)
		if len(value) > 1024 {
			value = append(value[:1024], []byte("…")...)
		}
		values := []string{s.Request.String("node_id", ""), s.Request.Endpoint, string(value), fmt.Sprint(s.Count), s.Status}
		for j, text := range values {
			table.SetCell(i+1, j, tview.NewTableCell(display(text)).SetReference(key))
		}
	}
	if row < table.GetRowCount() {
		table.Select(row, col)
	}
}
