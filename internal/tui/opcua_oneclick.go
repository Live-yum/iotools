package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The main-screen shortcut opens only local configuration and confirmation UI.
// It never connects or starts a scan merely on opening.
func (u *UI) showUAOneClick() {
	if u.running || u.quitting {
		u.setStatus("请先停止当前请求")
		return
	}
	if u.uaWorkspace != nil && u.uaWorkspace.scan != nil && u.uaWorkspace.scan.active {
		u.pages.SwitchToPage("ua-scan")
		u.uaWorkspace.scan.setStatus("一键导出进行中；S/F8停止，i查看状态")
		return
	}
	var base config.Request
	collection := false
	for _, r := range u.collection.Requests {
		if r.Protocol == "opcua" && r.Action == "scan" {
			base, collection = r, true
			break
		}
	}
	if !collection && u.selected >= 0 && u.selected < len(u.collection.Requests) {
		base = u.collection.Requests[u.selected]
	}
	if base.Protocol != "opcua" {
		u.modal("请选择OPC UA请求，或在集合中配置 action=scan 请求")
		return
	}
	if resolved, err := u.collection.Resolve(base, u.profile); err == nil {
		base = resolved
	}
	u.showUAWorkspaceFor(base)
	if u.uaWorkspace == nil {
		return
	}
	u.uaWorkspace.showScan()
	u.uaWorkspace.scan.oneClickForm(collection)
}

func (w *uaScanWorkspace) oneClickTargets(collection bool) []uaOneClickTarget {
	u := w.owner()
	var requests []config.Request
	if collection {
		for _, r := range u.collection.Requests {
			if r.Protocol == "opcua" && r.Action == "scan" {
				requests = append(requests, r)
			}
		}
	} else {
		r := w.base
		// Re-resolve the current concrete request in the current profile. Never
		// use an endpoint-based lookup, which could silently switch identities.
		for _, candidate := range u.collection.Requests {
			if candidate.ID == r.ID {
				r = candidate
				break
			}
		}
		r = copyRequest(r)
		r.Action = "scan"
		requests = append(requests, r)
	}
	targets := make([]uaOneClickTarget, 0, len(requests))
	for _, raw := range requests {
		resolved, resolveErr := u.collection.Resolve(raw, u.profile)
		var target uaOneClickTarget
		if resolveErr != nil {
			target = uaOneClickTarget{Request: raw, Label: raw.ID, Error: "配置未解析，未连接: " + resolveErr.Error()}
		} else {
			target, _ = uaOneClickTargetFor(resolved)
			if !collection && target.Error == "" {
				// Current-server mode follows the visible scan settings; collection
				// mode keeps each request's own root, limits and timeout exactly.
				target.Request.Params["node_id"] = w.rootNode
				target.Request.Params["max_depth"] = w.maxDepth
				target.Request.Params["max_nodes"] = w.maxNodes
				target.Request.Params["max_references"] = w.maxReferences
				target.Request.Params["read_batch_size"] = w.readBatch
				target, _ = uaOneClickTargetFor(target.Request)
			}
		}
		targets = append(targets, target)
	}
	return targets
}

