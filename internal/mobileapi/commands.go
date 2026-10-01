package mobileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	codec "github.com/Live-yum/iotools/internal/crypto"
	"github.com/Live-yum/iotools/internal/engine"
	"go.yaml.in/yaml/v3"
)

type command struct {
	RequestIDs      []string                 `json:"request_ids,omitempty"`
	SelectionIndex  *int                     `json:"selection_index,omitempty"`
	ExecuteTriggers bool                     `json:"execute_triggers,omitempty"`
	ResultID        string                   `json:"result_id,omitempty"`
	RunID           string                   `json:"run_id,omitempty"`
	SubscriptionID  string                   `json:"subscription_id,omitempty"`
	ApplicationURI  string                   `json:"application_uri,omitempty"`
	CertPath        string                   `json:"cert_path,omitempty"`
	KeyPath         string                   `json:"key_path,omitempty"`
	Target          string                   `json:"target,omitempty"`
	Port            int                      `json:"port,omitempty"`
	TimeoutMS       int                      `json:"timeout_ms,omitempty"`
	Concurrency     int                      `json:"concurrency,omitempty"`
	Method          string                   `json:"method,omitempty"`
	Listen          string                   `json:"listen,omitempty"`
	Scope           *engine.ModbusWriteScope `json:"scope,omitempty"`
	Snapshot        *engine.RegisterSnapshot `json:"snapshot,omitempty"`
	Before          *engine.RegisterSnapshot `json:"before,omitempty"`
	After           *engine.RegisterSnapshot `json:"after,omitempty"`
	Words           map[int]uint16           `json:"words,omitempty"`
	HexAddress      bool                     `json:"hex_address,omitempty"`
	Op              string                   `json:"op"`
	Source          string                   `json:"source,omitempty"`
	Format          string                   `json:"format,omitempty"`
	Profile         string                   `json:"profile,omitempty"`
	RequestID       string                   `json:"request_id,omitempty"`
	OriginalID      string                   `json:"original_id,omitempty"`
	Request         *config.Request          `json:"request,omitempty"`
	Token           string                   `json:"token,omitempty"`
	Confirmed       bool                     `json:"confirmed,omitempty"`
	InteractionID   string                   `json:"interaction_id,omitempty"`
	Value           any                      `json:"value,omitempty"`
	Options         *Options                 `json:"options,omitempty"`
	HistoryID       int64                    `json:"history_id,omitempty"`
	IDs             []int64                  `json:"ids,omitempty"`
	Query           string                   `json:"query,omitempty"`
	Data            string                   `json:"data,omitempty"`
	Direction       string                   `json:"direction,omitempty"`
	Codec           codec.Config             `json:"codec,omitempty"`
	Codecs          map[string]codec.Config  `json:"codecs,omitempty"`
	Rules           []codec.Rule             `json:"rules,omitempty"`
	SQL             string                   `json:"sql,omitempty"`
	Backup          string                   `json:"backup,omitempty"`
	Path            string                   `json:"path,omitempty"`
	Kind            string                   `json:"kind,omitempty"`
	Address         int                      `json:"address,omitempty"`
	WordOrder       string                   `json:"word_order,omitempty"`
	Prefix          string                   `json:"prefix,omitempty"`
	Overrides       *engine.HTTPOverrides    `json:"overrides,omitempty"`
}

