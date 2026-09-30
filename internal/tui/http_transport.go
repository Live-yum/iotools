package tui

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/rivo/tview"
)

func (u *UI) authorizeInsecureTLS(ctx context.Context, r config.Request) (bool, error) {
	answers := make(chan bool, 1)
	u.App.QueueUpdateDraw(func() {

		close := func(allowed bool) {
			u.pages.RemovePage("http-insecure-confirm")
			u.App.SetFocus(u.list)
			select {
			case answers <- allowed:
			default:
			}
		}
		modal := u.httpInsecurePanel(r, close)
		u.pages.AddPage("http-insecure-confirm", modal, true, true)
		u.App.SetFocus(modal)
	})
	select {
	case allowed := <-answers:
		return allowed, nil
	case <-ctx.Done():
		u.App.QueueUpdateDraw(func() { u.pages.RemovePage("http-insecure-confirm") })
		return false, ctx.Err()
	}
}

func (u *UI) httpInsecurePanel(r config.Request, close func(bool)) *tview.Flex {
	return u.httpConfirmation(" 高风险TLS例外 ", "目标："+r.Endpoint+"\n\n本次请求不校验证书真实性/主机名，可能遭遇中间人攻击。\n建议取消并配置可信CA。只批准本次目标，不修改系统信任或其他主机。", "仅本次忽略证书", close)
}
