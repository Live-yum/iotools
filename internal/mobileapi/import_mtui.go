package mobileapi

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"os"
	"time"
)

func (s *Session) importMTUI(c command) (any, error) {
	if err := s.idle(); err != nil {
		return nil, err
	}
	if c.Op == "modbus.import" {
		plan, err := engine.ImportMTUIConfig([]byte(c.Source), c.Prefix)
		if err != nil {
			return nil, err
		}
		// Detect ID collisions and incompatible collection formats before preview.
		if _, err = engine.AppendMTUIRequests(s.source, plan.Requests); err != nil {
			return nil, err
		}
		token, err := newToken()
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		if len(s.previews) >= 16 {
			s.previews = map[string]preview{}
		}
		s.previews[token] = preview{Token: token, Utility: &c, Revision: s.revision, Expires: time.Now().Add(5 * time.Minute)}
		s.mu.Unlock()
		return map[string]any{"token": token, "requests": plan.Requests, "warnings": plan.Warnings}, nil
	}
	if !c.Confirmed {
		return nil, errors.New("导入保存前需要明确确认新请求")
	}
	s.mu.Lock()
	p, ok := s.previews[c.Token]
	revision := s.revision
	if ok {
		delete(s.previews, c.Token)
	}
	s.mu.Unlock()
	if !ok || p.Utility == nil || p.Utility.Op != "modbus.import" || p.Revision != revision || time.Now().After(p.Expires) {
		return nil, errors.New("导入预览已失效，请重新预览")
	}
	plan, err := engine.ImportMTUIConfig([]byte(p.Utility.Source), p.Utility.Prefix)
	if err != nil {
		return nil, err
	}
	if len(c.RequestIDs) < 1 || len(c.RequestIDs) > 4 {
		return nil, errors.New("请选择1..4个预览中的请求")
	}
	selected := map[string]bool{}
	for _, id := range c.RequestIDs {
		if selected[id] {
			return nil, errors.New("导入选择重复")
		}
		selected[id] = true
	}
	requests := []config.Request{}
	for _, r := range plan.Requests {
		if selected[r.ID] {
			requests = append(requests, r)
			delete(selected, r.ID)
		}
	}
	if len(selected) != 0 {
		return nil, errors.New("导入选择不属于此预览")
	}
	source, err := engine.AppendSelectedMTUIRequests(s.source, requests)
	if err != nil {
		return nil, err
	}
	current, err := readBounded(s.path, 4<<20)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(current, s.source) {
		return nil, errors.New("配置已被其他操作修改，请重新载入和预览")
	}
	backup := s.path + ".before-import-" + c.Token[:12] + ".bak"
	file, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("不能创建独立导入备份：%w", err)
	}
	_, err = file.Write(current)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("备份未完成，原配置未修改：%w", err)
	}
	result, err := s.saveSource(source)
	if err != nil {
		return nil, fmt.Errorf("保存失败，原配置备份位于 %s：%w", backup, err)
	}
	state := result.(map[string]any)
	state["backup"] = backup
	return state, nil
}
