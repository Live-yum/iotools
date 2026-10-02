package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The upstream UI selects a finite chain of object keys / array indices. This
// JSONPath spelling supports exactly that scope, without recursive/filter scans.
func mqttParseSelector(path string) ([]mqttSelector, error) {
	if len(path) > 1024 || path == "" || path[0] != '$' {
		return nil, fmt.Errorf("路径应以$开头且最多1024字节")
	}
	selectors := []mqttSelector{}
	for at := 1; at < len(path); {
		if len(selectors) >= 32 {
			return nil, fmt.Errorf("路径最多32层")
		}
		switch path[at] {
		case '.':
			at++
			start := at
			for at < len(path) && path[at] != '.' && path[at] != '[' {
				if strings.ContainsRune("*?() \t\r\n]", rune(path[at])) {
					return nil, fmt.Errorf("仅支持字段与数组下标")
				}
				at++
			}
			if at == start {
				return nil, fmt.Errorf("字段名不能为空")
			}
			selectors = append(selectors, mqttSelector{key: path[start:at]})
		case '[':
			at++
			if at >= len(path) {
				return nil, fmt.Errorf("未闭合数组下标")
			}
			if path[at] == '\'' || path[at] == '"' {
				quote := path[at]
				at++
				var key strings.Builder
				closed := false
				for at < len(path) {
					ch := path[at]
					at++
					if ch == quote {
						closed = true
						break
					}
					if ch == '\\' {
						if at >= len(path) {
							return nil, fmt.Errorf("无效路径转义")
						}
						ch = path[at]
						at++
						if ch != quote && ch != '\\' {
							return nil, fmt.Errorf("路径只允许引号/反斜杠转义")
						}
					}
					key.WriteByte(ch)
				}
				if !closed || at >= len(path) || path[at] != ']' {
					return nil, fmt.Errorf("未闭合字段选择")
				}
				at++
				selectors = append(selectors, mqttSelector{key: key.String()})
			} else {
				start := at
				for at < len(path) && path[at] >= '0' && path[at] <= '9' {
					at++
				}
				if start == at || at >= len(path) || path[at] != ']' {
					return nil, fmt.Errorf("数组下标需非负整数")
				}
				n, err := strconv.Atoi(path[start:at])
				if err != nil || n > 16383 {
					return nil, fmt.Errorf("数组下标需0..16383")
				}
				at++
				selectors = append(selectors, mqttSelector{array: true, index: n})
			}
		default:
			return nil, fmt.Errorf("仅支持$.字段和$[下标]/$['字段']")
		}
	}
	return selectors, nil
}
func (v *inspector) mqttHistorySelectorForm() {
	h := v.mqttHistory
	if h == nil {
		return
	}
	index := ""
	if h.binaryIndex >= 0 {
		index = strconv.Itoa(h.binaryIndex)
	}
	form := tview.NewForm().AddInputField("字段JSONPath", h.path, 70, nil, nil).AddInputField("二进制字节0..4095(空=字段)", index, 12, nil, nil)
	close := func() {
		v.owner.pages.RemovePage("mqtt-history-selector")
		if h.graph {
			v.owner.App.SetFocus(h.chart)
		} else {
			v.owner.App.SetFocus(h.table)
		}
	}
	form.AddButton("应用", func() {
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		selectors, err := mqttParseSelector(path)
		if err != nil {
			form.SetTitle("路径无效：" + display(err.Error()))
			return
		}
		raw := strings.TrimSpace(form.GetFormItem(1).(*tview.InputField).GetText())
		index := -1
		if raw != "" {
			index, err = strconv.Atoi(raw)
			if err != nil || index < 0 || index > 4095 {
				form.SetTitle("字节下标需0..4095")
				return
			}
		}
		h.path = path
		h.selectors = selectors
		h.binaryIndex = index
		close()
		v.mqttHistoryRender()
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			v.owner.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" 选择历史数值 · 如$.temperature或$.values[0] · Esc取消 ")
	v.owner.pages.AddPage("mqtt-history-selector", form, true, true)
	v.owner.App.SetFocus(form)
}
func mqttNumeric(value any, format string) (float64, bool) {
	var n float64
	var err error
	switch x := value.(type) {
	case bool:
		if x {
			n = 1
		}
	case json.Number:
		n, err = x.Float64()
	case float64:
		n = x
	case float32:
		n = float64(x)
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	case uint64:
		n = float64(x)
	case string:
		fields := strings.Fields(x)
		if len(fields) == 0 {
			return 0, false
		}
		n, err = strconv.ParseFloat(fields[0], 64)
	case []any:
		n = float64(len(x))
	case map[string]any:
		if format != "messagepack" {
			return 0, false
		}
		if _, wrapper := x["messagepack_type"]; wrapper {
			return 0, false
		}
		n = float64(len(x))
	default:
		return 0, false
	}
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

type mqttGraphPoint struct {
	entry *mqttHistoryEntry
	value float64
}

func mqttGraphPoints(h *mqttHistoryState, entries []*mqttHistoryEntry) []mqttGraphPoint {
	points := []mqttGraphPoint{}
	for _, entry := range entries {
		if entry.retained {
			continue
		}
		value, ok := h.selectedValue(entry)
		if !ok {
			continue
		}
		number, ok := mqttNumeric(value, entry.format)
		if ok {
			points = append(points, mqttGraphPoint{entry, number})
		}
	}
	return points
}
func mqttRenderGraph(h *mqttHistoryState, entries []*mqttHistoryEntry) string {
	points := mqttGraphPoints(h, entries)
	if len(points) < 2 {
		return "至少需要2条非retained、可解释的历史值\n\n支持数值、布尔、数字开头文本、数组长度；MessagePack map使用项数\n按s选择JSONPath字段或二进制字节\n保留回放消息/缺失字段/非有限值/截断结构不进入图表"
	}
	const width, height = 70, 12
	low, high := points[0].value, points[0].value
	for _, point := range points {
		low = math.Min(low, point.value)
		high = math.Max(high, point.value)
	}
	grid := make([][]rune, height)
	for y := range grid {
		grid[y] = []rune(strings.Repeat(" ", width))
	}
	start, end := points[0].entry.received, points[len(points)-1].entry.received
	duration := end.Sub(start).Seconds()
	scale := math.Max(math.Abs(low), math.Abs(high))
	if scale == 0 {
		scale = 1
	}
	scaledLow, scaledHigh := low/scale, high/scale
	previousX, previousY := -1, -1
	for index, point := range points {
		fraction := float64(index) / float64(len(points)-1)
		if duration > 0 {
			fraction = point.entry.received.Sub(start).Seconds() / duration
		}
		fraction = math.Max(0, math.Min(1, fraction))
		x := int(math.Round(fraction * (width - 1)))
		y := height / 2
		if high != low {
			ratio := (point.value/scale - scaledLow) / (scaledHigh - scaledLow)
			y = height - 1 - int(math.Round(ratio*(height-1)))
		}
		if y < 0 {
			y = 0
		}
		if y >= height {
			y = height - 1
		}
		if previousX >= 0 {
			steps := int(math.Max(math.Abs(float64(x-previousX)), math.Abs(float64(y-previousY))))
			for step := 1; step < steps; step++ {
				ratio := float64(step) / float64(steps)
				px := previousX + int(math.Round(float64(x-previousX)*ratio))
				py := previousY + int(math.Round(float64(y-previousY)*ratio))
				grid[py][px] = '·'
			}
		}
		grid[y][x] = '●'
		previousX, previousY = x, y
	}
	var out strings.Builder
	fmt.Fprintf(&out, "有效点:%d / 保留历史:%d   最小:%g  最大:%g  最新:%g\n\n", len(points), len(entries), low, high, points[len(points)-1].value)
	for y, row := range grid {
		label := "           "
		if y == 0 {
			label = fmt.Sprintf("%10.4g ", high)
		} else if y == height-1 {
			label = fmt.Sprintf("%10.4g ", low)
		}
		out.WriteString(label + "│" + string(row) + "\n")
	}
	out.WriteString("           └" + strings.Repeat("─", width) + "\n")
	fmt.Fprintf(&out, "            %s UTC%s%s UTC\n", start.UTC().Format("15:04:05.000"), strings.Repeat(" ", 38), end.UTC().Format("15:04:05.000"))
	out.WriteString("\n接收时间轴；排除retained回放。图表使用float64近似，精确整数见历史表\n字段缺失时跳过，不以根对象替代")
	return out.String()
}
