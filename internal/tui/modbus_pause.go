package tui

func (u *UI) toggleModbusPause() {
	if !u.running || u.modbusPause == nil {
		u.setStatus("当前没有可暂停的Modbus读取；修改操作不暂停，F8可取消")
		return
	}
	paused := !u.modbusPause.Paused()
	u.modbusPause.SetPaused(paused)
	if paused {
		u.setStatus("Modbus 已暂停 · z继续 · F8取消 · 总时限仍计时")
	} else {
		u.setStatus("Modbus 已继续原采样 · z暂停 · F8取消")
	}
}
