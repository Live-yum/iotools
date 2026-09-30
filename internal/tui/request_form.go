package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"go.yaml.in/yaml/v3"
)

func (u *UI) editRequest() {
	if u.running {
		u.modal("请先取消当前请求，再编辑参数。 ")
		return
	}
	if len(u.indexes) == 0 {
		return
	}
	r := u.collection.Requests[u.selected]
	params, _ := yaml.Marshal(r.Params)
	form := tview.NewForm()
	form.AddInputField("请求 ID", r.ID, 42, nil, nil).AddInputField("显示名称", r.Name, 42, nil, nil).AddInputField("协议", r.Protocol, 42, nil, nil).AddInputField("操作", r.Action, 42, nil, nil).AddInputField("服务地址", r.Endpoint, 70, nil, nil).AddInputField("超时时间", r.Timeout, 20, nil, nil).AddTextArea("参数 YAML", string(params), 70, 10, 4<<20, nil)
	close := func() { u.pages.RemovePage("request-form"); u.App.SetFocus(u.list) }
	form.AddButton("保存", func() {
		get := func(i int) string { return form.GetFormItem(i).(*tview.InputField).GetText() }
		updated := config.Request{ID: get(0), Name: get(1), Protocol: get(2), Action: get(3), Endpoint: get(4), Timeout: get(5)}
		text := form.GetFormItem(6).(*tview.TextArea).GetText()
		if text != "" {
			if e := yaml.Unmarshal([]byte(text), &updated.Params); e != nil {
				form.SetTitle(" 参数 YAML 无效：" + clean(e.Error()) + " ")
				return
			}
		}
		b, e := config.ReplaceRequest(u.raw, r.ID, updated)
		if e != nil {
			form.SetTitle(" 校验失败：" + clean(e.Error()) + " ")
			return
		}
		current, e := os.ReadFile(u.path)
		if e != nil || !bytes.Equal(current, u.raw) {
			form.SetTitle(" 文件已被外部修改，请关闭后重新载入，避免覆盖 ")
			return
		}
		if e = config.Save(u.path, b); e != nil {
			form.SetTitle(" 保存失败：" + clean(e.Error()) + " ")
			return
		}
		u.collection, _ = config.Parse(b)
		u.raw = b
		close()
		u.populate(u.search.GetText())
		u.setStatus("已保存请求 " + updated.ID)
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(fmt.Sprintf(" 编辑请求 %s · 保存不会连接服务器 · Esc 取消 ", clean(r.ID)))
	u.pages.AddPage("request-form", form, true, true)
	u.App.SetFocus(form)
}

func redactPreview(r config.Request) config.Request {
	var walk func(any, bool) any
	walk = func(value any, crypto bool) any {
		switch v := value.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, x := range v {
				secret := key == "password" || key == "bearer" || key == "token" || key == "schema_registry_password" || key == "schema_registry_bearer" || (crypto && (key == "key" || key == "iv"))
				if secret {
					out[key] = "••••（按 F3/F4 主动编辑原始配置）"
				} else {
					out[key] = walk(x, crypto || key == "crypto")
				}
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, x := range v {
				out[i] = walk(x, crypto)
			}
			return out
		default:
			return value
		}
	}
	if r.Params != nil {
		r.Params = walk(r.Params, false).(map[string]any)
	}
	return r
}

func (v *inspector) persistAnnotations() error {
	u := v.owner
	id := u.lastRequest.ID
	if id == "" {
		return fmt.Errorf("没有正在查看的请求")
	}
	var r config.Request
	found := false
	for _, request := range u.collection.Requests {
		if request.ID == id {
			r = request
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("找不到源请求")
	}
	params := map[string]any{}
	for k, value := range r.Params {
		params[k] = value
	}
	r.Params = params
	pins := []any{}
	for address, pinned := range v.pins {
		if pinned {
			pins = append(pins, address)
		}
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].(int) < pins[j].(int) })
	labels := map[string]any{}
	for address, label := range v.labels {
		labels[strconv.Itoa(address)] = label
	}
	r.Params["pins"] = pins
	r.Params["labels"] = labels
	current, e := os.ReadFile(u.path)
	if e != nil {
		return e
	}
	if !bytes.Equal(current, u.raw) {
		return fmt.Errorf("配置文件已被外部修改，已停止覆盖")
	}
	b, e := config.ReplaceRequest(u.raw, id, r)
	if e != nil {
		return e
	}
	if e = config.Save(u.path, b); e != nil {
		return e
	}
	u.collection, e = config.Parse(b)
	if e != nil {
		return e
	}
	u.raw = b
	u.setStatus("固定项和标签已保存到请求配置")
	return nil
}

func (u *UI) showHelp() {
	view := tview.NewTextView().SetText(help).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 中文帮助 · 方向键/PgDn/滚轮滚动 · Esc/q 关闭 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape || e.Rune() == 'q' {
			u.pages.RemovePage("help")
			u.App.SetFocus(u.list)
			return nil
		}
		return e
	})
	u.pages.AddPage("help", view, true, true)
	u.App.SetFocus(view)
}

func (v *inspector) snapshot(load bool) {
	action := "保存快照"
	if load {
		action = "载入对比快照"
	}
	form := tview.NewForm().AddInputField("私有文件路径", "register-snapshot.json", 70, nil, nil)
	close := func() { v.owner.pages.RemovePage("snapshot-file"); v.owner.App.SetFocus(v.table) }
	form.AddButton(action, func() {
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		r := v.owner.lastRequest
		rows := make([]map[string]any, 0, len(v.values))
		for _, row := range v.values {
			rows = append(rows, row)
		}
		now, e := engine.NewRegisterSnapshot(r.Endpoint, r.Int("unit", 1), r.Action, rows)
		if e != nil {
			form.SetTitle(" 快照失败：" + clean(e.Error()))
			return
		}
		if load {
			before, err := engine.LoadRegisterSnapshot(path)
			if err != nil {
				form.SetTitle(" 载入失败：" + clean(err.Error()))
				return
			}
			changes, err := engine.DiffRegisterSnapshots(before, now)
			if err != nil {
				form.SetTitle(" 无法比较：" + clean(err.Error()))
				return
			}
			v.baseline = before.Values
			v.renderRegisters()
			close()
			text, _ := json.MarshalIndent(changes, "", "  ")
			view := tview.NewTextView().SetText(clean(string(text))).SetScrollable(true)
			view.SetBorder(true).SetTitle(" 快照差异（含新增/删除）· Esc 返回 ")
			view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEscape {
					v.owner.pages.RemovePage("snapshot-diff")
					v.owner.App.SetFocus(v.table)
					return nil
				}
				return e
			})
			v.owner.pages.AddPage("snapshot-diff", view, true, true)
			v.owner.App.SetFocus(view)
		} else {
			if e = engine.SaveRegisterSnapshot(path, now); e != nil {
				form.SetTitle(" 保存失败（不会覆盖已有文件）：" + clean(e.Error()))
				return
			}
			close()
			v.owner.setStatus("快照已保存到 " + path)
		}
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(" " + action + " · 包含目标地址，请保存在私有目录 · Esc 取消 ")
	v.owner.pages.AddPage("snapshot-file", form, true, true)
	v.owner.App.SetFocus(form)
}