func (w *uaScanWorkspace) oneClickForm(collection bool) {
	if w.active || w.closed || w.owner().quitting || w.oneClickPending {
		if w.active {
			w.setStatus("请先S/F8停止当前操作；重复操作未启动新连接")
		}
		return
	}
	w.oneClickPending = true
	// Resolve only on preview so the target list does not store resolved secrets
	// longer than needed. The immutable preview is exactly what gets executed.
	form := tview.NewForm()
	path := "opcua-snapshots-" + time.Now().UTC().Format("20060102-150405.000000000")
	form.AddInputField("新目录（父目录须已存在）", path, 0, nil, nil)
	var chosen []*tview.Checkbox
	if collection {
		for _, r := range w.owner().collection.Requests {
			if r.Protocol == "opcua" && r.Action == "scan" {
				form.AddCheckbox(clean(r.ID+" · "+r.Name), true, nil)
				chosen = append(chosen, form.GetFormItem(form.GetFormItemCount()-1).(*tview.Checkbox))
			}
		}
	} else {
		form.AddTextView("当前服务", clean(w.base.ID+" · "+uaScanFileEndpoint(w.base.Endpoint)), 0, 2, false, false)
	}
	close := func() {
		w.oneClickPending = false
		w.owner().pages.RemovePage("ua-oneclick-form")
		w.focusPane(w.focus)
	}
	form.AddButton("预览并确认", func() {
		if w.active || w.closed || w.owner().quitting {
			return
		}
		directory := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if directory == "" {
			form.SetTitle("必须填写新的输出目录")
			return
		}
		absolute, err := filepath.Abs(directory)
		if err != nil {
			form.SetTitle(clean(err.Error()))
			return
		}
		targets := w.oneClickTargets(collection)
		if collection {
			selected := make([]uaOneClickTarget, 0, len(targets))
			for i, target := range targets {
				if i < len(chosen) && chosen[i].IsChecked() {
					selected = append(selected, target)
				}
			}
			targets = selected
		}
		if len(targets) == 0 || len(targets) > 1000 {
			form.SetTitle("请勾选1..1000个明确扫描请求")
			return
		}
		w.owner().pages.RemovePage("ua-oneclick-form")
		w.oneClickConfirm(absolute, targets, w.owner().profile)
	}).AddButton("切换当前/集合", func() { close(); w.oneClickForm(!collection) }).AddButton("取消", close).SetCancelFunc(close)
	mode := "当前服务"
	if collection {
		mode = "勾选集合中的 scan 请求"
	}
	form.SetBorder(true).SetTitle(" 一键导出 · " + mode + " · CSV+JSON · Esc取消 ")
	w.owner().pages.AddPage("ua-oneclick-form", form, true, true)
	w.owner().App.SetFocus(form)
}

func uaOneClickPreview(directory, profile string, targets []uaOneClickTarget) string {
	var b strings.Builder
	fmt.Fprintf(&b, "将顺序连接以下 %d 个明确请求（profile=%s）。\n每项：扫描→全选已发现Variable→批量读值→独立CSV+JSON。\n输出新目录：%s\n已有目录/文件不覆盖；不修改服务数据。不自动发现其他服务。\n扫描或坏节点失败仍保存可得数据并标partial/fail，继续下一项。\nS/F8取消后保留已完成文件，并保存当前可得数据及汇总。\n这是分批、跨会话、跨服务的逐次读取，绝非同一时刻原子快照。\n每个请求保留原有认证、证书和信任设置，不降级。\n\n", len(targets), profile, directory)
	for i, target := range targets {
		r := target.Request
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, target.Label, uaScanFileEndpoint(r.Endpoint))
		if target.Error != "" {
			fmt.Fprintf(&b, "   跳过并记fail：%s\n\n", target.Error)
			continue
		}
		fmt.Fprintf(&b, "   根 %s · 深度%d（0不限）· 节点%d · 每节点引用%d\n   每批%d值 · 每服务总超时%s · %s / %s · auth=%s\n   文件 %s.csv + .json\n\n", r.String("node_id", "i=85"), r.Int("max_depth", 0), r.Int("max_nodes", 10000), r.Int("max_references", 1000), r.Int("read_batch_size", 100), r.Timeout, r.String("security_policy", "Basic256Sha256"), r.String("security_mode", "SignAndEncrypt"), r.String("auth", "anonymous"), uaOneClickFileStem(i, r.ID))
	}
	return clean(b.String())
}

func (w *uaScanWorkspace) oneClickConfirm(directory string, targets []uaOneClickTarget, profile string) {
	view := tview.NewTextView().SetText(uaOneClickPreview(directory, profile, targets)).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 确认连接范围 · ↑↓滚动 · Tab到按钮 · Esc取消 ")
	buttons := tview.NewForm()
	close := func() {
		w.oneClickPending = false
		w.owner().pages.RemovePage("ua-oneclick-confirm")
		w.focusPane(w.focus)
	}
	buttons.AddButton("开始只读扫描并导出", func() { close(); w.startOneClick(directory, targets) }).AddButton("取消", close).SetCancelFunc(close)
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false)
	root.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			close()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			if view.HasFocus() {
				w.owner().App.SetFocus(buttons)
			} else {
				w.owner().App.SetFocus(view)
			}
			return nil
		}
		return e
	})
	w.owner().pages.AddPage("ua-oneclick-confirm", root, true, true)
	w.owner().App.SetFocus(view)
}

