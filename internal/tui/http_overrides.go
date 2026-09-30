package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func parseHTTPOverrides(text string) (engine.HTTPOverrides, error) {
	var out engine.HTTPOverrides
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return out, fmt.Errorf("临时覆盖必须是JSON对象")
	}
	if len(text) > 65536 {
		return out, fmt.Errorf("临时覆盖最多64KiB")
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return out, fmt.Errorf("只允许一个JSON对象")
	}
	return out, nil
}
func (u *UI) httpOverrideForm() {
	if u.running || u.selected < 0 || u.selected >= len(u.collection.Requests) {
		return
	}
	original := u.collection.Requests[u.selected]
	if original.Protocol != "http" {
		u.modal("单次覆盖仅用于HTTP请求")
		return
	}
	form := tview.NewForm().SetItemPadding(0)
	form.SetBorder(true).SetTitle(" 单次HTTP覆盖 · 不改集合 · Esc取消 ")
	form.AddTextView("格式", "JSON键: Fields/Headers/Query/Form 为 name=value 字符串数组；Body/URL/Basic/Bearer 为字符串。无等号删除header/query/form。不要把凭据写入共享文件。", 0, 3, false, false)
	form.AddTextArea("临时覆盖JSON", "{}", 0, 10, 65536, nil)
	close := func() { u.pages.RemovePage("http-overrides"); u.App.SetFocus(u.list) }
	form.AddButton("检查并预览", func() {
		options, err := parseHTTPOverrides(form.GetFormItem(1).(*tview.TextArea).GetText())
		if err != nil {
			form.SetTitle(" " + display(err.Error()) + " ")
			return
		}
		collection, request, profile, err := engine.ApplyHTTPOverrides(u.collection, original, u.profile, options)
		if err != nil {
			form.SetTitle(" " + display(err.Error()) + " ")
			return
		}
		collection.Requests = append([]config.Request(nil), u.collection.Requests...)
		collection.Requests[u.selected] = request
		close()
		u.httpOverridePreview(collection, request, profile)
	}).AddButton("取消", close).SetCancelFunc(close)
	u.pages.AddPage("http-overrides", form, true, true)
	u.App.SetFocus(form)
}
func (u *UI) httpOverridePreview(collection *config.Collection, request config.Request, profile string) {
	preview := redactPreview(request)
	profileValues := map[string]any{}
	for key, value := range collection.Profiles[profile] {
		profileValues[key] = value
	}
	profilePreview := redactPreview(config.Request{Params: profileValues})
	data, _ := json.MarshalIndent(map[string]any{"request": preview, "profile": profile, "profile_values": profilePreview.Params}, "", "  ")
	view := tview.NewTextView().SetText(string(data)).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 单次请求预览 · 模板发送前解析 · 凭据已遮盖 ")
	buttons := tview.NewForm()
	close := func() { u.pages.RemovePage("http-override-preview"); u.App.SetFocus(u.list) }
	buttons.AddButton("执行一次", func() {
		if u.running {
			return
		}
		if u.readonly && request.Mutates() {
			u.modal("只读模式禁止修改操作")
			return
		}
		close()
		u.navigation = nil
		u.startCollection(request, collection, profile)
	}).AddButton("取消", close)
	flex := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	flex.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyTab {
			if view.HasFocus() {
				u.App.SetFocus(buttons)
			} else {
				u.App.SetFocus(view)
			}
			return nil
		}
		return e
	})
	u.pages.AddPage("http-override-preview", flex, true, true)
	u.App.SetFocus(view)
}