func (s *Session) command(c command) (any, error) {
	switch c.Op {
	case "result.get":
		return s.result(c.ResultID)
	case "modbus.pause", "modbus.resume":
		s.mu.Lock()
		controller := s.modbusPause
		s.mu.Unlock()
		if controller == nil {
			return nil, errors.New("当前没有可暂停的 Modbus 读取")
		}
		controller.SetPaused(c.Op == "modbus.pause")
		return map[string]any{"paused": controller.Paused()}, nil
	case "modbus.stats":
		s.mu.Lock()
		defer s.mu.Unlock()
		copy := map[string]any{}
		for k, v := range s.modbusStats {
			copy[k] = v
		}
		copy["session"] = s.modbusTotals
		return copy, nil
	case "subscriptions.list":
		return s.listSubscriptions(), nil
	case "subscriptions.stop":
		if c.SubscriptionID == "" {
			return nil, errors.New("subscription_id is required")
		}
		return map[string]any{"cancelled": s.stopSubscriptions(c.SubscriptionID)}, nil
	case "subscriptions.stop-all":
		return map[string]any{"cancelled": s.stopSubscriptions("")}, nil
	case "opcua.connections":
		return s.listConnections(), nil
	case "opcua.connections.clear":
		if !c.Confirmed {
			return nil, errors.New("清除连接记录需要明确确认")
		}
		return nil, s.clearConnections()
	case "files.list", "opcua.identity", "modbus.discovery.preview", "modbus.discovery.run", "modbus.controller.start", "modbus.snapshot.save", "modbus.snapshot.load", "modbus.snapshot.diff", "modbus.csv.diff", "modbus.interpret", "config.switch":
		return s.extra(c)
	case "state":
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.stateLocked(), nil
	case "catalog":
		return Catalog(), nil
	case "config.get":
		return map[string]any{"path": s.path, "source": string(s.source), "collection": s.collection}, nil
	case "config.validate":
		parsed, e := s.parseCollection([]byte(c.Source))
		return map[string]any{"collection": parsed}, e
	case "config.save":
		return s.saveSource([]byte(c.Source))
	case "config.reload":
		if e := s.idle(); e != nil {
			return nil, e
		}
		b, e := readBounded(s.path, 4<<20)
		if e != nil {
			return nil, e
		}
		return s.loadSource(b)
	case "config.import":
		parsed, e := engine.ImportCollection([]byte(c.Source), c.Format)
		if e != nil {
			return nil, e
		}
		b, e := yaml.Marshal(parsed)
		if e != nil {
			return nil, e
		}
		return map[string]any{"source": string(b), "collection": parsed}, nil
	case "request.save":
		return s.saveRequest(c)
	case "request.delete":
		if !c.Confirmed {
			return nil, errors.New("request deletion requires explicit confirmation")
		}
		if e := s.idle(); e != nil {
			return nil, e
		}
		clone := *s.collection
		clone.Requests = nil
		found := false
		for _, r := range s.collection.Requests {
			if r.ID == c.RequestID {
				found = true
			} else {
				clone.Requests = append(clone.Requests, r)
			}
		}
		if !found {
			return nil, errors.New("request not found")
		}
		b, e := yaml.Marshal(clone)
		if e != nil {
			return nil, e
		}
		return s.saveSource(b)
	case "profile.set":
		if e := s.idle(); e != nil {
			return nil, e
		}
		if c.Profile != "" {
			if _, ok := s.collection.Profiles[c.Profile]; !ok {
				return nil, errors.New("profile not found")
			}
		}
		s.mu.Lock()
		s.profile = c.Profile
		s.revision++
		s.previews = map[string]preview{}
		out := s.stateLocked()
		s.mu.Unlock()
		return out, nil
	case "options.set":
		if c.Options == nil {
			return nil, errors.New("options are required")
		}
		if e := s.idle(); e != nil {
			return nil, e
		}
		s.mu.Lock()
		transport := s.options.RTUTransport
		nativeSerial := s.options.NativeSerial
		s.options = *c.Options
		s.options.RTUTransport = transport
		s.options.NativeSerial = nativeSerial
		s.revision++
		s.previews = map[string]preview{}
		out := s.stateLocked()
		s.mu.Unlock()
		return out, nil
	case "preview":
		return s.prepare(c)
	case "run":
		return s.run(c)
	case "history.collection-script":
		sql, e := engine.HTTPHistoryCollectionScript(c.Kind, c.Source, c.Target)
		return map[string]any{"sql": sql}, e
	case "http.curl":
		collection, r, profile, e := s.selected(c)
		if e != nil {
			return nil, e
		}
		if c.ExecuteTriggers {
			if !c.Confirmed {
				return nil, errors.New("生成 curl 将执行所需依赖请求，请先明确确认")
			}
			return s.startTask("http.curl", func(ctx context.Context, id string) error {
				ctx = engine.WithHTTPWorkflowOptions(ctx, s.workflowOptions(id, true))
				value, e := engine.GenerateCurl(ctx, collection, r, profile, false, true)
				if e == nil {
					s.emit(id, "curl", map[string]any{"curl": value})
				}
				return e
			})
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctx = engine.WithHTTPWorkflowOptions(ctx, s.workflowOptions("", false))
		value, e := engine.GenerateCurl(ctx, collection, r, profile, false, false)
		return map[string]any{"curl": value}, e
	case "http.filter.start":
		if len(c.Data) > 4<<20 || len(c.Query) > 65536 {
			return nil, errors.New("JSON 筛选输入或表达式超过限制")
		}
		return s.startTask("http.filter", func(ctx context.Context, id string) error {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			result, err := engine.FilterJSON(ctx, c.Query, []byte(c.Data))
			if err != nil {
				return err
			}
			s.emit(id, "query", result)
			return nil
		})
	case "http.filter":
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return engine.FilterJSON(ctx, c.Query, []byte(c.Data))
	case "history.status", "history.list", "history.get", "history.delete", "history.collections", "history.query", "history.preview", "history.execute", "history.collection.preview":
		return s.history(c)
	case "crypto.convert":
		if len(c.Data) > 4<<20 {
			return nil, errors.New("crypto input exceeds 4 MiB")
		}
		var out []byte
		var e error
		switch c.Direction {
		case "encode", "encrypt":
			out, e = c.Codec.Encode([]byte(c.Data))
		case "decode", "decrypt":
			out, e = c.Codec.Decode([]byte(c.Data))
		default:
			return nil, errors.New("direction must be encode or decode")
		}
		return binaryResult(out), e
	case "crypto.transform":
		if len(c.Data) > 4<<20 {
			return nil, errors.New("crypto input exceeds 4 MiB")
		}
		out, e := codec.Transform([]byte(c.Data), c.Codecs, c.Rules)
		return binaryResult(out), e
	case "modbus.encode":
		order := c.WordOrder
		if order == "" {
			order = "ABCD"
		}
		return engine.EncodeModbusValue(c.Kind, fmt.Sprint(c.Value), order, c.Address)
	case "modbus.rules":
		_, r, _, e := s.selected(c)
		if e != nil {
			return nil, e
		}
		return engine.ModbusRules(r)
	case "modbus.import", "modbus.import.apply":
		return s.importMTUI(c)
	case "modbus.registers.import":
		return engine.ImportMTUIRegisters([]byte(c.Source), c.Kind)
	case "modbus.registers.export":
		_, r, _, e := s.selected(c)
		if e != nil {
			return nil, e
		}
		out, e := engine.ExportMTUIRegisters(r)
		return map[string]any{"source": string(out)}, e
	case "modbus.write-log":
		path, e := s.resolvePrivate(c.Path)
		if e != nil {
			return nil, e
		}
		return engine.ReadModbusWriteLog(path)
	case "file.read":
		path, e := s.resolvePrivate(c.Path)
		if e != nil {
			return nil, e
		}
		out, e := readBounded(path, 4<<20)
		return binaryResult(out), e
	case "file.write":
		if !c.Confirmed {
			return nil, errors.New("file write requires explicit confirmation")
		}
		if len(c.Data) > 4<<20 {
			return nil, errors.New("file input exceeds 4 MiB")
		}
		path, e := s.resolvePrivate(c.Path)
		if e != nil {
			return nil, e
		}
		if path == s.path {
			return nil, errors.New("use config.save for the active collection")
		}
		if strings.Contains(filepath.Base(path), "history.sqlite") {
			return nil, errors.New("history database cannot be overwritten")
		}
		e = config.SaveChecked(path, []byte(c.Data), func([]byte) error { return nil })
		return map[string]any{"path": path}, e
	}
	return nil, fmt.Errorf("unsupported command %q", c.Op)
}

func (s *Session) loadSource(b []byte) (any, error) {
	c, e := s.parseCollection(b)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.source = append([]byte(nil), b...)
	s.collection = c
	s.revision++
	s.previews = map[string]preview{}
	if _, ok := c.Profiles[s.profile]; !ok {
		s.profile = defaultProfile(c)
	}
	return s.stateLocked(), nil
}
func (s *Session) saveSource(b []byte) (any, error) {
	if e := s.idle(); e != nil {
		return nil, e
	}
	current, e := readBounded(s.path, 4<<20)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(current, s.source) {
		return nil, errors.New("配置文件已在其他位置更改，请重新载入后再保存")
	}
	if e := s.privatePath(s.path); e != nil {
		return nil, e
	}
	if e := config.SaveChecked(s.path, b, func(b []byte) error { _, e := s.parseCollection(b); return e }); e != nil {
		return nil, e
	}
	return s.loadSource(b)
}
func (s *Session) saveRequest(c command) (any, error) {
	if c.Request == nil {
		return nil, errors.New("request is required")
	}
	if e := s.idle(); e != nil {
		return nil, e
	}
	r := *c.Request
	if r.ID == "" {
		return nil, errors.New("request ID is required")
	}
	original := c.OriginalID
	if original == "" {
		original = r.ID
	}
	exists := false
	for _, old := range s.collection.Requests {
		if old.ID == original {
			exists = true
		}
		if old.ID == r.ID && old.ID != original {
			return nil, errors.New("request ID already exists")
		}
	}
	// Native documents retain unrelated comments when changing one request.
	if exists && original == r.ID {
		if _, e := config.Parse(s.source); e == nil {
			b, e := config.ReplaceRequest(s.source, original, r)
			if e != nil {
				return nil, e
			}
			return s.saveSource(b)
		}
	}
	// Slumber recipes require their source editor because flattening loses reusable
	// references and templates. Conversion is offered separately by config.import.
	if _, e := config.Parse(s.source); e != nil {
		return nil, errors.New("edit Slumber recipes in the source editor, or explicitly import as native first")
	}
	clone := *s.collection
	clone.Requests = append([]config.Request(nil), s.collection.Requests...)
	if exists {
		for i, old := range clone.Requests {
			if old.ID == original {
				clone.Requests[i] = r
			}
		}
	} else {
		clone.Requests = append(clone.Requests, r)
	}
	b, e := yaml.Marshal(clone)
	if e != nil {
		return nil, e
	}
	return s.saveSource(b)
}
func (s *Session) selected(c command) (*config.Collection, config.Request, string, error) {
	collection := s.collection
	r := config.Request{}
	if c.Request != nil {
		r = *c.Request
		if r.ID == "" {
			r.ID = "draft"
		}
	} else {
		for _, v := range collection.Requests {
			if v.ID == c.RequestID {
				r = v
				break
			}
		}
	}
	if r.ID == "" {
		return nil, r, "", errors.New("request not found")
	}
	profile := s.profile
	if c.Profile != "" {
		profile = c.Profile
	}
	// Deep copy isolates snapshots and runtime path normalization from saved data.
	b, e := json.Marshal(collection)
	if e != nil {
		return nil, r, "", e
	}
	var copyCollection config.Collection
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&copyCollection); e != nil {
		return nil, r, "", e
	}
	for i := range copyCollection.Requests {
		if e = normalizeRequest(&copyCollection.Requests[i]); e != nil {
			return nil, r, "", e
		}
	}
	copyCollection.SourcePath = s.path
	b, e = json.Marshal(r)
	if e != nil {
		return nil, r, "", e
	}
	dec = json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&r); e != nil {
		return nil, r, "", e
	}
	if e = normalizeRequest(&r); e != nil {
		return nil, r, "", e
	}
	collection = &copyCollection
	if c.Overrides != nil {
		collection, r, profile, e = engine.ApplyHTTPOverrides(collection, r, profile, *c.Overrides)
		if e != nil {
			return nil, r, "", e
		}
	}
	return collection, r, profile, nil
}
func (s *Session) history(c command) (any, error) {
	if s.options.ReadOnly && (c.Op == "history.delete" || c.Op == "history.execute") {
		return nil, errors.New("只读模式禁止修改历史数据库")
	}
	path := filepath.Join(s.root, "history.sqlite")
	if e := s.privatePath(path); e != nil {
		return nil, e
	}
	info, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, errors.New("无法读取历史数据库，请检查应用内历史文件")
	}
	exists := statErr == nil && info.Mode().IsRegular()
	if c.Op == "history.status" {
		return map[string]any{"exists": exists, "enabled": s.options.History}, nil
	}
	if !exists {
		if c.Op == "history.list" || c.Op == "history.collections" {
			return []any{}, nil
		}
		return nil, errors.New("暂无历史数据库：请在设置中开启 HTTP 历史并执行一次请求后使用此功能")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch c.Op {
	case "history.list":
		return engine.ListHTTPHistory(ctx, path, s.path, c.RequestID)
	case "history.get":
		return engine.GetHTTPHistory(ctx, path, s.path, c.HistoryID)
	case "history.delete":
		if s.options.ReadOnly {
			return nil, errors.New("只读模式禁止删除历史记录")
		}
		if e := s.idle(); e != nil {
			return nil, e
		}
		n, e := engine.DeleteHTTPHistory(ctx, path, s.path, c.IDs, c.Confirmed)
		return map[string]any{"deleted": n}, e
	case "history.collections":
		return engine.ListHTTPHistoryCollections(ctx, path)
	case "history.query":
		return engine.QueryHTTPHistoryScript(ctx, path, c.SQL)
	case "history.collection.preview":
		kind := c.Kind
		if kind == "rename" || kind == "merge" {
			kind = "migrate"
		}
		sql, e := engine.HTTPHistoryCollectionScript(kind, c.Source, c.Target)
		if e != nil {
			return nil, e
		}
		return engine.PreviewHTTPHistoryScript(ctx, path, sql)
	case "history.preview":
		return engine.PreviewHTTPHistoryScript(ctx, path, c.SQL)
	case "history.execute":
		if s.options.ReadOnly {
			return nil, errors.New("只读模式禁止修改历史数据库")
		}
		if e := s.idle(); e != nil {
			return nil, e
		}
		backup, e := s.resolvePrivate(c.Backup)
		if e != nil {
			return nil, e
		}
		return engine.ExecuteHTTPHistoryScript(ctx, path, c.SQL, c.Token, backup, c.Confirmed)
	}
	return nil, errors.New("unknown history operation")
}