func (w *uaScanWorkspace) startOneClick(directory string, targets []uaOneClickTarget) {
	if w.active || w.closed || w.owner().quitting {
		return
	}
	w.active, w.stopping, w.mode = true, false, "一键导出"
	w.oneClickReport = nil
	w.generation++
	generation := w.generation
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	u := w.owner()
	if u.localCancels == nil {
		u.localCancels = map[uint64]context.CancelFunc{}
	}
	u.nextLocalWork++
	id := u.nextLocalWork
	u.localCancels[id] = cancel
	run := w.run
	w.setStatus("一键导出进行中 · S/F8停止；观察结果写入独立文件")
	go func() {
		report, err := runUAOneClick(ctx, directory, targets, run, nil, func(progress string) {
			if ctx.Err() != nil {
				return
			}
			u.App.QueueUpdateDraw(func() {
				if !w.closed && w.generation == generation && !w.stopping {
					w.setStatus(progress + " · S/F8停止")
				}
			})
		})
		cancel()
		u.App.QueueUpdateDraw(func() {
			delete(u.localCancels, id)
			if !w.closed && w.generation == generation {
				w.active, w.stopping, w.cancel = false, false, nil
				w.oneClickReport = &report
				w.status = uaOneClickStatus(report, err)
				w.renderHeader()
				if name, _ := u.pages.GetFrontPage(); name == "ua-scan" && !u.quitting {
					w.oneClickResult()
				}
			}
			if u.quitting && !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
				u.App.Stop()
			}
		})
	}()
}

func uaOneClickStatus(report uaOneClickReport, err error) string {
	status := report.summary()
	if report.Cancelled || errors.Is(err, context.Canceled) {
		status = "已停止，已有结果保留 · " + status
	}
	if err != nil && !uaScanOnlyCancelled(err) {
		status = "导出错误：" + err.Error() + " · " + status
	}
	return status
}

func (w *uaScanWorkspace) oneClickResult() {
	if w.oneClickReport == nil || w.mode != "一键导出" {
		w.statusDetail()
		return
	}
	r := w.oneClickReport
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n目录：%s\n汇总文件状态见下方\n非原子快照；ScanComplete仅表示配置范围内扫描完整，不保证每个值读取成功。\n\n", w.status, r.Directory)
	switch {
	case r.Summary.Complete:
		fmt.Fprintf(&b, "汇总已写入：%s\n\n", r.Summary.Path)
	case r.Summary.Error != "":
		fmt.Fprintf(&b, "汇总未完整写入：%s · %s\n\n", r.Summary.Path, r.Summary.Error)
	default:
		fmt.Fprintln(&b, "未创建汇总文件；请查看上述错误。")
	}
	for i, service := range r.Services {
		fmt.Fprintf(&b, "%d. %s · %s\n%s\n扫描完整=%t · Variable%d · 返回%d · 未读%d · 失败/非Good值%d\n%s\nCSV: %s %s\nJSON: %s %s\n\n", i+1, service.RequestID, service.Outcome, service.Endpoint, service.ScanComplete, service.Rows, service.Read, service.Unread, service.FailedValues, service.Reason, service.CSV.Path, service.CSV.Error, service.JSON.Path, service.JSON.Error)
	}
	view := tview.NewTextView().SetText(clean(b.String())).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 一键导出结果 · ↑↓滚动 · Esc返回 · i重新查看 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			w.owner().pages.RemovePage("ua-oneclick-result")
			w.focusPane(w.focus)
			return nil
		}
		return e
	})
	w.owner().pages.AddPage("ua-oneclick-result", view, true, true)
	w.owner().App.SetFocus(view)
}
