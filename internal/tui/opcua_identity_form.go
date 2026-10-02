package tui

import (
	"context"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/rivo/tview"
	"path/filepath"
)

func (u *UI) uaIdentityForm(connection *tview.Form) {
	form := tview.NewForm().SetItemPadding(0)
	form.SetBorder(true).SetTitle(" 明确创建新客户端身份 · 不覆盖文件 · 私钥保存在本机且不上传 ")
	form.AddInputField("新证书文件", filepath.Join(filepath.Dir(u.path), "opcua-client.pem"), 0, nil, nil).AddInputField("新私钥文件", filepath.Join(filepath.Dir(u.path), "opcua-client-key.pem"), 0, nil, nil).AddInputField("Application URI", "urn:iotools:client", 0, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	busy := false
	close := func() { cancel(); u.pages.RemovePage("ua-identity"); u.App.SetFocus(connection) }
	form.AddButton("确认生成新身份", func() {
		cert := form.GetFormItem(0).(*tview.InputField).GetText()
		key := form.GetFormItem(1).(*tview.InputField).GetText()
		uri := form.GetFormItem(2).(*tview.InputField).GetText()
		if busy {
			return
		}
		busy = true
		if u.localCancels == nil {
			u.localCancels = map[uint64]context.CancelFunc{}
		}
		u.nextLocalWork++
		workID := u.nextLocalWork
		u.localCancels[workID] = cancel
		form.SetTitle(" 正在生成客户端身份 · 可取消 · 不覆盖任何已有文件 ")
		go func() {
			err := engine.GenerateOPCUAClientIdentityContext(ctx, cert, key, uri)
			u.App.QueueUpdateDraw(func() {
				busy = false
				delete(u.localCancels, workID)
				if u.quitting && !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
					u.App.Stop()
					return
				}
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					form.SetTitle("生成失败：" + display(err.Error()))
					return
				}
				connection.GetFormItem(6).(*tview.InputField).SetText(cert)
				connection.GetFormItem(7).(*tview.InputField).SetText(key)
				close()
				u.setStatus("客户端身份已创建；服务端需管理员信任证书。未连接、未上传私钥。")
			})
		}()
	}).AddButton("取消", close).SetCancelFunc(close)
	u.pages.AddPage("ua-identity", form, true, true)
	u.App.SetFocus(form)
}
