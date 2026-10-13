package tui

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gopcua/opcua/ua"
)

// A target is a concrete request, never just an endpoint: equal endpoints can
// carry different identities, trust pins and address-space visibility.
type uaOneClickTarget struct {
	Request config.Request
	Label   string
	Error   string
}
type uaOneClickRunner func(context.Context, config.Request, bool, engine.Emit) error
type uaOneClickWriter func(context.Context, string, string, uaScanSnapshot) error

type uaOneClickFileResult struct {
	Path     string `json:"path"`
	Error    string `json:"error,omitempty"`
	Complete bool   `json:"complete"`
}
type uaOneClickServiceResult struct {
	RequestID            string               `json:"request_id"`
	Endpoint             string               `json:"endpoint"`
	RootNode             string               `json:"root_node"`
	StartedAt            time.Time            `json:"started_at"`
	FinishedAt           time.Time            `json:"finished_at"`
	Outcome              string               `json:"outcome"`
	Reason               string               `json:"reason,omitempty"`
	Rows                 int                  `json:"rows"`
	Read                 int                  `json:"read"`
	Unread               int                  `json:"unread"`
	FailedValues         int                  `json:"failed_values"`
	ScanComplete         bool                 `json:"scan_complete"`
	NamespaceFingerprint string               `json:"namespace_fingerprint"`
	CSV                  uaOneClickFileResult `json:"csv"`
	JSON                 uaOneClickFileResult `json:"json"`
}
type uaOneClickReport struct {
	Directory  string                    `json:"directory"`
	StartedAt  time.Time                 `json:"started_at"`
	FinishedAt time.Time                 `json:"finished_at"`
	NonAtomic  bool                      `json:"non_atomic"`
	Cancelled  bool                      `json:"cancelled"`
	Success    int                       `json:"success"`
	Partial    int                       `json:"partial"`
	Failed     int                       `json:"failed"`
	Services   []uaOneClickServiceResult `json:"services"`
	Summary    uaOneClickFileResult      `json:"-"`
}

func (r uaOneClickReport) summary() string {
	return fmt.Sprintf("成功%d / partial%d / fail%d · %s（逐次读取，非原子快照）", r.Success, r.Partial, r.Failed, r.Directory)
}

// Validate provided limits without Request.Int's fallback/truncation behavior.
// Only absent parameters receive defaults. Authentication/trust are untouched.
func uaOneClickTargetFor(r config.Request) (uaOneClickTarget, error) {
	r = copyRequest(r)
	target := uaOneClickTarget{Request: r, Label: r.ID}
	if r.Name != "" {
		target.Label += " · " + r.Name
	}
	fail := func(err error) (uaOneClickTarget, error) { target.Error = err.Error(); return target, err }
	if r.Protocol != "opcua" || r.Action != "scan" {
		return fail(errors.New("仅支持明确配置的 OPC UA action=scan 请求"))
	}
	address, addressErr := url.Parse(r.Endpoint)
	if addressErr != nil || address.Scheme != "opc.tcp" || address.Hostname() == "" || address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" {
		return fail(errors.New("端点必须是无嵌入凭据、查询参数或片段的 opc.tcp URL"))
	}
	root, err := ua.ParseNodeID(r.String("node_id", "i=85"))
	if err != nil {
		return fail(fmt.Errorf("根 NodeId 无效: %w", err))
	}
	r.Params["node_id"] = root.String()
	for _, limit := range []struct {
		key           string
		def, min, max int
	}{
		{"max_depth", 0, 0, 256}, {"max_nodes", 10000, 1, 100000},
		{"max_references", 1000, 1, 100000}, {"read_batch_size", 100, 1, 1000},
	} {
		value := limit.def
		if raw, present := r.Params[limit.key]; present {
			var e error
			value, e = strconv.Atoi(fmt.Sprint(raw))
			if e != nil || value < limit.min || value > limit.max {
				return fail(fmt.Errorf("%s 必须是 %d..%d 的整数", limit.key, limit.min, limit.max))
			}
		}
		r.Params[limit.key] = value
	}
	if r.Timeout == "" {
		r.Timeout = "2m"
	}
	if _, err := r.Duration(); err != nil {
		return fail(err)
	}
	target.Request = r
	return target, nil
}

func uaOneClickAction(base config.Request, action string) config.Request {
	r := copyRequest(base)
	r.Action = action
	for _, key := range []string{"node_id", "node_ids", "browse_path", "attributes", "attribute", "value", "value_type", "arguments", "method_id", "object_id", "max_events", "max_depth", "max_nodes", "max_references", "read_batch_size", "expected_namespace_fingerprint"} {
		delete(r.Params, key)
	}
	return r
}

