package tui

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type modbusRotationCandidate struct {
	path, sourcePath string
	raw, sourceRaw   []byte
	info, sourceInfo os.FileInfo
	collection       *config.Collection
}

func modbusLocalConfigPath(path string) error {
	if len(path) == 0 || len(path) > 4096 || strings.TrimSpace(path) == "" || strings.Contains(path, "://") || strings.Contains(path, "${") || strings.Contains(path, "{{") || strings.Contains(path, "}}") || strings.IndexFunc(path, unicode.IsControl) >= 0 || strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`) {
		return fmt.Errorf("需要本机配置路径，拒绝URL、网络共享、模板或控制字符")
	}
	if i := strings.IndexByte(path, ':'); i >= 0 && !(i == 1 && len(path) > 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && (path[2] == '/' || path[2] == '\\')) {
		return fmt.Errorf("路径不能是URI")
	}
	return nil
}
func modbusReadConfigIdentity(path string) ([]byte, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("配置必须为普通文件，拒绝符号链接")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, nil, fmt.Errorf("读取时文件身份已改变")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > 4<<20 {
		return nil, nil, fmt.Errorf("配置超过4MiB")
	}
	return raw, after, nil
}
func (u *UI) modbusRotationBusy() bool {
	return u.running || len(u.localCancels) > 0 || u.activeUASubscriptions() > 0
}
func (u *UI) modbusRotationTarget() string {
	if v := u.inspector; v != nil && v.modbus != nil {
		if r, err := v.modbusSavedRequest(); err == nil {
			if next, ok := r.Params["next_config"].(string); ok && next != "" {
				return next
			}
		}
	}
	origin := u.ensureModbusSession().origin
	current, _ := filepath.Abs(u.path)
	first, _ := filepath.Abs(origin)
	if current != first {
		return origin
	}
	return ""
}
func (u *UI) modbusPrepareRotation(target string) (modbusRotationCandidate, error) {
	var c modbusRotationCandidate
	u.ensureModbusSession()
	if u.modbusRotationBusy() {
		return c, fmt.Errorf("请先停止所有请求、后台订阅和本机工作")
	}
	if err := modbusLocalConfigPath(target); err != nil {
		return c, err
	}
	sourcePath, err := filepath.Abs(u.path)
	if err != nil {
		return c, err
	}
	path := target
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(sourcePath), path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return c, err
	}
	if path == sourcePath {
		return c, fmt.Errorf("目标已经是当前集合")
	}
	sourceRaw, sourceInfo, err := modbusReadConfigIdentity(sourcePath)
	if err != nil {
		return c, err
	}
	if !bytes.Equal(sourceRaw, u.raw) {
		return c, fmt.Errorf("当前集合被外部修改，请先处理文件冲突")
	}
	raw, info, err := modbusReadConfigIdentity(path)
	if err != nil {
		return c, err
	}
	parsed, err := config.Parse(raw)
	if err != nil {
		return c, fmt.Errorf("轮换只接受原生集合YAML；MTUI JSON请先用完整配置转换：%w", err)
	}
	parsed.SourcePath = path
	return modbusRotationCandidate{path: path, sourcePath: sourcePath, raw: raw, sourceRaw: append([]byte{}, sourceRaw...), info: info, sourceInfo: sourceInfo, collection: parsed}, nil
}
func modbusEndpointSummary(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "（端点留待执行时校验）"
	}
	if parsed.Scheme == "tcp" || parsed.Scheme == "rtu+tcp" {
		return parsed.Scheme + "://" + parsed.Host
	}
	if parsed.Scheme == "mock" {
		return "mock://local"
	}
	if parsed.Scheme == "rtu" {
		return "串口（执行前查看请求详情）"
	}
	if parsed.Scheme != "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	return "（含模板，切换不会展开或执行）"
}
func (u *UI) modbusRotationForm() {
	if u.modbusRotationBusy() {
		u.modal("请先停止所有请求、后台订阅和本机工作，再轮换集合")
		return
	}
	u.ensureModbusSession()
	form := tview.NewForm().AddInputField("下一份原生集合YAML", u.modbusRotationTarget(), 75, nil, nil)
	close := func() { u.pages.RemovePage("modbus-rotation-file"); u.App.SetFocus(u.inspector.table) }
	form.AddButton("校验并预览切换", func() {
		candidate, err := u.modbusPrepareRotation(form.GetFormItem(0).(*tview.InputField).GetText())
		if err != nil {
			form.SetTitle("无法切换：" + display(err.Error()))
			return
		}
		close()
		u.modbusRotationPreview(candidate)
	}).AddButton("保存为当前请求next_config", func() {
		path := form.GetFormItem(0).(*tview.InputField).GetText()
		if err := modbusLocalConfigPath(path); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		if err := u.inspector.modbusSaveParams(map[string]any{"next_config": path}); err != nil {
			form.SetTitle(display(err.Error()))
			return
		}
		close()
		u.setStatus("已保存本机next_config路径；尚未读取下一份文件或连接设备")
	}).AddButton("取消", close).SetCancelFunc(close)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF8 {
			u.stop()
			return nil
		}
		return e
	})
	form.SetBorder(true).SetTitle(" 本机集合轮换 · 无next_config时可返回初始集合 · Esc取消 ")
	u.pages.AddPage("modbus-rotation-file", form, true, true)
	u.App.SetFocus(form)
}
func (v *inspector) modbusUnsavedLayout() (map[string]any, error) {
	if v.modbus == nil {
		return nil, nil
	}
	saved, err := v.modbusSavedRequest()
	if err != nil {
		return nil, err
	}
	cols, widths, hex, err := modbusParseColumns(saved.Params["columns"])
	if err != nil {
		return nil, err
	}
	mode := "read_at"
	if c, ok := saved.Params["columns"].(map[string]any); ok && c["time_mode"] == "ago" {
		mode = "ago"
	}
	changes := map[string]any{}
	if !reflect.DeepEqual(cols, v.modbus.columns) || !reflect.DeepEqual(widths, v.modbus.widths) || hex != v.modbus.hexAddress || mode != v.modbus.timeMode {
		columns := []any{}
		for _, key := range v.modbus.columns {
			columns = append(columns, key)
		}
		if len(columns) == 0 {
			for _, c := range modbusColumns[:13] {
				columns = append(columns, c.key)
			}
		}
		rawWidths := map[string]any{}
		for key, width := range v.modbus.widths {
			rawWidths[key] = width
		}
		address := "decimal"
		if v.modbus.hexAddress {
			address = "hex"
		}
		changes["columns"] = map[string]any{"visible": columns, "widths": rawWidths, "address_mode": address, "time_mode": v.modbus.timeMode}
	}
	if saved.Int("matrix_columns", 8) != v.matrixColumns {
		changes["matrix_columns"] = v.matrixColumns
	}
	// Read controls are temporary until explicitly saved. Include them in the
	// existing atomic source save; the action marker is consumed by the local
	// save helper and never serialized as an engine parameter.
	resolved, err := v.owner.collection.Resolve(saved, v.owner.profile)
	if err != nil {
		return nil, err
	}
	active := v.modbus.request
	if modbusReadAction(active.Action) == active.Action {
		if active.Action != resolved.Action {
			changes["__read_action"] = active.Action
		}
		for _, p := range []struct {
			key      string
			fallback int
		}{{"address", 0}, {"unit", 1}, {"count", 1}, {"samples", 1}, {"interval_ms", 1000}} {
			if active.Int(p.key, p.fallback) != resolved.Int(p.key, p.fallback) {
				changes[p.key] = active.Int(p.key, p.fallback)
			}
		}
		if active.String("word_order", "ABCD") != resolved.String("word_order", "ABCD") {
			changes["word_order"] = active.String("word_order", "ABCD")
		}
	}
	return changes, nil
}
func (u *UI) modbusRotationPreview(c modbusRotationCandidate) {
	changes, err := u.inspector.modbusUnsavedLayout()
	if err != nil {
		u.modal(err.Error())
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "当前：%s\n目标：%s\n\n只切换本机集合，不连接任何服务器，也不修改只读状态。\n当前结果会清空；新请求仍需明确执行，写入仍需确认。\n", c.sourcePath, c.path)
	if len(changes) > 0 {
		b.WriteString("\n存在尚未保存的临时读取/字序/列布局/矩阵设置。请选择保存后切换或明确放弃。\n")
	}
	for i, r := range c.collection.Requests {
		if i >= 1000 {
			b.WriteString("…列表预览限1000个请求\n")
			break
		}
		write := "只读"
		if r.Mutates() {
			write = "写操作：执行前确认"
		}
		fmt.Fprintf(&b, "\n%s · %s/%s · %s · %s", modbusLogText(r.ID), r.Protocol, modbusLogText(r.Action), modbusLogText(modbusEndpointSummary(r.Endpoint)), write)
	}
	view := tview.NewTextView().SetText(clean(b.String())).SetScrollable(true).SetWrap(true)
	view.SetBorder(true).SetTitle(" 集合切换预览 · Tab到按钮 · Esc取消 ")
	profiles := []string{""}
	for profile := range c.collection.Profiles {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles[1:])
	selected := 0
	for i, profile := range profiles {
		if profile == c.collection.DefaultProfile {
			selected = i
		}
	}
	controls := tview.NewForm().AddDropDown("目标环境(空=不展开)", profiles, selected, nil)
	close := func() { u.pages.RemovePage("modbus-rotation-preview"); u.App.SetFocus(u.inspector.table) }
	apply := func(save bool) {
		if save && len(changes) > 0 {
			if err := u.inspector.modbusSaveParams(changes); err != nil {
				view.SetTitle("保存失败：" + display(err.Error()))
				return
			}

			// Saving atomically replaces only the source. Keep the original
			// target snapshot so a changed destination requires fresh review.
			raw, info, err := modbusReadConfigIdentity(c.sourcePath)
			if err != nil || !bytes.Equal(raw, u.raw) {
				view.SetTitle("源文件保存后校验失败，请重新预览")
				return
			}
			c.sourceRaw, c.sourceInfo = raw, info
		}
		_, profile := controls.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
		if err := u.modbusApplyRotation(c, profile); err != nil {
			view.SetTitle("切换失败：" + display(err.Error()))
			return
		}
		close()
		u.App.SetFocus(u.list)
	}
	buttons := tview.NewForm()
	if len(changes) > 0 {
		buttons.AddButton("保存设置后切换", func() { apply(true) }).AddButton("放弃临时设置并切换", func() { apply(false) })
	} else {
		buttons.AddButton("明确切换", func() { apply(false) })
	}
	buttons.AddButton("取消", close)
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(controls, 3, 0, false).AddItem(buttons, 3, 0, false)
	panel.SetInputCapture(u.inspector.modbusPanelKeys(close, []tview.Primitive{view, controls}, buttons))
	u.pages.AddPage("modbus-rotation-preview", panel, true, true)
	u.App.SetFocus(view)
}
func (u *UI) modbusApplyRotation(c modbusRotationCandidate, profile string) error {
	if u.modbusRotationBusy() {
		return fmt.Errorf("当前仍有运行中的工作")
	}
	currentPath, _ := filepath.Abs(u.path)
	if currentPath != c.sourcePath || !bytes.Equal(u.raw, c.sourceRaw) {
		return fmt.Errorf("预览后当前集合已改变，请重新预览")
	}
	for _, item := range []struct {
		path string
		raw  []byte
		info os.FileInfo
	}{{c.sourcePath, c.sourceRaw, c.sourceInfo}, {c.path, c.raw, c.info}} {
		raw, info, err := modbusReadConfigIdentity(item.path)
		if err != nil {
			return err
		}
		if !os.SameFile(info, item.info) || !bytes.Equal(raw, item.raw) {
			return fmt.Errorf("预览后集合文件内容或身份已改变，请重新预览")
		}
	}
	if profile != "" {
		if _, ok := c.collection.Profiles[profile]; !ok {
			return fmt.Errorf("目标环境不存在")
		}
	}
	u.collection = c.collection
	u.path = c.path
	u.raw = append([]byte{}, c.raw...)
	u.profile = profile
	u.lastRequest = config.Request{}
	u.navigation = nil
	u.events = nil
	u.lastHTTPBody = nil
	u.mqttPreviewPending = nil
	u.methodArgumentsPending = nil
	u.uaCopyValue = ""
	u.uaCopyPending = false
	u.result.Clear()
	u.search.SetText("")
	u.populate("")
	if len(u.collection.Requests) > 0 {
		u.inspector.reset(u.collection.Requests[u.selected])
	} else {
		u.inspector.reset(config.Request{})
	}
	s := u.ensureModbusSession()
	s.scope = ""
	s.stats = modbusSessionStats{time: time.Now().UTC()}
	u.modbusActivity("INFO", "用户明确切换本机集合；尚未连接服务器")
	u.setStatus("集合已切换；只读权限保持不变，选择请求后按F5才执行")
	return nil
}
