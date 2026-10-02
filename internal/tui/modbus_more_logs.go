package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func modbusReviewedLogPath(path string, confirmed bool) (string, error) {
	if path == "" {
		if confirmed {
			return "", fmt.Errorf("勾选启用时必须填写日志文件路径")
		}
		return "", nil
	}
	if !confirmed {
		return "", fmt.Errorf("填写路径后请明确勾选启用写日志，或清空路径")
	}
	if len(path) > 4096 || strings.TrimSpace(path) == "" || strings.Contains(path, "${") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("日志路径无效、包含模板或超过4096字节")
	}
	return path, nil
}
func (v *inspector) modbusWriteLogForm() {
	path := ""
	if v.modbus != nil {
		path = v.modbus.request.String("write_log_file", "")
	}
	form := tview.NewForm().AddInputField("本机写日志JSONL路径", path, 70, nil, nil)
	close := func() { v.owner.pages.RemovePage("modbus-log-file"); v.owner.App.SetFocus(v.table) }
	form.AddButton("只读打开", func() {
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		if _, err := modbusReviewedLogPath(path, true); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		entries, err := engine.ReadModbusWriteLog(path)
		if err != nil {
			form.SetTitle("读取失败：" + display(err.Error()))
			return
		}
		close()
		v.modbusWriteLogPanel(path, entries)
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" 本机写日志 · 只读尾部2MiB/1000条 · 不连接设备 · Esc取消 ")
	v.owner.pages.AddPage("modbus-log-file", form, true, true)
	v.owner.App.SetFocus(form)
}
func (v *inspector) modbusWriteLogPanel(path string, entries []engine.ModbusWriteLogEntry) {
	table := tview.NewTable().SetFixed(1, 0).SetSelectable(true, false)
	table.SetBorder(true).SetTitle(" 写日志(只读快照) · attempt不代表成功 · Enter详情 · Esc返回 ")
	for col, text := range []string{"UTC时间", "阶段", "unit", "地址", "数量", "类型", "之前(若已知)", "新值", "功能码", "结果"} {
		table.SetCell(0, col, tview.NewTableCell(text).SetSelectable(false).SetTextColor(tcell.ColorAqua))
	}
	for i, entry := range entries {
		previous, value := "?", ""
		if entry.Previous != nil {
			b, _ := json.Marshal(entry.Previous)
			previous = string(b)
		}
		if entry.Value != nil {
			b, _ := json.Marshal(entry.Value)
			value = string(b)
		}
		cells := []string{entry.Timestamp.UTC().Format("2006-01-02 15:04:05.000"), entry.Phase, fmt.Sprint(entry.Unit), fmt.Sprint(entry.Address), fmt.Sprint(entry.Count), entry.Type, previous, value, entry.Function, entry.Status}
		for col, text := range cells {
			table.SetCell(i+1, col, tview.NewTableCell(display(modbusLogText(text))).SetReference(i))
		}
	}
	close := func() { v.owner.pages.RemovePage("modbus-write-logs"); v.owner.App.SetFocus(v.table) }
	table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() != tcell.KeyEnter {
			return e
		}
		row, _ := table.GetSelection()
		if row < 1 || row > len(entries) {
			return nil
		}
		data, _ := json.MarshalIndent(entries[row-1], "", "  ")
		view := tview.NewTextView().SetText(clean("文件：" + path + "\n\n" + string(data))).SetScrollable(true).SetWrap(true)
		view.SetBorder(true).SetTitle(" 写日志完整记录 · 时间/目标/操作ID/结果 · Esc返回 ")
		view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			if e.Key() == tcell.KeyF8 {
				v.owner.stop()
				return nil
			}
			if e.Key() == tcell.KeyEscape {
				v.owner.pages.RemovePage("modbus-log-detail")
				v.owner.App.SetFocus(table)
				return nil
			}
			return e
		})
		v.owner.pages.AddPage("modbus-log-detail", view, true, true)
		v.owner.App.SetFocus(view)
		return nil
	})
	if len(entries) > 0 {
		table.Select(len(entries), 0)
	}
	v.owner.pages.AddPage("modbus-write-logs", table, true, true)
	v.owner.App.SetFocus(table)
}

func modbusLogText(text string) string {
	runes := []rune(text)
	if len(runes) > 256 {
		return string(runes[:256]) + "…"
	}
	return text
}
