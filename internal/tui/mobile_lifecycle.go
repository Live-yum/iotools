package tui

// CancelMobileWork is called on the UI event loop. It preserves all widgets and
// unsaved editor text while cancelling active network/database workers.
func (u *UI) CancelMobileWork() {
	u.stop()
	u.stopUASubscriptions()
	for _, cancel := range u.localCancels {
		cancel()
	}
	u.setStatus("活动任务已取消 · 未保存编辑保留在内存 · 不自动重放请求")
}

// ApplyMobileOptions is called on the UI event loop after an explicit app choice.
func (u *UI) ApplyMobileOptions(readonly bool, historyPath string) {
	u.CancelMobileWork()
	u.readonly = readonly
	u.HTTPHistoryPath = historyPath
	u.setStatus("手机设置已生效 · 未保存编辑保留 · F1帮助")
}

// QuitMobile runs on the UI event loop and drains cancelled protocol workers.
func (u *UI) QuitMobile() { u.quit() }
