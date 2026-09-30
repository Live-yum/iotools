package engine

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
)

type RegisterRule struct {
	Address   *int              `yaml:"address" json:"address"`
	Repr      string            `yaml:"repr" json:"repr"`
	Next      []int             `yaml:"next,omitempty" json:"next,omitempty"`
	WordOrder string            `yaml:"word_order,omitempty" json:"word_order,omitempty"`
	Ops       []string          `yaml:"ops,omitempty" json:"ops,omitempty"`
	Enum      map[string]string `yaml:"enum,omitempty" json:"enum,omitempty"`
	Bits      map[int]string    `yaml:"bits,omitempty" json:"bits,omitempty"`
	Decimals  *int              `yaml:"decimals,omitempty" json:"decimals,omitempty"`
	Prefix    string            `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Suffix    string            `yaml:"suffix,omitempty" json:"suffix,omitempty"`
}
type registerAnnotations struct {
	Pins   []int          `yaml:"pins"`
	Labels map[int]string `yaml:"labels"`
	Rules  []RegisterRule `yaml:"rules"`
}

func reprWidth(repr string) int {
	switch repr {
	case "u16", "i16", "f16":
		return 1
	case "u32", "i32", "f32":
		return 2
	case "u64", "i64", "f64":
		return 4
	}
	return 0
}
func validOrder(v string) bool { return v == "ABCD" || v == "BADC" || v == "CDAB" || v == "DCBA" }
func parseRuleOp(s string) (byte, float64, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, 0, fmt.Errorf("operation requires + - * / ^ and a finite operand")
	}
	op := s[0]
	if op == 'x' || op == 'X' {
		op = '*'
	}
	if !strings.ContainsRune("+-*/^", rune(op)) {
		return 0, 0, fmt.Errorf("unsupported operation")
	}
	n, e := strconv.ParseFloat(strings.TrimSpace(s[1:]), 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || (op == '/' && n == 0) {
		return 0, 0, fmt.Errorf("invalid or zero-divisor operand")
	}
	return op, n, nil
}
func (r RegisterRule) addresses() ([]int, error) {
	width := reprWidth(r.Repr)
	if width == 0 || r.Address == nil {
		return nil, fmt.Errorf("rule requires explicit address and repr u16/i16/f16/u32/i32/f32/u64/i64/f64")
	}
	if len(r.Next) > width-1 {
		return nil, fmt.Errorf("too many next addresses")
	}
	out := make([]int, width)
	out[0] = *r.Address
	for i := 1; i < width; i++ {
		out[i] = out[i-1] + 1
		if i-1 < len(r.Next) {
			out[i] = r.Next[i-1]
		}
	}
	for _, a := range out {
		if a < 0 || a > 65535 {
			return nil, fmt.Errorf("rule address outside 0..65535")
		}
	}
	return out, nil
}
func parseRegisterAnnotations(r config.Request) (registerAnnotations, error) {
	var a registerAnnotations
	params := map[string]any{}
	for _, k := range []string{"pins", "labels", "rules"} {
		if v, ok := r.Params[k]; ok {
			params[k] = v
		}
	}
	b, e := yaml.Marshal(params)
	if e != nil {
		return a, e
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if e = d.Decode(&a); e != nil {
		return a, fmt.Errorf("invalid register annotations: %w", e)
	}
	if len(a.Pins) > 65536 || len(a.Labels) > 65536 || len(a.Rules) > 4096 {
		return a, fmt.Errorf("too many register annotations")
	}
	for _, p := range a.Pins {
		if p < 0 || p > 65535 {
			return a, fmt.Errorf("pin outside address range")
		}
	}
	for p, s := range a.Labels {
		if p < 0 || p > 65535 || len(s) > 1024 {
			return a, fmt.Errorf("invalid label address or length")
		}
	}
	seen := map[int]bool{}
	for _, rule := range a.Rules {
		if _, e = rule.addresses(); e != nil {
			return a, e
		}
		if seen[*rule.Address] {
			return a, fmt.Errorf("duplicate rule address")
		}
		seen[*rule.Address] = true
		if rule.WordOrder != "" && !validOrder(rule.WordOrder) {
			return a, fmt.Errorf("invalid rule word_order")
		}
		if rule.Decimals != nil && (*rule.Decimals < 0 || *rule.Decimals > 15) {
			return a, fmt.Errorf("decimals must be 0..15")
		}
		if len(rule.Ops) > 64 || len(rule.Prefix)+len(rule.Suffix) > 2048 {
			return a, fmt.Errorf("rule exceeds size bound")
		}
		for _, op := range rule.Ops {
			if _, _, e = parseRuleOp(op); e != nil {
				return a, e
			}
		}
		for k := range rule.Enum {
			if _, e = strconv.ParseInt(k, 10, 64); e != nil {
				return a, fmt.Errorf("enum keys must be signed integers")
			}
		}
		for k, s := range rule.Bits {
			if k < 0 || k >= reprWidth(rule.Repr)*16 || len(s) > 1024 {
				return a, fmt.Errorf("bit index outside representation width or label too long")
			}
		}
	}
	return a, nil
}
func (r RegisterRule) evaluate(values map[int]uint16, order string) (string, *float64, error) {
	addresses, e := r.addresses()
	if e != nil {
		return "", nil, e
	}
	data := make([]byte, len(addresses)*2)
	for i, a := range addresses {
		v, ok := values[a]
		if !ok {
			return "", nil, nil
		}
		binary.BigEndian.PutUint16(data[i*2:], v)
	}
	if r.WordOrder != "" {
		order = r.WordOrder
	}
	data = orderedRegisterBytes(data, order)
	var raw uint64
	for _, b := range data {
		raw = raw<<8 | uint64(b)
	}
	var v float64
	switch r.Repr {
	case "u16", "u32", "u64":
		v = float64(raw)
	case "i16":
		v = float64(int16(raw))
	case "i32":
		v = float64(int32(raw))
	case "i64":
		v = float64(int64(raw))
	case "f16":
		v = halfFloat(uint16(raw))
	case "f32":
		v = float64(math.Float32frombits(uint32(raw)))
	case "f64":
		v = math.Float64frombits(raw)
	}
	// Enumeration uses exact integral values. Fractional/overflow float values never
	// silently alias an integer enumeration key.
	if !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) && v >= math.MinInt64 && v < math.MaxInt64 {
		if s, ok := r.Enum[strconv.FormatInt(int64(v), 10)]; ok {
			return r.Prefix + s + r.Suffix, nil, nil
		}
	}
	if len(r.Bits) > 0 {
		keys := make([]int, 0, len(r.Bits))
		for k := range r.Bits {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		names := []string{}
		for _, k := range keys {
			if raw&(uint64(1)<<uint(k)) != 0 {
				names = append(names, r.Bits[k])
			}
		}
		s := "(none)"
		if len(names) > 0 {
			s = strings.Join(names, "|")
		}
		return r.Prefix + s + r.Suffix, nil, nil
	}
	for _, s := range r.Ops {
		op, n, e := parseRuleOp(s)
		if e != nil {
			return "", nil, e
		}
		switch op {
		case '+':
			v += n
		case '-':
			v -= n
		case '*':
			v *= n
		case '/':
			v /= n
		case '^':
			v = math.Pow(v, n)
		}
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return r.Prefix + "不可解释" + r.Suffix, nil, nil
	}
	s := floatText(v)
	if r.Decimals != nil {
		s = strconv.FormatFloat(v, 'f', *r.Decimals, 64)
	}
	return r.Prefix + s + r.Suffix, &v, nil
}
func annotateRegisters(rows []map[string]any, a registerAnnotations, order string) error {
	values := map[int]uint16{}
	indexed := map[int]map[string]any{}
	for _, row := range rows {
		address := row["address"].(int)
		values[address] = row["u16"].(uint16)
		indexed[address] = row
		if label, ok := a.Labels[address]; ok {
			row["label"] = label
		}
	}
	for _, p := range a.Pins {
		if row := indexed[p]; row != nil {
			row["pinned"] = true
		}
	}
	for _, r := range a.Rules {
		row := indexed[*r.Address]
		if row == nil {
			continue
		}
		text, n, e := r.evaluate(values, order)
		if e != nil {
			return e
		}
		row["custom"] = text
		if n != nil {
			row["custom_numeric"] = *n
		}
	}
	return nil
}
