package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
)

type mtuiRegisterEntry struct {
	Address *int          `json:"address"`
	Label   *string       `json:"label,omitempty"`
	Pinned  bool          `json:"pinned,omitempty"`
	Custom  *RegisterRule `json:"custom,omitempty"`
}
type mtuiRegisters struct {
	Holdings  []mtuiRegisterEntry `json:"holdings,omitempty"`
	Inputs    []mtuiRegisterEntry `json:"inputs,omitempty"`
	Coils     []mtuiRegisterEntry `json:"coils,omitempty"`
	Discretes []mtuiRegisterEntry `json:"discretes,omitempty"`
}

func mtuiSection(action string) string {
	switch action {
	case "read-holding", "sweep-holding", "search-holding", "write-register", "write-registers", "write-typed", "read-write-registers":
		return "holdings"
	case "read-input", "sweep-input":
		return "inputs"
	case "read-coils", "sweep-coils", "write-coil", "write-coils":
		return "coils"
	case "read-discrete", "sweep-discrete":
		return "discretes"
	}
	return ""
}
func strictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

// ImportMTUIRegisters imports only the selected register space. It never imports
// device targets, write permissions, API settings or automatically runs a request.
// Callers preview/merge these three params and explicitly save the collection.
func ImportMTUIRegisters(data []byte, action string) (map[string]any, error) {
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("MTUI import exceeds4MiB")
	}
	section := mtuiSection(action)
	if section == "" {
		return nil, fmt.Errorf("choose a holding/input/coil/discrete request before import")
	}
	var wrapper map[string]json.RawMessage
	if e := json.Unmarshal(data, &wrapper); e != nil {
		return nil, e
	}
	if v, ok := wrapper["registers"]; ok {
		if version, exists := wrapper["version"]; exists {
			var n int
			if e := json.Unmarshal(version, &n); e != nil || n != 1 {
				return nil, fmt.Errorf("unsupported MTUI config version")
			}
		}
		data = v
	}
	var input mtuiRegisters
	if e := strictJSON(data, &input); e != nil {
		return nil, e
	}
	all := map[string][]mtuiRegisterEntry{"holdings": input.Holdings, "inputs": input.Inputs, "coils": input.Coils, "discretes": input.Discretes}
	// Validate every section; invalid ignored sections must not hide corrupt data.
	converted := map[string]map[string]any{}
	for name, entries := range all {
		pins := []any{}
		labels := map[string]any{}
		rules := []any{}
		seen := map[int]bool{}
		if len(entries) > 65536 {
			return nil, fmt.Errorf("too many register entries")
		}
		for _, entry := range entries {
			if entry.Address == nil || *entry.Address < 0 || *entry.Address > 65535 || seen[*entry.Address] {
				return nil, fmt.Errorf("missing, duplicate or invalid MTUI address")
			}
			address := *entry.Address
			seen[address] = true
			if entry.Pinned {
				pins = append(pins, address)
			}
			if entry.Label != nil {
				labels[strconv.Itoa(address)] = *entry.Label
			}
			if entry.Custom != nil {
				rule := *entry.Custom
				if rule.Address != nil {
					return nil, fmt.Errorf("custom address belongs on register entry")
				}
				rule.Address = &address
				rule.WordOrder = strings.ToUpper(rule.WordOrder)
				b, e := yaml.Marshal(rule)
				if e != nil {
					return nil, e
				}
				var v map[string]any
				if e = yaml.Unmarshal(b, &v); e != nil {
					return nil, e
				}
				rules = append(rules, v)
			}
		}
		params := map[string]any{"pins": pins, "labels": labels, "rules": rules}
		if _, e := parseRegisterAnnotations(config.Request{Params: params}); e != nil {
			return nil, e
		}
		converted[name] = params
	}
	return converted[section], nil
}

// ExportMTUIRegisters emits the upstream register payload format for one space.
func ExportMTUIRegisters(r config.Request) ([]byte, error) {
	section := mtuiSection(r.Action)
	if section == "" {
		return nil, fmt.Errorf("unsupported register space")
	}
	a, e := parseRegisterAnnotations(r)
	if e != nil {
		return nil, e
	}
	entries := map[int]*mtuiRegisterEntry{}
	get := func(address int) *mtuiRegisterEntry {
		if p := entries[address]; p != nil {
			return p
		}
		v := address
		p := &mtuiRegisterEntry{Address: &v}
		entries[address] = p
		return p
	}
	for _, address := range a.Pins {
		get(address).Pinned = true
	}
	for address, label := range a.Labels {
		v := label
		get(address).Label = &v
	}
	for _, rule := range a.Rules {
		address := *rule.Address
		rule.Address = nil
		rule.WordOrder = strings.ToLower(rule.WordOrder)
		get(address).Custom = &rule
	}
	addresses := make([]int, 0, len(entries))
	for address := range entries {
		addresses = append(addresses, address)
	}
	sort.Ints(addresses)
	list := []mtuiRegisterEntry{}
	for _, address := range addresses {
		list = append(list, *entries[address])
	}
	return json.MarshalIndent(map[string]any{section: list}, "", "  ")
}
