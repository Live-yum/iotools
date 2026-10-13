package mobileapi

import (
	"encoding/json"
	"errors"
)

const (
	maxScanResultBytes = 16 << 20
	maxScanResultNodes = 100000
	maxScanPageBytes   = 1 << 20
)

// Only the latest foreground scan is retained. Payloads share the existing
// 32 MiB result-cache budget; event queue bounds remain independent/unchanged.
type scanResult struct {
	runID    string
	nodes    []json.RawMessage
	bytes    int
	metadata map[string]json.RawMessage
	finished bool
	complete bool
	failure  string
}

func (s *Session) scanBytesLocked() int {
	if s.scanResult == nil {
		return 0
	}
	return s.scanResult.bytes
}

func (s *Session) scanDescriptorLocked() any {
	r := s.scanResult
	if r == nil {
		return nil
	}
	return map[string]any{"run_id": r.runID, "node_count": len(r.nodes), "bytes": r.bytes, "finished": r.finished, "complete": r.finished && r.complete && r.failure == "", "error": r.failure, "summary": r.metadata["scan-summary"], "namespace": r.metadata["scan-namespace"]}
}

func (s *Session) captureScanLocked(runID, kind string, data any) {
	r := s.scanResult
	if r == nil || r.runID != runID || r.finished || r.failure != "" {
		return
	}
	if kind != "scan-node" && kind != "scan-summary" && kind != "scan-namespace" {
		return
	}
	raw, err := wireMarshal(data)
	fail := func(message string) {
		r.failure = message
		if s.runID == runID && s.cancel != nil {
			s.cancel()
		}
	}
	if err != nil || len(raw) > maxEventBytes {
		fail("OPC UA scan contains an unsupported or oversized node/metadata (64 KiB); result is incomplete")
		return
	}
	oldBytes := len(r.metadata[kind])
	if kind == "scan-node" {
		oldBytes = 0
		if len(r.nodes) >= maxScanResultNodes {
			fail("OPC UA scan result exceeds 100000 nodes; narrow the scan, result is incomplete")
			return
		}
	}
	nextBytes := r.bytes + len(raw) - oldBytes
	if nextBytes > maxScanResultBytes {
		fail("OPC UA scan result exceeds 16 MiB; narrow the scan, result is incomplete")
		return
	}
	for len(s.resultOrder) > 0 && s.resultBytes+nextBytes > maxResultCacheBytes {
		s.evictResultLocked()
	}
	if kind == "scan-node" {
		r.nodes = append(r.nodes, raw)
	} else {
		if r.metadata == nil {
			r.metadata = map[string]json.RawMessage{}
		}
		r.metadata[kind] = raw
		if kind == "scan-summary" {
			var summary struct {
				Complete bool `json:"complete"`
				Nodes    int  `json:"nodes"`
			}
			_ = json.Unmarshal(raw, &summary)
			r.complete = summary.Complete
			if summary.Complete && summary.Nodes != len(r.nodes) {
				fail("OPC UA scan summary count differs from retained nodes; result is incomplete")
			}
		}
	}
	r.bytes = nextBytes
}

func (s *Session) finishScan(runID string, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.scanResult
	if r == nil || r.runID != runID {
		return runErr
	}
	r.finished = true
	if r.failure != "" {
		return errors.New(r.failure)
	}
	if runErr != nil {
		r.failure = runErr.Error()
		if len(r.failure) > 2048 {
			r.failure = r.failure[:2048]
		}
	} else if !r.complete {
		r.failure = "OPC UA scan ended without a complete summary; result is incomplete"
		runErr = errors.New(r.failure)
	}
	return runErr
}

// Pagination reads local immutable node records only. The run ID is the cursor
// identity; a new run/close invalidates it instead of returning another tree.
func (s *Session) scanPage(c command) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.scanResult
	if s.closed || r == nil || c.RunID == "" || c.RunID != r.runID {
		return nil, errors.New("scan result expired; run_id no longer identifies the retained tree")
	}
	limit := c.Limit
	if limit == 0 {
		limit = 100
	}
	if c.Offset < 0 || c.Offset > len(r.nodes) || limit < 1 || limit > 200 {
		return nil, errors.New("scan page requires valid offset and limit 1..200")
	}
	nodes := make([]json.RawMessage, 0, limit)
	next, size := c.Offset, 0
	for next < len(r.nodes) && len(nodes) < limit {
		raw := r.nodes[next]
		if size+len(raw) > maxScanPageBytes {
			break
		}
		nodes = append(nodes, raw)
		size += len(raw)
		next++
	}
	return map[string]any{"run_id": r.runID, "offset": c.Offset, "next_offset": next, "nodes": nodes, "scan_result": s.scanDescriptorLocked()}, nil
}
