package tui

import (
	"bytes"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var modbusDeviceKinds = []string{"本机模拟 mock", "串口 RTU", "Modbus TCP", "RTU over TCP"}
var modbusDeviceParams = []string{"address", "count", "samples", "interval_ms", "unit", "word_order", "baud", "data_bits", "parity", "stop_bits", "connect_timeout_ms", "request_timeout_ms", "request_gap_ms"}

func modbusDeviceText(s string, max int) bool {
	return len(s) > 0 && len(s) <= max && strings.IndexFunc(s, unicode.IsControl) < 0 && !strings.Contains(s, "${")
}
func modbusDeviceCandidate(base config.Request, f *tview.Form) (config.Request, error) {
	r := modbusReadRequest(base)
	kind, _ := f.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
	text := func(i int) string { return strings.TrimSpace(f.GetFormItem(i).(*tview.InputField).GetText()) }
	host := text(1)
	port, e := strconv.Atoi(text(2))
	if kind >= 2 {
		if !modbusDeviceText(host, 253) || strings.ContainsAny(host, "/@?#\\") || port < 1 || port > 65535 || e != nil {
			return r, fmt.Errorf("主机/端口无效；主机不含URL、凭据、路径或模板")
		}
		if strings.Contains(host, ":") {
			a, e := netip.ParseAddr(host)
			if e != nil || a.Zone() != "" {
				return r, fmt.Errorf("IPv6需无方括号的明确地址")
			}
		} else {
			for _, c := range host {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
					return r, fmt.Errorf("主机需ASCII名称或IP")
				}
			}
		}
		scheme := "tcp://"
		if kind == 3 {
			scheme = "rtu+tcp://"
		}
		r.Endpoint = scheme + net.JoinHostPort(host, strconv.Itoa(port))
	} else if kind == 1 {
		path := text(3)
		if !modbusDeviceText(path, 4096) || strings.Contains(path, "://") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`) {
			return r, fmt.Errorf("串口需本机设备路径或COM名称，不接受网络共享/URL")
		}
		r.Endpoint = "rtu://" + path
	} else {
		r.Endpoint = "mock://local"
	}
	for _, field := range []struct {
		i      int
		key    string
		lo, hi int
	}{{4, "unit", 1, 247}, {5, "baud", 1, 4000000}, {6, "data_bits", 5, 8}, {8, "stop_bits", 1, 2}, {10, "connect_timeout_ms", 1, 60000}, {11, "request_timeout_ms", 1, 60000}, {12, "request_gap_ms", 0, 60000}} {
		n, e := strconv.Atoi(text(field.i))
		if e != nil || n < field.lo || n > field.hi {
			return r, fmt.Errorf("%s需%d..%d", field.key, field.lo, field.hi)
		}
		r.Params[field.key] = n
	}
	if kind == 1 && r.Int("request_timeout_ms", 1) > 5000 {
		return r, fmt.Errorf("串口请求超时最多5000ms，保证取消等待有界")
	}
	_, r.Params["parity"] = f.GetFormItem(7).(*tview.DropDown).GetCurrentOption()
	_, r.Params["word_order"] = f.GetFormItem(9).(*tview.DropDown).GetCurrentOption()
	r.Timeout = text(13)
	if _, err := r.Duration(); err != nil {
		return r, err
	}
	if kind != 1 {
		for _, k := range []string{"baud", "data_bits", "parity", "stop_bits"} {
			delete(r.Params, k)
		}
	}
	if r.Endpoint != base.Endpoint || r.Int("unit", 1) != base.Int("unit", 1) {
		for _, k := range []string{"pins", "labels", "rules"} {
			delete(r.Params, k)
		}
	}
	if _, err := engine.NewModbusInterpreter(r); err != nil {
		return r, err
	}
	return r, nil
}
func (v *inspector) modbusDeviceForm() {
	if v.modbus == nil {
		return
	}
	u := v.owner
	r, err := u.collection.Resolve(modbusReadRequest(v.modbus.request), u.profile)
	if err != nil {
		u.modal("设备设置需先解析当前环境：" + err.Error())
		return
	}
	kind, host, port, path := 0, "127.0.0.1", "502", ""
	if strings.HasPrefix(r.Endpoint, "rtu://") {
		kind = 1
		path = strings.TrimPrefix(r.Endpoint, "rtu://")
	} else if strings.HasPrefix(r.Endpoint, "tcp://") || strings.HasPrefix(r.Endpoint, "rtu+tcp://") {
		kind = 2
		prefix := "tcp://"
		if strings.HasPrefix(r.Endpoint, "rtu+tcp://") {
			kind = 3
			prefix = "rtu+tcp://"
		}
		host, port, err = net.SplitHostPort(strings.TrimPrefix(r.Endpoint, prefix))
		if err != nil {
			host = ""
			port = "502"
		}
	}
	f := tview.NewForm().SetItemPadding(0).AddDropDown("接口", modbusDeviceKinds, kind, nil).AddInputField("主机/IP", host, 40, nil, nil).AddInputField("TCP端口", port, 8, nil, nil).AddInputField("本机串口路径", path, 45, nil, nil)
	for _, field := range []struct {
		label, key string
		def        int
	}{{"Unit", "unit", 1}, {"波特率", "baud", 9600}, {"数据位(5..8)", "data_bits", 8}} {
		f.AddInputField(field.label, strconv.Itoa(r.Int(field.key, field.def)), 12, nil, nil)
	}
	parity := 0
	for i, p := range []string{"N", "E", "O"} {
		if r.String("parity", "N") == p {
			parity = i
		}
	}
	f.AddDropDown("校验", []string{"N", "E", "O"}, parity, nil).AddInputField("停止位(1..2)", strconv.Itoa(r.Int("stop_bits", 1)), 8, nil, nil)
	order := 0
	for i, s := range []string{"ABCD", "BADC", "CDAB", "DCBA"} {
		if r.String("word_order", "ABCD") == s {
			order = i
		}
	}
	f.AddDropDown("字序", []string{"ABCD", "BADC", "CDAB", "DCBA"}, order, nil)
	fallback := 5000
	if d, e := r.Duration(); e == nil && d < 5*time.Second {
		fallback = int(d / time.Millisecond)
	}
	for _, field := range []struct {
		label, key string
		def        int
	}{{"连接超时ms", "connect_timeout_ms", fallback}, {"请求超时ms", "request_timeout_ms", fallback}, {"请求后间隔ms", "request_gap_ms", 0}} {
		f.AddInputField(field.label, strconv.Itoa(r.Int(field.key, field.def)), 12, nil, nil)
	}
	total := r.Timeout
	if total == "" {
		total = "15s"
	}
	f.AddInputField("总时限(如15s)", total, 12, nil, nil)
	close := v.modbusDialog("modbus-device", f)
	f.AddButton("列串口", func() { v.modbusSerialPicker(f, engine.ListModbusSerialPorts) }).AddButton("网络发现", func() {
		k, _ := f.GetFormItem(0).(*tview.DropDown).GetCurrentOption()
		if k < 2 {
			f.SetTitle("请选择TCP或RTU-over-TCP接口后进入发现")
			return
		}
		v.modbusNetworkForm(f)
	}).AddButton("预览", func() {
		candidate, e := modbusDeviceCandidate(r, f)
		if e != nil {
			f.SetTitle(display(e.Error()))
			return
		}
		v.modbusDevicePreview(candidate, f)
	}).AddButton("取消", close)
	f.SetBorder(true).SetTitle(" 设备设置 · 打开不连接 · Esc取消 F8停止 ")
}
func (v *inspector) modbusSaveDevice(r config.Request) error {
	u := v.owner
	if u.modbusRotationBusy() {
		return fmt.Errorf("请先停止请求和发现")
	}
	saved, err := v.modbusSavedRequest()
	if err != nil {
		return err
	}
	resolved, err := u.collection.Resolve(saved, u.profile)
	if err != nil {
		return err
	}
	next := modbusReadRequest(saved)
	next.Action = r.Action
	// Keep an unchanged source template instead of persisting its expansion.
	if r.Endpoint != resolved.Endpoint {
		next.Endpoint = r.Endpoint
	}
	next.Timeout = r.Timeout
	for _, k := range modbusDeviceParams {
		if value, ok := r.Params[k]; ok {
			next.Params[k] = value
		} else {
			delete(next.Params, k)
		}
	}
	if r.Endpoint != resolved.Endpoint || r.Int("unit", 1) != resolved.Int("unit", 1) || r.Action != modbusReadAction(resolved.Action) {
		for _, k := range []string{"pins", "labels", "rules"} {
			delete(next.Params, k)
		}
	}
	raw, err := config.ReplaceRequest(u.raw, next.ID, next)
	if err != nil {
		return err
	}
	c, err := config.Parse(raw)
	if err != nil {
		return err
	}
	old, _, err := modbusReadConfigIdentity(u.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(old, u.raw) {
		return fmt.Errorf("配置已被外部修改，未保存")
	}
	if err = config.Save(u.path, raw); err != nil {
		return err
	}
	c.SourcePath = u.path
	u.raw = raw
	u.collection = c
	u.preview()
	return nil
}
func (v *inspector) modbusDevicePreview(r config.Request, previous *tview.Form) {
	u := v.owner
	view := tview.NewTextView().SetDynamicColors(false).SetWrap(true)
	view.SetText(fmt.Sprintf("接口目标：%s\nUnit：%d\n读取空间：%s；地址%d；数量%d\n连接超时%dms；请求超时%dms；请求间隔%dms；总时限%s\n串口：baud=%d data=%d parity=%s stop=%d\n字序：%s\n\n临时应用/保存均不连接。明确读取才发起当前窗口的只读请求。更换设备或Unit将清空该请求标注与缓存。串口打开时才配置端口；未选择串口时忽略串口字段。保存保持未改动的端点模板。", r.Endpoint, r.Int("unit", 1), r.Action, r.Int("address", 0), r.Int("count", 1), r.Int("connect_timeout_ms", 5000), r.Int("request_timeout_ms", 5000), r.Int("request_gap_ms", 0), r.Timeout, r.Int("baud", 9600), r.Int("data_bits", 8), r.String("parity", "N"), r.Int("stop_bits", 1), r.String("word_order", "ABCD")))
	close := func() { u.pages.RemovePage("modbus-device-preview"); u.App.SetFocus(previous) }
	apply := func(save, run bool) {
		if u.modbusRotationBusy() {
			view.SetTitle("请先停止请求和发现")
			return
		}
		if save {
			if err := v.modbusSaveDevice(r); err != nil {
				view.SetTitle(display(err.Error()))
				return
			}
		}
		u.lastRequest = copyRequest(r)
		v.reset(r)
		u.pages.RemovePage("modbus-device-preview")
		u.pages.RemovePage("modbus-device")
		u.App.SetFocus(v.table)
		if run {
			u.start(r)
		} else {
			u.setStatus("设备设置已应用；尚未连接，按r明确读取")
		}
	}
	buttons := tview.NewForm().AddButton("临时应用", func() { apply(false, false) }).AddButton("保存设置", func() { apply(true, false) }).AddButton("明确读取", func() { apply(false, true) }).AddButton("返回", close)
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, false).AddItem(buttons, 3, 0, true)
	panel.SetBorder(true).SetTitle(" 核对设备目标 · 只读操作 ")
	panel.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			close()
			return nil
		}
		if e.Key() == tcell.KeyF8 {
			u.stop()
			return nil
		}
		return e
	})
	u.pages.AddPage("modbus-device-preview", panel, true, true)
	u.App.SetFocus(buttons)
}
