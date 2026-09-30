package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Each remappable action terminates here. It never synthesizes a global key or
// falls through to a second handler that could reinterpret a read as a write.
func (v *inspector) modbusDispatch(action string) {
	if v.modbus == nil {
		return
	}
	u := v.owner
	address, selected := v.modbusSelectedAddress()
	switch action {
	case "device":
		v.modbusDeviceForm()
	case "custom-rule":
		v.modbusRuleForm(-1)
	case "annotations":
		v.modbusAnnotationPanel(false)
	case "device-id":
		v.modbusDeviceIDForm()
	case "raw":
		v.modbusRawForm()
	case "sweep":
		v.modbusRangeForm(false)
	case "unit-scan":
		v.modbusRangeForm(true)
	case "go-to":
		v.modbusGoToForm()
	case "read-controls":
		v.modbusReadForm(nil)
	case "inspect":
		v.modbusInspect(false)
	case "graph":
		v.modbusInspect(true)
	case "write":
		v.modbusWriteForm()
	case "word-order":
		v.modbusCycleOrder()
	case "unit":
		v.modbusReadForm(nil)
	case "register-type", "page-up", "page-down", "batch-decrease", "batch-increase":
		r := modbusReadRequest(v.modbus.request)
		switch action {
		case "register-type":
			for i, a := range modbusReadActions {
				if a == r.Action {
					r.Action = modbusReadActions[(i+1)%len(modbusReadActions)]
					break
				}
			}
		case "page-up":
			r.Params["address"] = r.Int("address", 0) - r.Int("count", 1)
		case "page-down":
			r.Params["address"] = r.Int("address", 0) + r.Int("count", 1)
		case "batch-decrease":
			r.Params["count"] = r.Int("count", 1) - 1
		case "batch-increase":
			r.Params["count"] = r.Int("count", 1) + 1
		}
		if r.Int("address", 0) < 0 || r.Int("count", 1) < 1 || r.Int("count", 1) > 125 || r.Int("address", 0)+r.Int("count", 1) > 65536 {
			u.setStatus("读取窗口越界；未截断或改变设置")
			return
		}
		v.modbusReadForm(&r)
	case "refresh":
		if u.running {
			u.setStatus("已在采样；F8可取消")
			return
		}
		u.start(modbusReadRequest(v.modbus.request))
	case "pause":
		u.toggleModbusPause()
	case "stats":
		u.modbusStatsPanel()
	case "activity":
		u.modbusActivityPanel()
	case "rotation":
		u.modbusRotationForm()
	case "clear-session":
		u.modbusClearSession()
	case "copy-column":
		_, col := v.table.GetSelection()
		if col < 0 || col >= v.table.GetColumnCount() {
			return
		}
		var text strings.Builder
		for row := 0; row < v.table.GetRowCount(); row++ {
			cell := v.table.GetCell(row, col)
			value := strings.ReplaceAll(tview.Unescape(cell.Text), "\n", " ")
			if text.Len()+len(value) > 1<<20 {
				u.setStatus("列复制超过1MiB；请使用CSV导出")
				return
			}
			text.WriteString(value)
			text.WriteByte('\n')
		}
		u.copyText(text.String())
	case "matrix":
		v.matrix = !v.matrix
		v.renderRegisters()
	case "pin":
		if !selected {
			return
		}
		v.pins[address] = !v.pins[address]
		if err := v.persistAnnotations(); err != nil {
			u.setStatus("固定项保存失败：" + err.Error())
		}
		v.renderRegisters()
	case "label":
		if selected {
			v.label(address)
		}
	case "filter":
		v.filtered = !v.filtered
		v.renderRegisters()
	case "baseline":
		v.baseline = map[int]uint16{}
		for a, row := range v.values {
			if n, ok := row["u16"].(uint16); ok {
				v.baseline[a] = n
			}
		}
		v.renderRegisters()
	case "snapshot-save":
		v.snapshot(false)
	case "snapshot-open":
		v.snapshot(true)
	case "columns":
		v.modbusColumnsPanel()
	case "keymap":
		v.modbusKeymapForm()
	case "import":
		v.modbusImportForm()
	case "export":
		v.modbusExportForm(false)
	case "dump":
		v.modbusExportForm(true)
	case "more":
		v.modbusMoreMenu()
	default:
		u.setStatus(fmt.Sprintf("未知Modbus动作:%s", action))
	}
}
func (v *inspector) modbusInteractionKey(e *tcell.EventKey) *tcell.EventKey {
	e = v.modbusToolKey(e)
	if e == nil {
		return nil
	}
	if e.Key() == tcell.KeyEnter {
		v.modbusDispatch("inspect")
		return nil
	}
	if v.matrix && (e.Rune() == '+' || e.Rune() == '-') {
		delta := 1
		if e.Rune() == '-' {
			delta = -1
		}
		next := v.matrixColumns + delta
		if next >= 1 && next <= 16 {
			v.matrixColumns = next
			v.renderRegisters()
		}
		return nil
	}
	return v.modbusAdvancedKey(e)
}
