package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (w *uaScanWorkspace) request(action string) config.Request {
	r := copyRequest(w.base)
	r.Action = action
	for _, key := range []string{"node_id", "node_ids", "browse_path", "attributes", "attribute", "value", "value_type", "arguments", "method_id", "object_id", "max_events", "max_depth", "max_nodes", "max_references", "read_batch_size", "expected_namespace_fingerprint"} {
		delete(r.Params, key)
	}
	return r
}

// Do not hide an I/O error joined with cancellation. Every leaf must be a cancellation.
func uaScanOnlyCancelled(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !uaScanOnlyCancelled(child) {
				return false
			}
		}
		return true
	}
	if cause := errors.Unwrap(err); cause != nil {
		return uaScanOnlyCancelled(cause)
	}
	return err == context.Canceled
}

// startJob keeps cancellation registered until the worker has flushed files and closed
// its engine session. A closing/replaced view cannot consume any later callback.
func (w *uaScanWorkspace) startJob(mode string, work func(context.Context, engine.Emit) (string, error)) {
	if w.active || w.closed || w.owner().quitting {
		return
	}
	w.active = true
	w.stopping = false
	w.mode = mode
	w.received = 0
	w.recorded = 0
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
	w.setStatus(mode + "进行中 · S/F8停止")
	go func() {
		note, err := work(ctx, func(event engine.Event) {
			if ctx.Err() != nil && event.Kind != "scan-summary" && event.Kind != "record-count" {
				return
			}
			u.App.QueueUpdateDraw(func() {
				if !w.closed && w.generation == generation && (!w.stopping || event.Kind == "record-count") {
					if event.Kind == "record-count" {
						w.recorded = uaScanInt(event.Data)
						w.renderHeader()
					} else {
						w.apply(event)
					}
				}
			})
		})
		contextErr := ctx.Err()
		cancel()
		u.App.QueueUpdateDraw(func() {
			delete(u.localCancels, id)
			if !w.closed && w.generation == generation {
				w.active = false
				if mode == "批量读值" {
					for _, node := range w.nodes {
						if node.row.Status == "待读取" {
							node.row.Status = "本次未返回（停止/失败）"
							w.renderVariable(node)
						}
					}
				}
				w.stopping = false
				w.cancel = nil
				switch {
				case err != nil && !uaScanOnlyCancelled(err):
					w.status = mode + "失败/部分完成：" + err.Error()
				case contextErr != nil || errors.Is(err, context.Canceled):
					w.status = mode + "已停止，已有结果保留"
				default:
					w.status = mode + "完成"
				}
				if note != "" {
					w.status += " · " + note
				}
				if mode == "扫描" && !w.complete && err != nil && (w.reason == "" || w.reason == "尚未完成扫描") {
					w.reason = err.Error()
				}
				w.renderHeader()
			}
			if u.quitting && !u.running && u.activeUASubscriptions() == 0 && len(u.localCancels) == 0 {
				u.App.Stop()
			}
		})
	}()
}
func (w *uaScanWorkspace) startScan() {
	if w.active || w.closed || w.owner().quitting {
		return
	}
	w.reset()
	r := w.request("scan")
	r.Params["node_id"] = w.rootNode
	r.Params["max_depth"] = w.maxDepth
	r.Params["max_nodes"] = w.maxNodes
	r.Params["max_references"] = w.maxReferences
	if r.Timeout == "" {
		r.Timeout = "2m"
	}
	w.startJob("扫描", func(ctx context.Context, emit engine.Emit) (string, error) {
		return "发现的变量可全选；未自动读值", w.run(ctx, r, false, emit)
	})
}
func (w *uaScanWorkspace) namespaceReady() bool {
	if w.namespaceFingerprint == "" {
		w.setStatus("缺少已验证NamespaceArray指纹；请先重新扫描，禁止批量读取/订阅")
		return false
	}
	return true
}
func (w *uaScanWorkspace) readSelected() {
	rows, ok := w.selectedRows(10000)
	if !ok || !w.namespaceReady() {
		return
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.NodeID
	}
	for _, row := range rows {
		n := w.nodes[row.NodeID]
		n.row.HasValue = false
		n.row.Value = nil
		n.row.ValueType = nil
		n.row.ValueTypeName = ""
		n.row.ValueIsArray = false
		n.row.SourceTimestamp = nil
		n.row.ServerTimestamp = nil
		n.row.ObservedAt = time.Time{}
		n.row.Kind = ""
		n.row.Status = "待读取"
		n.row.Error = ""
		w.renderVariable(n)
	}
	r := w.request("read")
	r.Params["expected_namespace_fingerprint"] = w.namespaceFingerprint
	r.Params["node_ids"] = ids
	r.Params["read_batch_size"] = w.readBatch
	w.startJob("批量读值", func(ctx context.Context, emit engine.Emit) (string, error) {
		return fmt.Sprintf("选中%d变量，共享会话、每批%d", len(ids), w.readBatch), w.run(ctx, r, false, emit)
	})
}
func (w *uaScanWorkspace) subscribeSelected(path, format string) {
	rows, ok := w.selectedRows(1000)
	if !ok || !w.namespaceReady() {
		return
	}
	ids := make([]string, len(rows))
	metadata := make(map[string]uaScanRow, len(rows))
	for i, row := range rows {
		ids[i] = row.NodeID
		metadata[row.NodeID] = row
	}
	r := w.request("subscribe")
	r.Params["expected_namespace_fingerprint"] = w.namespaceFingerprint
	r.Timeout = "24h"
	r.Params["auto_reconnect"] = false
	r.Params["node_ids"] = ids
	r.Params["max_events"] = uaScanRecordLimit
	if r.Int("interval_ms", 1000) < 50 || r.Int("interval_ms", 1000) > 60000 {
		r.Params["interval_ms"] = 1000
	}
	mode := "共享实时订阅（不写盘）"
	if path != "" {
		mode = "共享订阅记录"
	}
	w.startJob(mode, func(ctx context.Context, emit engine.Emit) (string, error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		var recorder *uaScanRecorder
		var err error
		if path != "" {
			recorder, err = w.newRecorder(path, format, r.Endpoint)
			if err != nil {
				return "未连接，未覆盖任何现有文件", err
			}
		}
		var recordErr error
		writeRecord := func(row uaScanRow) bool {
			if recorder == nil {
				return true
			}
			if recordErr != nil {
				return false
			}
			if recordErr = recorder.Write(row); recordErr != nil {
				cancel()
				return false
			}
			emit(engine.Event{Kind: "record-count", Data: recorder.Count()})
			return true
		}
		lifecycle := func(kind, status string) bool {
			return writeRecord(uaScanRow{Kind: kind, Status: status, ObservedAt: time.Now().UTC()})
		}
		if !lifecycle("session_start", "记录开始；NamespaceArray指纹="+r.String("expected_namespace_fingerprint", "")+"；生命周期行HasValue=false；缺少session_end表示文件可能不完整") {
			return path, errors.Join(recordErr, recorder.Close())
		}
		count := 0
		runErr := w.run(ctx, r, false, func(event engine.Event) {
			if ctx.Err() != nil || recordErr != nil {
				return
			}
			switch event.Kind {
			case "notification":
				m, ok := event.Data.(map[string]any)
				if !ok {
					recordErr = fmt.Errorf("订阅事件缺少节点数据")
					cancel()
					return
				}
				row, known := metadata[uaScanString(m, "node_id")]
				if !known {
					recordErr = fmt.Errorf("收到选中范围外的订阅节点")
					cancel()
					return
				}
				if count >= uaScanRecordLimit {
					recordErr = fmt.Errorf("已达到%d条通知上限，停止订阅", uaScanRecordLimit)
					cancel()
					return
				}
				if !writeRecord(uaScanValueRow(row, event)) {
					return
				}
				count++
			case "connected", "reconnecting", "error":
				if !lifecycle(event.Kind, fmt.Sprint(event.Data)) {
					return
				}
			}
			emit(event)
		})
		note := fmt.Sprintf("收到%d条通知；断线后请重新扫描再订阅", count)
		if recorder != nil {
			terminal := "Good: 订阅结束"
			if runErr != nil {
				terminal = "订阅停止/断流: " + runErr.Error()
			}
			if ctx.Err() != nil && runErr == nil {
				terminal = "订阅已取消: " + ctx.Err().Error()
			}
			lifecycle("session_end", terminal)
			recordErr = errors.Join(recordErr, recorder.Close())
			note = fmt.Sprintf("%s · 已写%d行（含生命周期）/%d字节；断线后请重新扫描再订阅", path, recorder.Count(), recorder.Bytes())
		}
		if count >= uaScanRecordLimit {
			note += " · 已达10万条通知上限，已停止"
		}
		if recordErr != nil {
			return note + " · 文件可能不完整", errors.Join(recordErr, runErr)
		}
		return note, runErr
	})
}
func (w *uaScanWorkspace) exportSelected(path, format string) {
	rows, ok := w.selectedRows(0)
	if !ok {
		return
	}
	snapshot := uaScanSnapshot{NamespaceFingerprint: w.namespaceFingerprint, Endpoint: w.base.Endpoint, RootNode: w.rootNode, ExportedAt: time.Now().UTC(), ScanComplete: w.complete, ScanReason: w.reason, Rows: rows}
	w.startJob("快照导出", func(ctx context.Context, _ engine.Emit) (string, error) {
		return fmt.Sprintf("%s · %d个选中变量（未读变量明确标记）", path, len(rows)), writeUAScanSnapshot(ctx, path, format, snapshot)
	})
}
func (w *uaScanWorkspace) fileForm(record bool) {
	limit := 0
	if record {
		limit = 1000
	}
	if _, ok := w.selectedRows(limit); !ok {
		return
	}
	formats := []string{"csv", "json"}
	title := " 导出选中变量快照 · 显式路径 · 新文件0600、不覆盖 "
	if record {
		formats = []string{"csv", "jsonl"}
		title = " 实时记录 · 单共享会话 · 10万行(含生命周期)/128MiB/24h上限后停止 "
	}
	form := tview.NewForm().AddInputField("新文件路径（必填）", "", 0, nil, nil).AddDropDown("格式", formats, 0, nil)
	close := func() { w.owner().pages.RemovePage("ua-scan-form"); w.focusPane(w.focus) }
	label := "导出快照"
	if record {
		label = "开始订阅并写文件"
	}
	form.AddButton(label, func() {
		path := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if path == "" {
			form.SetTitle("请指定新文件路径；不自动写盘")
			return
		}
		_, format := form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
		close()
		if record {
			w.subscribeSelected(path, format)
		} else {
			w.exportSelected(path, format)
		}
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetBorder(true).SetTitle(title)
	w.owner().pages.AddPage("ua-scan-form", form, true, true)
	w.owner().App.SetFocus(form)
}
func (w *uaScanWorkspace) detail(id string) {
	n := w.nodes[id]
	if n == nil {
		return
	}
	data, err := json.MarshalIndent(uaScanEncodedRow(n.row), "", "  ")
	if err != nil {
		data = []byte(fmt.Sprintf("值编码失败（未隐藏/截断）：%v\n节点：%s\n类型：%s\n状态：%s", err, n.row.NodeID, n.row.ValueTypeName, n.row.Status))
	}
	view := tview.NewTextView().SetText(clean(string(data))).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 变量/节点详情 · Esc返回扫描 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			w.owner().pages.RemovePage("ua-scan-detail")
			w.focusPane(w.focus)
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			w.stop()
			return nil
		}
		return e
	})
	w.owner().pages.AddPage("ua-scan-detail", view, true, true)
	w.owner().App.SetFocus(view)
}

