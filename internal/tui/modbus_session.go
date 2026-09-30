package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type modbusActivityEntry struct {
	time           time.Time
	level, message string
}
type modbusSessionStats struct {
	reads, writes, readErrors, writeErrors, cancelled uint64
	sum, min, max                                     float64
	lastError                                         string
	lastErrorTime, time                               time.Time
}
type modbusSessionState struct {
	scope, origin      string
	stats              modbusSessionStats
	entries            []modbusActivityEntry
	bytes              int
	dropped            uint64
	statsView, logView *tview.TextView
	follow, wrap       bool
	currentCancelled   bool
}

func (u *UI) ensureModbusSession() *modbusSessionState {
	if u.modbusSession == nil {
		origin, _ := filepath.Abs(u.path)
		u.modbusSession = &modbusSessionState{origin: origin, stats: modbusSessionStats{time: time.Now().UTC()}, follow: true}
	}
	return u.modbusSession
}
func modbusActivityAction(action string) string {
	switch action {
	case "read-holding", "read-input", "read-coils", "read-discrete", "write-register", "write-registers", "read-write-registers", "write-coil", "write-coils", "write-typed", "read-raw", "write-raw", "read-device-id", "scan-units", "sweep-holding", "search-holding":
		return action
	}
	return "未知动作"
}
func (u *UI) modbusSessionStart(r config.Request) {
	if r.Protocol != "modbus" {
		return
	}
	s := u.ensureModbusSession()
	s.currentCancelled = false
	scope := r.Endpoint + fmt.Sprintf("/%d", r.Int("unit", 1))
	if scope != s.scope {
		s.scope = scope
		s.stats = modbusSessionStats{time: time.Now().UTC()}
		u.modbusActivity("INFO", "切换设备/单元；通信统计已重置")
	}
	u.modbusActivity("INFO", "开始 "+modbusActivityAction(r.Action))
}
func (u *UI) modbusSessionEnd(r config.Request, err error) {
	if r.Protocol != "modbus" {
		return
	}
	if errors.Is(err, context.Canceled) {
		s := u.ensureModbusSession()
		if !s.currentCancelled {
			s.stats.cancelled++
			s.currentCancelled = true
			u.modbusRenderStats()
		}
	}
	if err != nil {
		u.modbusActivity("WARN", "请求结束："+engine.ModbusErrorClass(err))
	} else {
		u.modbusActivity("INFO", "请求完成")
	}
}
func (u *UI) modbusOperation(op engine.ModbusOperation) {
	s := u.ensureModbusSession()
	if !op.Success && op.ErrorClass != "已取消" && op.ErrorClass != "超时" && op.ErrorClass != "通信或响应校验失败" {
		op.ErrorClass = "通信或响应校验失败"
	}
	st := &s.stats
	if op.Cancelled {
		s.currentCancelled = true
		st.cancelled++
	} else if op.Success {
		if op.Write {
			st.writes++
		} else {
			st.reads++
			duration := float64(op.Duration) / float64(time.Millisecond)
			if duration < 0 {
				duration = 0
			}
			st.sum += duration
			if st.reads == 1 || duration < st.min {
				st.min = duration
			}
			if duration > st.max {
				st.max = duration
			}
		}
	} else {
		if op.Write {
			st.writeErrors++
		} else {
			st.readErrors++
		}
		st.lastError = op.ErrorClass
		st.lastErrorTime = op.Time
	}
	level, status := "INFO", "成功"
	if !op.Success {
		level, status = "WARN", op.ErrorClass
	}
	if op.Cancelled {
		level = "INFO"
	}
	u.modbusActivity(level, fmt.Sprintf("%s unit=%d address=%d count=%d · %s · %.3fms", modbusActivityAction(op.Action), op.Unit, op.Address, op.Count, status, float64(op.Duration)/float64(time.Millisecond)))
	u.modbusRenderStats()
}
func (u *UI) modbusActivity(level, message string) {
	s := u.ensureModbusSession()
	message = clean(message)
	runes := []rune(message)
	if len(runes) > 512 {
		message = string(runes[:512]) + "…"
	}
	if level != "INFO" && level != "WARN" && level != "ERROR" {
		level = "INFO"
	}
	entry := modbusActivityEntry{time.Now().UTC(), level, message}
	s.entries = append(s.entries, entry)
	s.bytes += len(message) + 64
	for len(s.entries) > 1000 || s.bytes > 1<<20 {
		old := s.entries[0]
		s.bytes -= len(old.message) + 64
		s.entries[0] = modbusActivityEntry{}
		s.entries = s.entries[1:]
		s.dropped++
	}
	u.modbusRenderActivity()
}
func (u *UI) modbusRenderStats() {
	s := u.ensureModbusSession()
	if s.statsView == nil {
		return
	}
	st := s.stats
	percent := 0.0
	if st.reads+st.readErrors > 0 {
		percent = float64(st.readErrors) * 100 / float64(st.reads+st.readErrors)
	}
	latency := "尚无成功读取"
	if st.reads > 0 {
		latency = fmt.Sprintf("最小 %.3fms / 平均 %.3fms / 最大 %.3fms", st.min, st.sum/float64(st.reads), st.max)
	}
	last := "无"
	if st.lastError != "" {
		last = st.lastError + " · " + st.lastErrorTime.UTC().Format(time.RFC3339)
	}
	s.statsView.SetText(fmt.Sprintf("统计起点（UTC）：%s\n\n读取：%d 成功 / %d 失败（%.1f%%）\n写入：%d 成功 / %d 失败或结果未知\n用户取消：%d\n\n成功读取延迟：%s\n最后失败：%s\n\n每次实际读取/写入操作计数，采样等待不计入延迟。\nraw/设备识别按整个逻辑操作计时；unit扫描包含每个探测。\n未进入通信的参数错误只记活动日志，不冒充设备失败。\n\nc 清空本机会话数据/统计 · Esc 返回 · F8 取消采样", st.time.Format(time.RFC3339), st.reads, st.readErrors, percent, st.writes, st.writeErrors, st.cancelled, latency, last))
}
func (u *UI) modbusClearSession() {
	s := u.ensureModbusSession()
	s.stats = modbusSessionStats{time: time.Now().UTC()}
	v := u.inspector
	if v != nil && v.protocol == "modbus" {
		v.values = map[int]map[string]any{}
		v.history = map[int][]float64{}
		if v.modbus != nil && v.modbus.interaction != nil {
			v.modbus.interaction.frames = nil
			v.modbus.interaction.words = 0
			v.modbus.interaction.dropped = 0
		}
		v.baseline = map[int]uint16{}
		v.rows = nil
		v.renderRegisters()
	}
	u.modbusActivity("INFO", "清空本机读取缓存、趋势和统计；设备未修改")
	u.modbusRenderStats()
}
func (u *UI) modbusStatsPanel() {
	s := u.ensureModbusSession()
	view := tview.NewTextView().SetWrap(true).SetScrollable(true)
	s.statsView = view
	view.SetBorder(true).SetTitle(" Modbus通信统计 · 设备/单元切换或明确清空后重置 ")
	close := func() { u.pages.RemovePage("modbus-stats"); s.statsView = nil; u.App.SetFocus(u.inspector.table) }
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			u.stop()
			return nil
		}
		if e.Key() == tcell.KeyRune && e.Rune() == 'c' {
			u.modbusClearSession()
			return nil
		}
		return e
	})
	u.modbusRenderStats()
	u.pages.AddPage("modbus-stats", view, true, true)
	u.App.SetFocus(view)
}
func (u *UI) modbusActivityText() string {
	s := u.ensureModbusSession()
	var b strings.Builder
	fmt.Fprintf(&b, "Modbus活动日志 · UTC · %d条 · 已淘汰%d条\n不含载荷、认证参数、端点或原始错误正文\n\n", len(s.entries), s.dropped)
	for _, entry := range s.entries {
		fmt.Fprintf(&b, "%s %-5s %s\n", entry.time.Format("2006-01-02T15:04:05.000Z"), entry.level, entry.message)
	}
	return b.String()
}
func (u *UI) modbusRenderActivity() {
	s := u.ensureModbusSession()
	if s.logView == nil {
		return
	}
	row, column := s.logView.GetScrollOffset()
	s.logView.SetText(u.modbusActivityText()).SetWrap(s.wrap)
	if s.follow {
		s.logView.ScrollToEnd()
	} else {
		s.logView.ScrollTo(row, column)
	}
	follow := "暂停跟随"
	if s.follow {
		follow = "跟随最新"
	}
	s.logView.SetTitle(" Modbus活动 · " + follow + " · f跟随 w换行 Ctrl-Y复制 e导出 Esc返回 ")
}
func (u *UI) modbusActivityPanel() {
	s := u.ensureModbusSession()
	s.follow = true
	s.logView = tview.NewTextView().SetScrollable(true)
	view := s.logView
	view.SetBorder(true)
	close := func() { u.pages.RemovePage("modbus-activity"); s.logView = nil; u.App.SetFocus(u.inspector.table) }
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyF8:
			u.stop()
			return nil
		case tcell.KeyCtrlY:
			u.copyText(u.modbusActivityText())
			return nil
		case tcell.KeyUp, tcell.KeyPgUp, tcell.KeyHome:
			s.follow = false
		case tcell.KeyEnd:
			s.follow = true
			u.modbusRenderActivity()
			return nil
		}
		if e.Key() == tcell.KeyRune {
			switch e.Rune() {
			case 'f':
				s.follow = true
				u.modbusRenderActivity()
				return nil
			case 'w':
				s.wrap = !s.wrap
				u.modbusRenderActivity()
				return nil
			case 'e':
				u.modbusActivityExport()
				return nil
			}
		}
		return e
	})
	u.modbusRenderActivity()
	u.pages.AddPage("modbus-activity", view, true, true)
	u.App.SetFocus(view)
}
func (u *UI) modbusActivityExport() {
	data := []byte(u.modbusActivityText())
	form := tview.NewForm().AddInputField("新建私有日志文件", "modbus-activity.log", 70, nil, nil)
	close := func() {
		u.pages.RemovePage("modbus-activity-export")
		if view := u.ensureModbusSession().logView; view != nil {
			u.App.SetFocus(view)
		} else {
			u.App.SetFocus(u.inspector.table)
		}
	}
	form.AddButton("明确导出", func() {
		if err := modbusWritePrivate(form.GetFormItem(0).(*tview.InputField).GetText(), data); err != nil {
			form.SetTitle("导出失败：" + display(err.Error()))
			return
		}
		close()
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			u.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" 导出当前活动快照 · 不覆盖已有文件 · Esc取消 ")
	u.pages.AddPage("modbus-activity-export", form, true, true)
	u.App.SetFocus(form)
}
