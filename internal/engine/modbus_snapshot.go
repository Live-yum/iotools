package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// RegisterSnapshot preserves raw values, not lossy floating interpretations.
// Endpoint/unit/function metadata prevents comparing unrelated device spaces.
type RegisterSnapshot struct {
	Version  int            `json:"version"`
	Endpoint string         `json:"endpoint"`
	Unit     int            `json:"unit"`
	Action   string         `json:"action"`
	Time     time.Time      `json:"time"`
	Values   map[int]uint16 `json:"values"`
}
type RegisterChange struct {
	Address int     `json:"address"`
	Before  *uint16 `json:"before"`
	After   *uint16 `json:"after"`
}

func NewRegisterSnapshot(endpoint string, unit int, action string, rows []map[string]any) (RegisterSnapshot, error) {
	s := RegisterSnapshot{Version: 1, Endpoint: endpoint, Unit: unit, Action: action, Time: time.Now().UTC(), Values: map[int]uint16{}}
	for _, row := range rows {
		address, ok := row["address"].(int)
		if !ok || address < 0 || address > 65535 {
			return s, fmt.Errorf("invalid snapshot address")
		}
		value, ok := row["u16"].(uint16)
		if !ok {
			return s, fmt.Errorf("snapshot requires raw uint16 registers")
		}
		if _, ok = s.Values[address]; ok {
			return s, fmt.Errorf("duplicate snapshot address")
		}
		s.Values[address] = value
	}
	return s, validateRegisterSnapshot(s)
}
func validateRegisterSnapshot(s RegisterSnapshot) error {
	if s.Version != 1 || s.Endpoint == "" || s.Unit < 1 || s.Unit > 247 || (s.Action != "read-holding" && s.Action != "read-input") || len(s.Values) > 65536 {
		return fmt.Errorf("invalid snapshot metadata")
	}
	for a := range s.Values {
		if a < 0 || a > 65535 {
			return fmt.Errorf("invalid snapshot address")
		}
	}
	return nil
}

// SaveRegisterSnapshot refuses overwrite: each capture is an immutable baseline.
func SaveRegisterSnapshot(path string, s RegisterSnapshot) error {
	if e := validateRegisterSnapshot(s); e != nil {
		return e
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	ok = true
	return nil
}
func LoadRegisterSnapshot(path string) (RegisterSnapshot, error) {
	var s RegisterSnapshot
	f, e := os.Open(path)
	if e != nil {
		return s, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 2<<20+1))
	if e != nil {
		return s, e
	}
	if len(b) > 2<<20 {
		return s, fmt.Errorf("snapshot exceeds 2 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&s); e != nil {
		return s, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return s, fmt.Errorf("expected one snapshot")
	}
	return s, validateRegisterSnapshot(s)
}
func DiffRegisterSnapshots(before, after RegisterSnapshot) ([]RegisterChange, error) {
	if e := validateRegisterSnapshot(before); e != nil {
		return nil, e
	}
	if e := validateRegisterSnapshot(after); e != nil {
		return nil, e
	}
	if before.Endpoint != after.Endpoint || before.Unit != after.Unit || before.Action != after.Action {
		return nil, fmt.Errorf("snapshot endpoint/unit/register type mismatch")
	}
	all := map[int]bool{}
	for a := range before.Values {
		all[a] = true
	}
	for a := range after.Values {
		all[a] = true
	}
	addresses := make([]int, 0, len(all))
	for a := range all {
		addresses = append(addresses, a)
	}
	sort.Ints(addresses)
	out := []RegisterChange{}
	for _, a := range addresses {
		b, bok := before.Values[a]
		n, nok := after.Values[a]
		if bok && nok && b == n {
			continue
		}
		change := RegisterChange{Address: a}
		if bok {
			v := b
			change.Before = &v
		}
		if nok {
			v := n
			change.After = &v
		}
		out = append(out, change)
	}
	return out, nil
}