func (w *uaScanWorkspace) statusDetail() {
	text := fmt.Sprintf("%s\n\n端点：%s\n根NodeId：%s\n扫描完整：%t\nNamespaceArray指纹：%s\n扫描状态：%s\n节点%d / 变量%d / 已选%d\n本次读取/通知%d / 文件记录%d行\n\n读取最多10000变量，每批%d，共享会话。\n订阅最多1000变量，单共享会话，禁用自动重连；断线后重新扫描再订阅。\n记录最多100000行（含session_start/connected/session_end等生命周期）、128MiB、24小时。\n达到上限或文件写入失败会停止，不会静默截断。缺少session_end的记录文件可能不完整。\nCSV/JSON快照只导出当前选择；CSV/JSONL记录只在明确指定新路径后创建0600文件，不覆盖。", w.status, w.base.Endpoint, w.rootNode, w.complete, w.namespaceFingerprint, w.reason, len(w.nodes), len(w.variables), len(w.selected), w.received, w.recorded, w.readBatch)
	view := tview.NewTextView().SetText(clean(text)).SetWrap(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" 扫描/记录完整状态 · Esc返回 · F8停止 ")
	view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			w.owner().pages.RemovePage("ua-scan-detail")
			w.focusPane(w.focus)
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			w.stop()
			return nil
		}
		return e
	})
	w.owner().pages.AddPage("ua-scan-detail", view, true, true)
	w.owner().App.SetFocus(view)
}
