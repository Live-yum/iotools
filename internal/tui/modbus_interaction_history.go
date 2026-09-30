package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const modbusFrameLimit = 128
const modbusWordLimit = 16384

// Frames preserve response boundaries. No multiword value ever joins rows from
// separate frames, even if their timestamps happen to be equal.
type modbusFrame struct {
	at    time.Time
	words map[int]uint16
}
type modbusInteraction struct {
	frames         []modbusFrame
	words, dropped int
	interpreter    *engine.ModbusInterpreter
	view           *tview.TextView
	address        int
	field          string
	graph, follow  bool
}

var modbusNumericFields = []string{"u16", "i16", "f16", "u32", "i32", "f32", "u64", "i64", "f64", "bcd", "bcd32", "custom_numeric"}

func (v *inspector) modbusCapture(e engine.Event) {
	if v.modbus == nil || v.modbus.interaction == nil || e.Kind != "registers" {
		return
	}
	h := v.modbus.interaction
	rows, ok := e.Data.([]map[string]any)
	if !ok || len(rows) == 0 || len(rows) > 2000 {
		return
	}
	frame := modbusFrame{at: e.Time, words: map[int]uint16{}}
	for _, row := range rows {
		a, ok := row["address"].(int)
		n, nok := row["u16"].(uint16)
		if !ok || !nok || a < 0 || a > 65535 {
			return
		}
		if _, duplicate := frame.words[a]; duplicate {
			return
		}
		frame.words[a] = n
		if frame.at.IsZero() {
			frame.at, _ = row["sampled_at"].(time.Time)
		}
	}
	h.frames = append(h.frames, frame)
	h.words += len(frame.words)
	for len(h.frames) > modbusFrameLimit || h.words > modbusWordLimit {
		h.words -= len(h.frames[0].words)
		h.frames[0] = modbusFrame{}
		h.frames = h.frames[1:]
		h.dropped++
	}
	if h.view != nil && h.follow {
		v.modbusRenderInteraction()
	}
}
func modbusNumber(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(fmt.Sprint(value), 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

type modbusPoint struct {
	at    time.Time
	value float64
	frame int
}

func (h *modbusInteraction) series(address int, field string) []modbusPoint {
	points := []modbusPoint{}
	for i, frame := range h.frames {
		row, err := h.interpreter.Field(frame.words, address)
		if err != nil {
			continue
		}
		if n, ok := modbusNumber(row[field]); ok {
			points = append(points, modbusPoint{frame.at, n, i})
		}
	}
	return points
}
func (h *modbusInteraction) latest(address int) (map[string]any, time.Time) {
	for i := len(h.frames) - 1; i >= 0; i-- {
		f := h.frames[i]
		if _, ok := f.words[address]; ok {
			row, _ := h.interpreter.Field(f.words, address)
			return row, f.at
		}
	}
	return nil, time.Time{}
}
func modbusPointStats(points []modbusPoint) (low, high, average float64) {
	if len(points) == 0 {
		return
	}
	low, high = points[0].value, points[0].value
	scale := 0.0
	for _, p := range points {
		low = math.Min(low, p.value)
		high = math.Max(high, p.value)
		scale = math.Max(scale, math.Abs(p.value))
	}
	if scale == 0 {
		return
	}
	for _, p := range points {
		average += p.value / scale / float64(len(points))
	}
	average = math.Max(-1, math.Min(1, average)) * scale
	return
}
func modbusPlot(points []modbusPoint) string {
	if len(points) < 2 {
		return "至少需要2个同响应内可解释的有限值；缺失/非有限值不进入图表"
	}
	low, high, avg := modbusPointStats(points)
	const w, h = 64, 10
	grid := make([][]rune, h)
	for y := range grid {
		grid[y] = []rune(strings.Repeat(" ", w))
	}
	scale := math.Max(math.Abs(low), math.Abs(high))
	if scale == 0 {
		scale = 1
	}
	lo, hi := low/scale, high/scale
	start, end := points[0].at, points[len(points)-1].at
	span := end.Sub(start).Seconds()
	for i, p := range points {
		fraction := float64(i) / float64(len(points)-1)
		if span > 0 {
			fraction = p.at.Sub(start).Seconds() / span
		}
		fraction = math.Max(0, math.Min(1, fraction))
		x := int(math.Round(fraction * (w - 1)))
		y := h / 2
		if high != low {
			y = h - 1 - int(math.Round((p.value/scale-lo)/(hi-lo)*(h-1)))
		}
		if y < 0 {
			y = 0
		}
		if y >= h {
			y = h - 1
		}
		grid[y][x] = '●'
	}
	var out strings.Builder
	fmt.Fprintf(&out, "有效点:%d  最小:%g  最大:%g  平均:%g\n", len(points), low, high, avg)
	for _, row := range grid {
		out.WriteString(string(row))
		out.WriteByte('\n')
	}
	fmt.Fprintf(&out, "UTC %s → %s\n仅画采样点，不跨缺失响应插值；浮点图/统计为近似值\n", start.UTC().Format("15:04:05.000"), end.UTC().Format("15:04:05.000"))
	return out.String()
}
func (v *inspector) modbusInspect(graph bool) {
	if v.modbus == nil || v.modbus.interaction == nil {
		return
	}
	address, ok := v.modbusSelectedAddress()
	if !ok {
		v.owner.setStatus("请先选择已收到的寄存器")
		return
	}
	h := v.modbus.interaction
	h.address = address
	h.graph = graph
	h.follow = true
	h.field = "u16"
	_, column := v.table.GetSelection()
	if !v.matrix && column < len(v.modbus.columns) {
		key := v.modbus.columns[column]
		if key == "custom" {
			key = "custom_numeric"
		}
		for _, f := range modbusNumericFields {
			if f == key {
				h.field = f
			}
		}
	}
	view := tview.NewTextView().SetScrollable(true).SetWrap(false)
	h.view = view
	close := func() { h.view = nil; v.owner.pages.RemovePage("modbus-inspect"); v.owner.App.SetFocus(v.table) }
	view.SetBorder(true).SetTitle(" 寄存器详情 · g图/详情 ←/→字段 f跟随 c清本机历史 Ctrl-Y复制 Esc关闭 F8停止 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyF8:
			v.owner.stop()
			return nil
		case tcell.KeyCtrlY:
			v.owner.copyText(view.GetText(false))
			return nil
		case tcell.KeyLeft, tcell.KeyRight:
			idx := 0
			for i, f := range modbusNumericFields {
				if f == h.field {
					idx = i
				}
			}
			delta := 1
			if e.Key() == tcell.KeyLeft {
				delta = -1
			}
			h.field = modbusNumericFields[(idx+delta+len(modbusNumericFields))%len(modbusNumericFields)]
			v.modbusRenderInteraction()
			return nil
		}
		switch e.Rune() {
		case 'g':
			h.graph = !h.graph
			v.modbusRenderInteraction()
			return nil
		case 'f':
			h.follow = !h.follow
			v.modbusRenderInteraction()
			return nil
		case 'c':
			h.frames = nil
			h.words = 0
			h.dropped = 0
			v.modbusRenderInteraction()
			return nil
		}
		return e
	})
	v.owner.pages.AddPage("modbus-inspect", view, true, true)
	v.owner.App.SetFocus(view)
	v.modbusRenderInteraction()
}
func (v *inspector) modbusRenderInteraction() {
	h := v.modbus.interaction
	if h == nil || h.view == nil {
		return
	}
	row, at := h.latest(h.address)
	var out strings.Builder
	fmt.Fprintf(&out, "空间:%s  Unit:%d  地址:%d  字序:%s\n采样UTC:%s  跟随:%t  响应:%d/%d 丢弃:%d\n标签:%s\n选中字段:%s\n\n", modbusReadAction(v.modbus.request.Action), v.modbus.request.Int("unit", 1), h.address, v.modbus.request.String("word_order", "ABCD"), at.UTC().Format(time.RFC3339Nano), h.follow, len(h.frames), modbusFrameLimit, h.dropped, v.labels[h.address], h.field)
	if h.graph {
		out.WriteString(modbusPlot(h.series(h.address, h.field)))
	} else {
		fmt.Fprintln(&out, "字段             NOW(精确文本)             MIN / MAX / AVG (近似) / 点数")
		for _, field := range modbusNumericFields {
			points := h.series(h.address, field)
			low, high, avg := modbusPointStats(points)
			current := "未收到完整同响应数据"
			if value, ok := row[field]; ok {
				current = fmt.Sprint(value)
			}
			fmt.Fprintf(&out, "%-16s %-25s", field, current)
			if len(points) > 0 {
				fmt.Fprintf(&out, " %g / %g / %g / %d", low, high, avg, len(points))
			}
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "\nHEX: %v\nBIT: %v\nASCII: %v\n规则: %v\n", row["hex"], row["binary"], row["ascii"], row["custom"])
	}
	fmt.Fprintln(&out, "\n只使用同一响应的原始词；窗口切换/请求重启会清空此历史。\n缺失操作数不借用旧值；枚举/位标签只作文本。未发起额外设备读取。")
	h.view.SetText(clean(out.String()))
}
func (v *inspector) modbusCycleOrder() {
	if v.modbus == nil {
		return
	}
	if v.owner.running {
		v.owner.modal("请先F8停止采样再本机重解释")
		return
	}
	orders := []string{"ABCD", "BADC", "CDAB", "DCBA"}
	idx := 0
	for i, s := range orders {
		if s == v.modbus.request.String("word_order", "ABCD") {
			idx = i
		}
	}
	r := copyRequest(v.modbus.request)
	r.Params["word_order"] = orders[(idx+1)%len(orders)]
	interpreter, err := engine.NewModbusInterpreter(r)
	if err != nil {
		v.owner.modal(err.Error())
		return
	}
	v.modbus.request = r
	v.modbus.interaction.interpreter = interpreter
	// Rebuild latest rows independently for each frame; newer incomplete rows
	// replace older complete interpretations instead of retaining stale fields.
	v.values = map[int]map[string]any{}
	for _, frame := range v.modbus.interaction.frames {
		rows, err := interpreter.Interpret(frame.words)
		if err != nil {
			continue
		}
		for _, row := range rows {
			row["sampled_at"] = frame.at
			v.values[row["address"].(int)] = row
		}
	}
	v.renderRegisters()
	v.owner.setStatus("字序临时改为" + r.String("word_order", "") + "；仅重解释已收响应，R可保存/明确读取")
}
