package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
)

// MTUIConfigPlan is an offline conversion, never an instruction to connect or
// enable an API, credentials, logging, background polling, or write permissions.
type MTUIConfigPlan struct {
	Requests []config.Request `json:"requests"`
	Warnings []string         `json:"warnings"`
}

type mtuiDeviceConfig struct {
	Interface      json.RawMessage `json:"interface"`
	Unit           int             `json:"unit_id"`
	ConnectTimeout int             `json:"connect_timeout_ms"`
	RequestTimeout int             `json:"request_timeout_ms"`
	Gap            int             `json:"request_gap_ms"`
	WordOrder      string          `json:"word_order"`
}

var mtuiPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func mtuiSafeString(s string, max int) bool {
	return len(s) <= max && !strings.Contains(s, "${") && strings.IndexFunc(s, unicode.IsControl) < 0
}

// ImportMTUIConfig converts an entire version-1 MTUI JSON configuration into
// four explicit single-read native requests. Unsupported settings are reported
// by field name, never silently turned into different device behavior.
func ImportMTUIConfig(data []byte, prefix string) (MTUIConfigPlan, error) {
	plan := MTUIConfigPlan{}
	if len(data) == 0 || len(data) > 4<<20 {
		return plan, fmt.Errorf("MTUI配置需1..4MiB")
	}
	if !mtuiPrefixPattern.MatchString(prefix) {
		return plan, fmt.Errorf("ID前缀需1..64位字母、数字、_或-")
	}
	var root map[string]json.RawMessage
	if err := strictJSON(data, &root); err != nil {
		return plan, err
	}
	if root == nil {
		return plan, fmt.Errorf("MTUI配置必须是JSON对象")
	}
	warn := func(s string) { plan.Warnings = append(plan.Warnings, s) }
	decode := func(k string, target any) error {
		if v, ok := root[k]; ok {
			if string(v) == "null" {
				return fmt.Errorf("%s不能为null", k)
			}
			return json.Unmarshal(v, target)
		}
		return nil
	}
	version := 1
	if err := decode("version", &version); err != nil || version != 1 {
		return plan, fmt.Errorf("仅支持MTUI配置version1")
	}
	name := "MTUI"
	if err := decode("name", &name); err != nil || !mtuiSafeString(name, 128) {
		return plan, fmt.Errorf("配置名称无效或过长")
	}
	d := mtuiDeviceConfig{Interface: json.RawMessage(`"mock"`), Unit: 1, ConnectTimeout: 1000, RequestTimeout: 2000, WordOrder: "abcd"}
	if raw, ok := root["device"]; ok {
		if err := mtuiConfigObject(raw, &d); err != nil {
			return plan, fmt.Errorf("device: %w", err)
		}
	}
	if d.Unit < 1 || d.Unit > 247 {
		return plan, fmt.Errorf("unit_id需1..247，禁止广播")
	}
	if d.ConnectTimeout < 1 || d.ConnectTimeout > 86400000 || d.RequestTimeout < 1 || d.RequestTimeout > 86400000 || d.Gap < 0 || d.Gap > 86400000 {
		return plan, fmt.Errorf("设备时间范围无效")
	}
	order := strings.ToUpper(d.WordOrder)
	if order != "ABCD" && order != "BADC" && order != "CDAB" && order != "DCBA" {
		return plan, fmt.Errorf("未知word_order")
	}
	endpoint, transport, err := mtuiInterface(d.Interface)
	if err != nil {
		return plan, err
	}
	for _, field := range []struct {
		key   string
		value *int
		max   int
	}{{"connect_timeout_ms", &d.ConnectTimeout, 60000}, {"request_timeout_ms", &d.RequestTimeout, 60000}, {"request_gap_ms", &d.Gap, 60000}} {
		limit := field.max
		if field.key == "request_timeout_ms" && strings.HasPrefix(endpoint, "rtu://") {
			limit = 5000
		}
		if *field.value > limit {
			*field.value = limit
			warn(fmt.Sprintf("device.%s：预览中明确限制为%dms", field.key, limit))
		}
	}
	startup := struct {
		Address int    `json:"address"`
		Type    string `json:"type"`
		Panel   string `json:"panel"`
	}{5, "input", "main"}
	if raw, ok := root["startup"]; ok {
		if err := mtuiConfigObject(raw, &startup); err != nil {
			return plan, fmt.Errorf("startup: %w", err)
		}
	}
	if startup.Address < 0 || startup.Address > 65535 {
		return plan, fmt.Errorf("startup.address需0..65535")
	}
	kinds := []string{"holding", "input", "coil", "discrete"}
	found := false
	for _, s := range kinds {
		if s == startup.Type {
			found = true
		}
	}
	if !found {
		return plan, fmt.Errorf("未知startup.type")
	}
	if startup.Panel != "main" {
		warn("startup.panel：使用原生结果表；其他面板通过快捷键打开")
	}
	batch := struct {
		Size        int    `json:"size"`
		Anchor      string `json:"anchor"`
		Full        bool   `json:"read_full_customs"`
		ByRegisters bool   `json:"custom_by_registers"`
	}{10, "middle", false, false}
	if raw, ok := root["batch"]; ok {
		if err := mtuiConfigObject(raw, &batch); err != nil {
			return plan, fmt.Errorf("batch: %w", err)
		}
	}
	if batch.Size < 1 || batch.Size > 125 {
		return plan, fmt.Errorf("batch.size需1..125；不会隐式扩大或拆分设备读取")
	}
	address := startup.Address
	switch batch.Anchor {
	case "start":
	case "middle":
		address -= batch.Size / 2
	case "end":
		address -= batch.Size - 1
	default:
		return plan, fmt.Errorf("未知batch.anchor")
	}
	if address < 0 {
		address = 0
	}
	if address+batch.Size > 65536 {
		address = 65536 - batch.Size
	}
	if batch.Full {
		warn("batch.read_full_customs：未自动扩大读取范围，多字解释仅使用已读取数据")
	}
	if batch.ByRegisters {
		warn("batch.custom_by_registers：原生规则按实际读取范围解释，未导入该布局模式")
	}
	interval := 1000
	if err := decode("refresh_interval_ms", &interval); err != nil || interval < 10 || interval > 86400000 {
		return plan, fmt.Errorf("refresh_interval_ms需10..86400000")
	}
	cols, err := mtuiImportColumns(root["columns"])
	if err != nil {
		return plan, err
	}
	matrix := struct {
		Columns int  `json:"columns"`
		Context bool `json:"show_context"`
	}{0, true}
	if raw, ok := root["matrix"]; ok {
		if err := mtuiConfigObject(raw, &matrix); err != nil {
			return plan, fmt.Errorf("matrix: %w", err)
		}
	}
	if matrix.Columns < 0 || matrix.Columns > 65535 {
		return plan, fmt.Errorf("matrix.columns无效")
	}
	if matrix.Columns == 0 {
		matrix.Columns = 8
	}
	if matrix.Columns > 16 {
		matrix.Columns = 16
		warn("matrix.columns：原生矩阵限制为16列")
	}
	if !matrix.Context {
		warn("matrix.show_context：原生矩阵仍显示已读取范围")
	}
	// Validate every annotation space, including ones not selected at startup.
	registers := root["registers"]
	if string(registers) == "null" {
		return plan, fmt.Errorf("registers不能为null")
	}
	if registers == nil {
		registers = json.RawMessage(`{}`)
	}
	actions := map[string]string{"holding": "read-holding", "input": "read-input", "coil": "read-coils", "discrete": "read-discrete"}
	// Put the original startup space first without dropping the other three spaces.
	sorted := []string{startup.Type}
	for _, s := range kinds {
		if s != startup.Type {
			sorted = append(sorted, s)
		}
	}
	for _, kind := range sorted {
		annotations, err := ImportMTUIRegisters(registers, actions[kind])
		if err != nil {
			return plan, err
		}
		params := map[string]any{"connect_timeout_ms": d.ConnectTimeout, "request_timeout_ms": d.RequestTimeout, "request_gap_ms": d.Gap, "address": address, "count": batch.Size, "unit": d.Unit, "word_order": order, "samples": 1, "interval_ms": interval, "matrix_columns": matrix.Columns, "columns": cols}
		for k, v := range annotations {
			params[k] = v
		}
		for k, v := range transport {
			params[k] = v
		}
		r := config.Request{ID: prefix + "-" + kind, Name: name + " · " + kind, Protocol: "modbus", Action: actions[kind], Endpoint: endpoint, Timeout: strconv.Itoa(d.ConnectTimeout+d.RequestTimeout+1000) + "ms", Params: params}
		if err := validateParams(r); err != nil {
			return plan, err
		}
		plan.Requests = append(plan.Requests, r)
	}
	converted := map[string]bool{"version": true, "name": true, "device": true, "startup": true, "batch": true, "columns": true, "registers": true, "refresh_interval_ms": true, "matrix": true}
	keys := []string{}
	for k := range root {
		if !converted[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case "api":
			warn("api：未启用或保存监听地址/端口，必须单独明确启动本机API")
		case "read_only":
			warn("read_only：只生成只读动作，绝不更改当前会话的只读权限")
		case "write_log":
			warn("write_log：未启用写日志或保存任意目录；原生运行记录保持当前设置")
		case "next_config":
			warn("next_config：未自动读取路径或轮换连接，后续配置需独立预览导入")
		case "keybinds":
			warn("keybinds：上游32个动作未套用到原生按键；请用K显式配置原生作用域快捷键")
		default:
			warn(k + "：未转换；保留原生应用当前行为")
		}
	}
	warn("已保存refresh_interval_ms供手动配置采样；samples固定1，不启动后台采样、不自动连接、不写设备")
	return plan, nil
}

