package mobileapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Live-yum/iotools/internal/engine"
)

func (s *Session) extra(c command) (any, error) {
	switch c.Op {
	case "opcua.identity":
		if !c.Confirmed {
			return nil, errors.New("生成新的 OPC UA 证书及私钥需要明确确认")
		}
		cert, e := s.resolvePrivate(c.CertPath)
		if e != nil {
			return nil, e
		}
		key, e := s.resolvePrivate(c.KeyPath)
		if e != nil {
			return nil, e
		}
		return s.startTask("opcua.identity", func(ctx context.Context, id string) error {
			e := engine.GenerateOPCUAClientIdentityContext(ctx, cert, key, c.ApplicationURI)
			if e == nil {
				s.emit(id, "identity", map[string]any{"cert_path": cert, "key_path": key, "application_uri": c.ApplicationURI})
			}
			return e
		})
	case "modbus.discovery.preview":
		method := c.Method
		if method == "" {
			method = "tcp"
		}
		plan, e := engine.PrepareModbusDiscoveryMethod(c.Target, c.Port, c.TimeoutMS, c.Concurrency, method)
		if e != nil {
			return nil, e
		}
		token, e := newToken()
		if e != nil {
			return nil, e
		}
		s.mu.Lock()
		if len(s.previews) >= 16 {
			s.previews = map[string]preview{}
		}
		s.previews[token] = preview{Token: token, Revision: s.revision, Expires: time.Now().Add(5 * time.Minute), Utility: &c}
		s.mu.Unlock()
		return map[string]any{"token": token, "method": plan.Method, "targets": plan.Targets, "port": plan.Port, "timeout_ms": plan.TimeoutMS, "concurrency": plan.Concurrency, "notice": "仅检查指定 IP 的连通性；不能据此认定发现了 Modbus 设备"}, nil
	case "modbus.discovery.run":
		if !c.Confirmed {
			return nil, errors.New("开始网络探测前请确认预览的确切目标")
		}
		s.mu.Lock()
		p, ok := s.previews[c.Token]
		if ok {
			delete(s.previews, c.Token)
		}
		revision := s.revision
		s.mu.Unlock()
		if !ok || p.Utility == nil || p.Utility.Op != "modbus.discovery.preview" || revision != p.Revision || time.Now().After(p.Expires) {
			return nil, errors.New("探测预览已失效")
		}
		q := p.Utility
		method := q.Method
		if method == "" {
			method = "tcp"
		}
		plan, e := engine.PrepareModbusDiscoveryMethod(q.Target, q.Port, q.TimeoutMS, q.Concurrency, method)
		if e != nil {
			return nil, e
		}
		return s.startTask("modbus.discovery", func(ctx context.Context, id string) error {
			return engine.DiscoverModbusNetwork(ctx, plan, func(r engine.ModbusDiscoveryResult) {
				s.emit(id, "discovery", map[string]any{"address": r.Address, "open": r.Open, "error": r.Error, "completed": r.Completed, "total": r.Total})
			})
		})
	case "modbus.controller.start":
		if !c.Confirmed {
			return nil, errors.New("启动本机控制接口前请明确确认地址及写入范围")
		}
		collection, r, profile, e := s.selected(c)
		if e != nil {
			return nil, e
		}
		r, e = collection.Resolve(r, profile)
		if e != nil {
			return nil, e
		}
		if e = s.prepareRequest(&r); e != nil {
			return nil, e
		}
		if c.Scope != nil && s.options.ReadOnly {
			return nil, errors.New("只读模式不允许控制接口提供写入功能")
		}
		return s.startTask("modbus.controller", func(ctx context.Context, id string) error {
			if s.options.RTUTransport != nil {
				ctx = engine.WithModbusRTUTransport(ctx, s.options.RTUTransport)
			}
			return engine.ServeModbusAPI(ctx, c.Listen, r, c.Scope)
		})
	case "modbus.snapshot.save":
		if c.Snapshot == nil {
			return nil, errors.New("snapshot is required")
		}
		if !c.Confirmed {
			return nil, errors.New("保存快照需要明确确认新文件名")
		}
		path, e := s.resolvePrivate(c.Path)
		if e != nil {
			return nil, e
		}
		e = engine.SaveRegisterSnapshot(path, *c.Snapshot)
		return map[string]any{"path": path}, e
	case "modbus.snapshot.load":
		path, e := s.resolvePrivate(c.Path)
		if e != nil {
			return nil, e
		}
		return engine.LoadRegisterSnapshot(path)
	case "modbus.snapshot.diff":
		if c.Before == nil || c.After == nil {
			return nil, errors.New("before and after snapshots are required")
		}
		return engine.DiffRegisterSnapshots(*c.Before, *c.After)
	case "modbus.csv.diff":
		before, e := engine.ParseMTUICSV([]byte(c.Source), c.HexAddress)
		if e != nil {
			return nil, e
		}
		current := map[engine.ModbusCSVCell]uint16{}
		for address, value := range c.Words {
			current[engine.ModbusCSVCell{Type: c.Kind, Address: address}] = value
		}
		return engine.DiffMTUICSV(before, current), nil
	case "modbus.interpret":
		_, r, _, e := s.selected(c)
		if e != nil {
			return nil, e
		}
		interpreter, e := engine.NewModbusInterpreter(r)
		if e != nil {
			return nil, e
		}
		return interpreter.Interpret(c.Words)
	case "config.switch":
		if e := s.idle(); e != nil {
			return nil, e
		}
		path, e := s.resolvePrivate(c.Path)
		if e != nil {
			return nil, e
		}
		b, e := readBounded(path, 4<<20)
		if e != nil {
			return nil, e
		}
		oldPath := s.path
		s.path = path
		parsed, e := s.parseCollection(b)
		s.path = oldPath
		if e != nil {
			return nil, e
		}
		encoded, e := json.Marshal(parsed)
		if e != nil {
			return nil, e
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte{}, b...), encoded...)))
		if !c.Confirmed {
			token, e := newToken()
			if e != nil {
				return nil, e
			}
			copyCommand := c
			copyCommand.Path = path
			s.mu.Lock()
			if len(s.previews) >= 16 {
				s.previews = map[string]preview{}
			}
			s.previews[token] = preview{Token: token, Utility: &copyCommand, Digest: digest, Revision: s.revision, Expires: time.Now().Add(5 * time.Minute)}
			s.mu.Unlock()
			return map[string]any{"path": path, "collection": parsed, "confirmation_required": true, "token": token}, nil
		}
		s.mu.Lock()
		p, ok := s.previews[c.Token]
		if ok {
			delete(s.previews, c.Token)
		}
		revision := s.revision
		s.mu.Unlock()
		if !ok || p.Utility == nil || p.Utility.Op != "config.switch" || p.Utility.Path != path || p.Digest != digest || p.Revision != revision || time.Now().After(p.Expires) {
			return nil, errors.New("切换预览已失效或目标内容已更改，请重新预览")
		}
		s.stopSubscriptions("")
		s.mu.Lock()
		s.path = path
		s.source = b
		s.collection = parsed
		s.profile = parsed.DefaultProfile
		s.revision++
		s.previews = map[string]preview{}
		data := s.stateLocked()
		s.mu.Unlock()
		return data, nil
	case "files.list":
		entries, e := os.ReadDir(s.root)
		if e != nil {
			return nil, e
		}
		out := []map[string]any{}
		for _, entry := range entries {
			if len(out) >= 1000 {
				break
			}
			if entry.Type().IsRegular() {
				info, e := entry.Info()
				if e != nil {
					continue
				}
				out = append(out, map[string]any{"name": entry.Name(), "bytes": info.Size(), "modified": info.ModTime(), "path": filepath.Join(s.root, entry.Name())})
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown local utility %s", c.Op)
}
func (s *Session) startTask(kind string, work func(context.Context, string) error) (any, error) {
	s.mu.Lock()
	if s.closed || s.paused || s.cancel != nil {
		s.mu.Unlock()
		return nil, errors.New("当前会话忙碌、暂停或已关闭")
	}
	id, e := newToken()
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.runID, s.runDone = cancel, id, done
	s.mu.Unlock()
	s.emit(id, "started", map[string]any{"operation": kind})
	go func() {
		var workErr error
		defer func() {
			if recover() != nil {
				workErr = errors.New("本地操作返回异常数据")
			}
			cancel()
			status := "completed"
			message := ""
			if workErr != nil {
				status = "failed"
				message = workErr.Error()
				if errors.Is(workErr, context.Canceled) {
					status = "cancelled"
				}
			}
			s.emit(id, "done", map[string]any{"operation": kind, "status": status, "error": message})
			s.mu.Lock()
			s.cancel = nil
			s.mu.Unlock()
			close(done)
		}()
		workErr = work(ctx, id)
	}()
	return map[string]any{"run_id": id}, nil
}

func (s *Session) observeModbus(id string, op engine.ModbusOperation) {
	data := map[string]any{"time": op.Time, "action": op.Action, "unit": op.Unit, "address": op.Address, "count": op.Count, "duration_ms": float64(op.Duration) / float64(time.Millisecond), "write": op.Write, "success": op.Success, "cancelled": op.Cancelled, "error_class": op.ErrorClass}
	s.mu.Lock()
	stats := s.modbusStats
	stats["operations"] = stats["operations"].(int) + 1
	key := "failure"
	if op.Success {
		key = "success"
	}
	stats[key] = stats[key].(int) + 1
	stats["duration_ms"] = stats["duration_ms"].(float64) + float64(op.Duration)/float64(time.Millisecond)
	stats["last"] = data
	s.mu.Unlock()
	s.emit(id, "modbus.metrics", data)
}
