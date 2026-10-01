package mobileapi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
)

// Only this explicit metadata allowlist is ever persisted, never passwords,
// usernames, certificates, payloads, templates or request source.
type connectionRecord struct {
	Endpoint      string    `json:"endpoint"`
	NodeID        string    `json:"node_id,omitempty"`
	Policy        string    `json:"security_policy,omitempty"`
	Mode          string    `json:"security_mode,omitempty"`
	Fingerprint   string    `json:"server_cert_sha256,omitempty"`
	LastConnected time.Time `json:"last_connected"`
}

func (s *Session) connectionPath() string { return filepath.Join(s.root, "opcua-connections.json") }
func (s *Session) loadConnections() {
	path := s.connectionPath()
	if s.privatePath(path) != nil {
		return
	}
	b, e := readBounded(path, 1<<20)
	if e != nil {
		return
	}
	var rows []connectionRecord
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if dec.Decode(&rows) != nil || len(rows) > 100 {
		return
	}
	s.connections = rows
}
func (s *Session) listConnections() []connectionRecord {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	return append([]connectionRecord{}, s.connections...)
}
func (s *Session) recordConnection(r config.Request, ev engine.Event) {
	if r.Protocol != "opcua" || ev.Kind != "connected" {
		return
	}
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	row := connectionRecord{Endpoint: r.Endpoint, NodeID: r.String("node_id", ""), Policy: r.String("security_policy", ""), Mode: r.String("security_mode", ""), Fingerprint: r.String("server_cert_sha256", ""), LastConnected: time.Now().UTC()}
	if data, ok := ev.Data.(map[string]any); ok {
		if value, ok := data["security_policy"].(string); ok {
			row.Policy = value
		}
		if value, ok := data["security_mode"].(string); ok {
			row.Mode = value
		}
	}
	rows := []connectionRecord{row}
	for _, old := range s.connections {
		if old.Endpoint != row.Endpoint && len(rows) < 100 {
			rows = append(rows, old)
		}
	}
	s.connections = rows
	path := s.connectionPath()
	if s.privatePath(path) != nil {
		return
	}
	b, e := json.Marshal(rows)
	if e != nil || len(b) > 1<<20 {
		return
	}
	_ = config.SaveChecked(path, b, func([]byte) error { return nil })
}
func (s *Session) clearConnections() error {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	path := s.connectionPath()
	if e := s.privatePath(path); e != nil {
		return e
	}
	e := os.Remove(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	s.connections = nil
	return nil
}
func boundedView(value any) any {
	b, e := json.Marshal(value)
	if e != nil {
		return map[string]any{"error": "unsupported value"}
	}
	if len(b) > 16<<10 {
		return map[string]any{"truncated": true, "bytes": len(b), "preview": string(b[:4096])}
	}
	return value
}