func mtuiInterface(data []byte) (string, map[string]any, error) {
	var kind string
	if json.Unmarshal(data, &kind) == nil {
		if kind == "mock" {
			return "mock://local", map[string]any{}, nil
		}
		return "", nil, fmt.Errorf("未知device.interface")
	}
	var raw map[string]json.RawMessage
	if err := strictJSON(data, &raw); err != nil || len(raw) != 1 {
		return "", nil, fmt.Errorf("interface需唯一tcp/rtu_over_tcp/serial对象或mock")
	}
	for kind, value := range raw {
		switch kind {
		case "tcp", "rtu_over_tcp":
			var d struct {
				IP   string `json:"ip"`
				Port int    `json:"port"`
			}
			if err := mtuiConfigObject(value, &d); err != nil {
				return "", nil, err
			}
			if d.Port < 1 || d.Port > 65535 || d.IP == "" || !mtuiSafeString(d.IP, 253) || strings.ContainsAny(d.IP, "/@?#\\ ") {
				return "", nil, fmt.Errorf("TCP主机/端口无效，禁止嵌入认证或模板")
			}
			if strings.Contains(d.IP, ":") && net.ParseIP(d.IP) == nil {
				return "", nil, fmt.Errorf("IPv6地址无效")
			}
			scheme := "tcp://"
			if kind == "rtu_over_tcp" {
				scheme = "rtu+tcp://"
			}
			return scheme + net.JoinHostPort(d.IP, strconv.Itoa(d.Port)), map[string]any{}, nil
		case "serial":
			var d struct {
				Path   string `json:"path"`
				Baud   int    `json:"baud_rate"`
				Data   int    `json:"data_bits"`
				Parity string `json:"parity"`
				Stop   int    `json:"stop_bits"`
			}
			if err := mtuiConfigObject(value, &d); err != nil {
				return "", nil, err
			}
			if d.Path == "" || !mtuiSafeString(d.Path, 1024) || d.Baud < 1 || d.Baud > 4000000 || d.Data < 5 || d.Data > 8 || (d.Stop != 1 && d.Stop != 2) {
				return "", nil, fmt.Errorf("串口配置无效")
			}
			parity := map[string]string{"none": "N", "odd": "O", "even": "E"}[d.Parity]
			if parity == "" {
				return "", nil, fmt.Errorf("串口parity无效")
			}
			return "rtu://" + d.Path, map[string]any{"baud": d.Baud, "data_bits": d.Data, "parity": parity, "stop_bits": d.Stop}, nil
		}
	}
	return "", nil, fmt.Errorf("未知interface类型")
}
func mtuiImportColumns(raw []byte) (map[string]any, error) {
	c := struct {
		Visible []string `json:"visible"`
		Time    string   `json:"time_mode"`
		Address string   `json:"address_mode"`
		Label   int      `json:"label_width"`
		Custom  int      `json:"custom_width"`
	}{[]string{"address", "time", "u16", "i16", "hex", "ascii", "bits", "custom", "label"}, "read_at", "dec", 20, 10}
	if raw != nil {
		if err := mtuiConfigObject(raw, &c); err != nil {
			return nil, fmt.Errorf("columns: %w", err)
		}
	}
	allowed := " address time u16 i16 u8 i8 hex hex32 f16 bcd bcd32 u32 i32 u32_m10k i32_m10k u64 i64 f32 f64 ascii bits custom label "
	if len(c.Visible) < 1 || len(c.Visible) > 23 || (c.Time != "read_at" && c.Time != "ago") || (c.Address != "dec" && c.Address != "hex") || c.Label < 0 || c.Label > 120 || c.Custom < 0 || c.Custom > 120 {
		return nil, fmt.Errorf("columns列/模式/宽度无效（宽度0自动或1..120）")
	}
	visible := []any{}
	seen := map[string]bool{}
	for _, key := range c.Visible {
		if !strings.Contains(allowed, " "+key+" ") || seen[key] {
			return nil, fmt.Errorf("未知或重复MTUI列:%s", key)
		}
		seen[key] = true
		if key == "bits" {
			key = "binary"
		}
		visible = append(visible, key)
	}
	mode := "decimal"
	if c.Address == "hex" {
		mode = "hex"
	}
	widths := map[string]any{}
	if c.Label > 0 {
		widths["label"] = c.Label
	}
	if c.Custom > 0 {
		widths["custom"] = c.Custom
	}
	return map[string]any{"visible": visible, "address_mode": mode, "time_mode": c.Time, "widths": widths}, nil
}

