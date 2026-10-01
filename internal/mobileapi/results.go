package mobileapi

import (
	"encoding/json"
	"errors"
)

const (
	maxCachedResults     = 8
	maxResultCacheBytes  = 32 << 20
	maxCachedResultBytes = (16 << 20) - 4096
)

type cachedResult struct {
	RunID, Kind string
	JSON        json.RawMessage
}

func (s *Session) cacheResultLocked(id, runID, kind string, raw []byte) {
	for len(s.resultOrder) > 0 && (len(s.resultOrder) >= maxCachedResults || s.resultBytes+len(raw) > maxResultCacheBytes) {
		expired := s.resultOrder[0]
		s.resultOrder = s.resultOrder[1:]
		old := s.results[expired]
		s.resultBytes -= len(old.JSON)
		delete(s.results, expired)
		s.evictedResultCount++
		if len(s.evictedResults) >= 256 {
			s.evictedResults = s.evictedResults[1:]
		}
		s.evictedResults = append(s.evictedResults, expired)
	}
	s.results[id] = cachedResult{RunID: runID, Kind: kind, JSON: raw}
	s.resultOrder = append(s.resultOrder, id)
	s.resultBytes += len(raw)
}
func (s *Session) result(id string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.results[id]
	if !ok {
		return nil, errors.New("完整结果已从有界内存缓存移除，请缩小查询范围后重新执行")
	}
	return map[string]any{"result_id": id, "run_id": entry.RunID, "kind": entry.Kind, "data": entry.JSON}, nil
}