// Request IDs are labels, not paths. A numeric prefix also separates duplicate
// labels and requests sharing an endpoint, without exposing endpoint/credentials.
func uaOneClickFileStem(index int, id string) string {
	var name strings.Builder
	for _, r := range id {
		if name.Len() >= 48 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			name.WriteRune(r)
		} else {
			name.WriteByte('_')
		}
	}
	if name.Len() == 0 {
		name.WriteString("server")
	}
	return fmt.Sprintf("%03d-%s", index+1, name.String())
}

// Network work is sequential and cancellable. Cancellation stops future targets,
// but a separate, bounded local-file context preserves observations already made.
func runUAOneClick(ctx context.Context, directory string, targets []uaOneClickTarget, run uaOneClickRunner, write uaOneClickWriter, progress func(string)) (report uaOneClickReport, err error) {
	report = uaOneClickReport{Directory: directory, StartedAt: time.Now().UTC(), NonAtomic: true}
	if ctx == nil || run == nil || len(targets) == 0 || len(targets) > 1000 {
		return report, errors.New("需要上下文、执行器和1..1000个明确请求")
	}
	if write == nil {
		write = writeUAScanSnapshot
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if strings.TrimSpace(directory) == "" {
		return report, errors.New("必须指定新的输出目录")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return report, err
	}
	report.Directory = directory
	// Mkdir, not MkdirAll: an existing directory/file/symlink is always refused.
	if err = os.Mkdir(directory, 0700); err != nil {
		return report, fmt.Errorf("创建新目录失败，尚未连接；不覆盖已有路径: %w", err)
	}
	for index, target := range targets {
		result := uaOneClickServiceResult{RequestID: target.Request.ID, Endpoint: uaScanFileEndpoint(target.Request.Endpoint), RootNode: target.Request.String("node_id", "i=85"), Outcome: "fail"}
		switch {
		case ctx.Err() != nil:
			result.Outcome, result.Reason = "partial", "已取消，未开始此请求；未创建服务快照"
		case target.Error != "":
			result.Reason = target.Error
		default:
			validated, validationErr := uaOneClickTargetFor(target.Request)
			if validationErr != nil {
				result.Reason = validationErr.Error()
			} else {
				if progress != nil {
					progress(fmt.Sprintf("%d/%d %s · 扫描/全选/批量读值", index+1, len(targets), target.Label))
				}
				result = runUAOneClickService(ctx, directory, index, validated, run, write)
			}
		}
		switch result.Outcome {
		case "success":
			report.Success++
		case "partial":
			report.Partial++
		default:
			report.Failed++
		}
		report.Services = append(report.Services, result)
		if progress != nil {
			progress(fmt.Sprintf("%d/%d %s · %s · %d变量", index+1, len(targets), target.Label, result.Outcome, result.Rows))
		}
	}
	report.Cancelled = ctx.Err() != nil
	report.FinishedAt = time.Now().UTC()
	// Summary is new and exclusive as well; a failed summary is never reported
	// as a complete export. File errors already appear in per-service entries.
	data, encodeErr := json.MarshalIndent(report, "", "  ")
	if encodeErr != nil {
		return report, encodeErr
	}
	report.Summary.Path = filepath.Join(directory, "summary.json")
	file, createErr := uaScanCreateFile(report.Summary.Path)
	if createErr != nil {
		report.Summary.Error = createErr.Error()
		return report, errors.Join(createErr, ctx.Err())
	}
	err = uaOneClickWriteSummary(&report, data, file)
	if report.Cancelled {
		err = errors.Join(err, ctx.Err())
	}
	return report, err
}

// The sink boundary permits deterministic summary write/sync/close failure
// tests. Like snapshot persistence, summary cleanup is independent of network
// cancellation and only claims success after the file has been synced/closed.
func uaOneClickWriteSummary(report *uaOneClickReport, data []byte, sink uaScanFileSink) error {
	fileCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	writer := newUAScanFileWriter(fileCtx, sink, report.Summary.Path, uaScanFileLimit)
	err := errors.Join(writer.write(append(data, '\n')), writer.close())
	if err != nil {
		report.Summary.Error = err.Error()
	} else {
		report.Summary.Complete = true
	}
	return err
}

func runUAOneClickService(ctx context.Context, directory string, index int, target uaOneClickTarget, run uaOneClickRunner, write uaOneClickWriter) uaOneClickServiceResult {
	r := target.Request
	result := uaOneClickServiceResult{RequestID: r.ID, Endpoint: uaScanFileEndpoint(r.Endpoint), RootNode: r.String("node_id", "i=85"), StartedAt: time.Now().UTC(), Outcome: "fail"}
	duration, _ := r.Duration() // Target validation happened before any network I/O.
	serviceCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	snapshot := uaScanSnapshot{Endpoint: r.Endpoint, RootNode: result.RootNode, ScanReason: "未收到扫描完成状态", Rows: []uaScanRow{}}
	indexes, seen := map[string]int{}, map[string]bool{}
	// Keep a small separate reservation for terminal diagnostics even when an
	// observation exhausts the main allowance. Neither quota is reused.
	memory := uaOneClickMemory{remaining: uaOneClickMemoryLimit - 1<<20}
	noteMemory := uaOneClickMemory{remaining: 1 << 20}
	var memoryErr error
	stopForMemory := func(err error) {
		if memoryErr == nil {
			memoryErr = err
			cancel()
		}
	}
	ownedEvent := func(event engine.Event) (engine.Event, bool) {
		if memoryErr != nil {
			return engine.Event{}, false
		}
		owned, err := memory.ownEvent(event)
		if err != nil {
			stopForMemory(err)
			return engine.Event{}, false
		}
		return owned, true
	}
	var notes []string
	addNote := func(s string) {
		if s != "" && len(notes) < 20 {
			if err := noteMemory.bytes(len(s)); err != nil {
				stopForMemory(err)
				return
			}
			notes = append(notes, strings.Clone(s))
		}
	}
	fingerprintInvalid, scanEventError := false, false
	scanErr := run(serviceCtx, r, false, func(event engine.Event) {
		switch event.Kind {
		case "scan-namespace", "scan-node", "scan-error", "scan-summary":
		default:
			return
		}
		event, accepted := ownedEvent(event)
		if !accepted {
			return
		}
		m, ok := event.Data.(map[string]any)
		if !ok {
			return
		}
		switch event.Kind {
		case "scan-namespace":
			fingerprint := uaScanString(m, "fingerprint")
			decoded, err := hex.DecodeString(fingerprint)
			if err != nil || len(decoded) != 32 || (snapshot.NamespaceFingerprint != "" && snapshot.NamespaceFingerprint != fingerprint) {
				fingerprintInvalid = true
			} else {
				snapshot.NamespaceFingerprint = fingerprint
			}
		case "scan-node":
			id := uaScanString(m, "node_id")
			if id == "" || seen[id] {
				return
			}
			if len(seen) >= r.Int("max_nodes", 10000) {
				scanEventError = true
				addNote("达到节点上限")
				cancel()
				return
			}
			seen[id] = true
			if uaScanString(m, "node_class") != "Variable" {
				return
			}
			row := uaScanRow{NodeID: id, ParentID: uaScanString(m, "parent_id"), DisplayName: uaScanString(m, "display_name"), BrowseName: uaScanString(m, "browse_name"), NodeClass: "Variable", Depth: uaScanInt(m["depth"]), Status: "未读取"}
			if row.DisplayName == "" {
				row.DisplayName = row.BrowseName
			}
			if row.DisplayName == "" {
				row.DisplayName = row.NodeID
			}
			indexes[id] = len(snapshot.Rows)
			snapshot.Rows = append(snapshot.Rows, row)
		case "scan-error":
			scanEventError = true
			addNote(uaScanString(m, "error"))
		case "scan-summary":
			snapshot.ScanComplete, _ = m["complete"].(bool)
			snapshot.ScanReason = uaScanString(m, "reason")
		}
	})
	if scanErr != nil {
		snapshot.ScanComplete = false
		addNote("扫描: " + scanErr.Error())
	}
	if scanEventError || memoryErr != nil {
		snapshot.ScanComplete = false
	}
	if memoryErr != nil {
		snapshot.ScanReason += "; 内存预算/值处理: " + memoryErr.Error()
	}
	if !snapshot.ScanComplete {
		addNote(snapshot.ScanReason)
	}
	if fingerprintInvalid || snapshot.NamespaceFingerprint == "" {
		snapshot.NamespaceFingerprint = ""
		addNote("缺少有效且一致的 NamespaceArray 指纹，禁止读取")
	}
	var readErr error
	if snapshot.NamespaceFingerprint != "" && serviceCtx.Err() == nil {
		// The engine accepts at most 10,000 node IDs per read action. Larger
		// discovered sets are partitioned without dropping any Variable.
		for offset := 0; offset < len(snapshot.Rows); offset += 10000 {
			end := min(offset+10000, len(snapshot.Rows))
			ids := make([]string, end-offset)
			allowed := make(map[string]bool, len(ids))
			for i := offset; i < end; i++ {
				ids[i-offset] = snapshot.Rows[i].NodeID
				allowed[ids[i-offset]] = true
			}
			read := uaOneClickAction(r, "read")
			read.Params["expected_namespace_fingerprint"] = snapshot.NamespaceFingerprint
			read.Params["node_ids"] = ids
			read.Params["read_batch_size"] = r.Int("read_batch_size", 100)
			chunkErr := run(serviceCtx, read, false, func(event engine.Event) {
				if event.Kind != "value" || memoryErr != nil {
					return
				}
				m, ok := event.Data.(map[string]any)
				if !ok {
					return
				}
				id, textID := m["node_id"].(string)
				if !textID {
					stopForMemory(errors.New("unsupported non-string OPC UA value NodeID"))
					return
				}
				if !allowed[id] {
					return
				}
				event, accepted := ownedEvent(event)
				if !accepted {
					if prior := &snapshot.Rows[indexes[id]]; prior.Kind != "" {
						prior.Error = strings.TrimPrefix(prior.Error+"; 后续观测未接收: "+memoryErr.Error(), "; ")
					}
					return
				}
				i := indexes[id]
				snapshot.Rows[i] = uaScanValueRow(snapshot.Rows[i], event)
			})
			if chunkErr != nil {
				readErr = errors.Join(readErr, chunkErr)
				addNote("读取: " + chunkErr.Error())
				if !engine.OPCUAReadCanContinue(chunkErr) {
					break
				}
			}
			if serviceCtx.Err() != nil {
				break
			}
		}
	}
	if serviceCtx.Err() != nil {
		addNote("已停止/超时: " + serviceCtx.Err().Error())
	}
	memoryNoted := memoryErr != nil
	if memoryNoted {
		// This terminal reason must remain visible even after 20 prior errors.
		notes = append(notes, "内存预算/值处理: "+memoryErr.Error())
	}
	unreadReason := strings.Join(notes, "; ")
	for i := range snapshot.Rows {
		row := &snapshot.Rows[i]
		if row.Kind == "" {
			result.Unread++
			row.Status = "未读取（取消/超时/失败/未返回）"
			row.Error = unreadReason // one shared bounded diagnostic, not N copies
		} else {
			result.Read++
			if !row.HasValue || row.Error != "" || (row.Status != "Good" && row.Status != ua.StatusOK.Error()) {
				result.FailedValues++
			}
		}
	}
	result.Rows, result.ScanComplete, result.NamespaceFingerprint = len(snapshot.Rows), snapshot.ScanComplete, snapshot.NamespaceFingerprint
	if result.Unread > 0 || result.FailedValues > 0 {
		addNote(fmt.Sprintf("未读%d；失败/非Good值%d", result.Unread, result.FailedValues))
	}
	if !snapshot.ScanComplete && snapshot.ScanReason == "" {
		snapshot.ScanReason = strings.Join(notes, "; ")
	}
	snapshot.ExportedAt = time.Now().UTC()
	stem := uaOneClickFileStem(index, r.ID)
	result.CSV.Path, result.JSON.Path = filepath.Join(directory, stem+".csv"), filepath.Join(directory, stem+".json")
	// Each format gets a fresh bounded allowance. A slow or timed-out CSV
	// must not consume JSON's budget; both still persist after network cancel.
	writeSnapshot := func(path, format string) error {
		fileCtx, fileCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer fileCancel()
		return write(fileCtx, path, format, snapshot)
	}
	if err := writeSnapshot(result.CSV.Path, "csv"); err != nil {
		result.CSV.Error = err.Error()
		addNote("CSV: " + err.Error())
	}
	if err := writeSnapshot(result.JSON.Path, "json"); err != nil {
		result.JSON.Error = err.Error()
		addNote("JSON: " + err.Error())
	}
	switch {
	case result.CSV.Error != "" && result.JSON.Error != "":
		result.Outcome = "fail"
	case !snapshot.ScanComplete && len(snapshot.Rows) == 0:
		result.Outcome = "fail"
	case !snapshot.ScanComplete || snapshot.NamespaceFingerprint == "" || readErr != nil || memoryErr != nil || result.Unread > 0 || result.FailedValues > 0 || serviceCtx.Err() != nil || result.CSV.Error != "" || result.JSON.Error != "":
		result.Outcome = "partial"
	default:
		result.Outcome = "success"
	}
	result.CSV.Complete = result.CSV.Error == ""
	result.JSON.Complete = result.JSON.Error == ""
	if memoryErr != nil && !memoryNoted {
		notes = append(notes, "内存预算/值处理: "+memoryErr.Error())
	}
	result.Reason, result.FinishedAt = strings.Join(notes, "; "), time.Now().UTC()
	return result
}