// AppendMTUIRequests keeps existing YAML comments and recipes intact. It neither
// writes a file nor changes a session; callers explicitly review and save.
func AppendMTUIRequests(source []byte, requests []config.Request) ([]byte, error) {
	if len(requests) != 4 {
		return nil, fmt.Errorf("MTUI导入需四个只读空间")
	}
	return AppendSelectedMTUIRequests(source, requests)
}

// AppendSelectedMTUIRequests appends an explicitly selected subset of a reviewed
// conversion without overwriting existing requests or enabling writes.
func AppendSelectedMTUIRequests(source []byte, requests []config.Request) ([]byte, error) {
	c, err := config.Parse(source)
	if err != nil {
		return nil, err
	}
	if len(requests) < 1 || len(requests) > 4 {
		return nil, fmt.Errorf("请选择1..4个导入请求")
	}
	seen := map[string]bool{}
	for _, r := range c.Requests {
		seen[r.ID] = true
	}
	actions := map[string]bool{}
	for _, r := range requests {
		allowed := r.Action == "read-holding" || r.Action == "read-input" || r.Action == "read-coils" || r.Action == "read-discrete"
		if seen[r.ID] || actions[r.Action] || r.Protocol != "modbus" || !allowed || r.Mutates() || r.Int("samples", 1) != 1 {
			return nil, fmt.Errorf("请求ID冲突或不是只读Modbus请求:%s", r.ID)
		}
		if err := validateParams(r); err != nil {
			return nil, err
		}
		seen[r.ID] = true
		actions[r.Action] = true
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(source, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	var seq *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "requests" {
			seq = root.Content[i+1]
		}
	}
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("collection.requests需列表")
	}
	for _, r := range requests {
		var n yaml.Node
		if err := n.Encode(r); err != nil {
			return nil, err
		}
		seq.Content = append(seq.Content, &n)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	_, err = config.Parse(out)
	return out, err
}

// Go's JSON decoder accepts null for scalar fields; operational MTUI objects do
// not. Validate that distinction before applying defaults to omitted fields.
func mtuiConfigObject(raw []byte, out any) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var check func(any, int) error
	check = func(value any, depth int) error {
		if depth > 32 {
			return fmt.Errorf("配置嵌套超过32层")
		}
		switch x := value.(type) {
		case nil:
			return fmt.Errorf("操作配置不能含null")
		case map[string]any:
			for _, item := range x {
				if err := check(item, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := check(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := check(value, 0); err != nil {
		return err
	}
	return strictJSON(raw, out)
}
