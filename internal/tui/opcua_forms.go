package tui

import (
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/ua"
	"github.com/rivo/tview"
	"io"
	"reflect"
	"strings"
)

func (u *UI) confirmDerived(r config.Request) {
	if u.running {
		u.modal("请等待读取完成，或先按 F8 取消")
		return
	}
	if u.readonly {
		u.modal("只读模式禁止写入和方法调用")
		return
	}
	if r.Protocol == "mqtt" && r.Action == "clean-retained" {
		u.confirmMQTTCleanup(r)
		return
	}
	body, _ := json.MarshalIndent(redactPreview(r), "", "  ")
	view := tview.NewTextView().SetText(string(body)).SetScrollable(true).SetWrap(true)
	view.SetBorder(true).SetTitle(" 修改确认 · 请核对目标、unit/地址/节点和参数 · 上下滚动 · Tab 切换按钮 ")
	close := func() { u.pages.RemovePage("derived-confirm"); u.App.SetFocus(u.list) }
	cancel := tview.NewButton("取消").SetSelectedFunc(close)
	confirm := tview.NewButton("确认执行").SetSelectedFunc(func() {
		close()
		if !u.readonly && !u.running {
			u.start(r)
		}
	})
	buttons := tview.NewFlex().AddItem(cancel, 0, 1, false).AddItem(confirm, 0, 1, false)
	layout := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 1, 0, false)
	focus := 0
	widgets := []tview.Primitive{view, cancel, confirm}
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
	u.pages.AddPage("derived-confirm", layout, true, true)
	u.App.SetFocus(view)
}
func copyRequest(r config.Request) config.Request {
	p := map[string]any{}
	for k, v := range r.Params {
		p[k] = v
	}
	r.Params = p
	return r
}
func (u *UI) methodForm(data map[string]any) {
	inputs, ok := data["inputs"].([]engine.MethodArgument)
	if !ok {
		return
	}
	r := copyRequest(u.lastRequest)
	r.Action = "call"
	r.Params["method_id"] = fmt.Sprint(data["method_id"])
	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" 方法调用 · 按服务器声明输入 JSON 标量/数组 · Esc 取消 ")
	form.AddInputField("所属对象 NodeId", r.String("object_id", ""), 48, nil, nil)
	for _, a := range inputs {
		form.AddInputField(a.Name+" ("+a.Type+")", "", 48, nil, nil)
	}
	close := func() { u.pages.RemovePage("ua-method"); u.App.SetFocus(u.inspector.tree) }
	form.AddButton("检查并确认", func() {
		args := []any{}
		for i, a := range inputs {
			text := form.GetFormItem(i + 1).(*tview.InputField).GetText()
			var value any
			d := json.NewDecoder(strings.NewReader(text))
			d.UseNumber()
			if err := decodeSingle(d, &value); err != nil {
				form.SetTitle("参数必须是 JSON（字符串需要双引号）：" + display(a.Name))
				return
			}
			// Integer JSON numbers remain decimal strings, avoiding float64 precision loss.
			value = methodJSONNumbers(value)
			args = append(args, map[string]any{"type": a.Type, "value": value})
		}
		r.Params["object_id"] = form.GetFormItem(0).(*tview.InputField).GetText()
		r.Params["arguments"] = args
		close()
		u.confirmDerived(r)
	})
	form.AddButton("取消", close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		return e
	})
	u.pages.AddPage("ua-method", form, true, true)
	u.App.SetFocus(form)
}
func methodJSONNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		return string(x)
	case []any:
		for i := range x {
			x[i] = methodJSONNumbers(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = methodJSONNumbers(x[k])
		}
	}
	return v
}
func (u *UI) attributeForm(data map[string]any) {
	if u.running {
		u.setStatus("请先等待属性读取结束")
		return
	}
	r := copyRequest(u.lastRequest)
	r.Action = "write"
	delete(r.Params, "node_ids")
	delete(r.Params, "attributes")
	r.Params["node_id"] = data["node_id"]
	r.Params["attribute"] = data["attribute"]
	form := tview.NewForm()
	form.SetBorder(true).SetTitle(" 属性写入 · JSON 值 · Esc 取消 ")
	form.AddInputField("属性", fmt.Sprint(data["attribute"]), 40, nil, nil)
	form.AddInputField("类型", strings.TrimPrefix(fmt.Sprint(data["value_type"]), "TypeID"), 40, nil, nil)
	raw, _ := json.Marshal(editableUAValue(data["value"]))
	form.AddInputField("JSON 值", string(raw), 64, nil, nil)
	close := func() { u.pages.RemovePage("ua-attribute"); u.App.SetFocus(u.inspector.table) }
	form.AddButton("检查并确认", func() {
		var value any
		d := json.NewDecoder(strings.NewReader(form.GetFormItem(2).(*tview.InputField).GetText()))
		d.UseNumber()
		if err := decodeSingle(d, &value); err != nil {
			form.SetTitle("JSON 值无效：" + display(err.Error()))
			return
		}
		r.Params["attribute"] = form.GetFormItem(0).(*tview.InputField).GetText()
		r.Params["value_type"] = form.GetFormItem(1).(*tview.InputField).GetText()
		r.Params["value"] = methodJSONNumbers(value)
		close()
		u.confirmDerived(r)
	})
	form.AddButton("取消", close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		return e
	})
	u.pages.AddPage("ua-attribute", form, true, true)
	u.App.SetFocus(form)
}

func decodeSingle(d *json.Decoder, value *any) error {
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("只允许一个 JSON 值")
	}
	return nil
}
func editableUAValue(value any) any {
	switch v := value.(type) {
	case *ua.NodeID:
		if v != nil {
			return v.String()
		}
	case *ua.GUID:
		if v != nil {
			return v.String()
		}
	case *ua.LocalizedText:
		if v != nil {
			return map[string]any{"text": v.Text, "locale": v.Locale}
		}
	case *ua.QualifiedName:
		if v != nil {
			return map[string]any{"name": v.Name, "namespace": v.NamespaceIndex}
		}
	}
	r := reflect.ValueOf(value)
	if r.IsValid() && r.Kind() == reflect.Slice && r.Type().Elem().Kind() != reflect.Uint8 {
		out := []any{}
		for i := 0; i < r.Len(); i++ {
			out = append(out, editableUAValue(r.Index(i).Interface()))
		}
		return out
	}
	return value
}
