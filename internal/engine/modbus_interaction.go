package engine

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/Live-yum/iotools/internal/config"
)

// EncodeModbusValue is a local-only preview. It shares the exact, bounded
// encoder used by write-typed and never opens a transport.
func EncodeModbusValue(kind, value, order string, address int) ([]uint16, error) {
	if address < 0 || address > 65535 {
		return nil, fmt.Errorf("address must be 0..65535")
	}
	values, err := encodeTypedRegisters(kind, value, order)
	if err != nil {
		return nil, err
	}
	if address+len(values) > 65536 {
		return nil, fmt.Errorf("typed value crosses end of register range")
	}
	out := make([]uint16, len(values))
	for i, v := range values {
		out[i] = uint16(v.(int))
	}
	return out, nil
}

// ModbusInterpreter holds validated annotations for repeated local previews.
// A call must contain only words from ONE response, never a cache union.
type ModbusInterpreter struct {
	annotations registerAnnotations
	order       string
	rules       map[int]RegisterRule
}

func NewModbusInterpreter(r config.Request) (*ModbusInterpreter, error) {
	order := r.String("word_order", "ABCD")
	if !validOrder(order) {
		return nil, fmt.Errorf("invalid word_order")
	}
	annotations, err := parseRegisterAnnotations(r)
	if err != nil {
		return nil, err
	}
	rules := map[int]RegisterRule{}
	for _, rule := range annotations.Rules {
		rules[*rule.Address] = rule
	}
	return &ModbusInterpreter{annotations: annotations, order: order, rules: rules}, nil
}
func (m *ModbusInterpreter) Interpret(words map[int]uint16) ([]map[string]any, error) {
	if m == nil || len(words) > 2000 {
		return nil, fmt.Errorf("one response is limited to 2000 words")
	}
	addresses := make([]int, 0, len(words))
	for a := range words {
		if a < 0 || a > 65535 {
			return nil, fmt.Errorf("address must be 0..65535")
		}
		addresses = append(addresses, a)
	}
	sort.Ints(addresses)
	rows := []map[string]any{}
	for start := 0; start < len(addresses); {
		end := start + 1
		for end < len(addresses) && addresses[end] == addresses[end-1]+1 {
			end++
		}
		data := make([]byte, 2*(end-start))
		for i := start; i < end; i++ {
			binary.BigEndian.PutUint16(data[(i-start)*2:], words[addresses[i]])
		}
		rows = append(rows, decodeRegisters(data, addresses[start], m.order)...)
		start = end
	}
	if err := annotateRegisters(rows, m.annotations, m.order); err != nil {
		return nil, err
	}
	return rows, nil
}

// Field reinterprets only a selected row, keeping graph work bounded by response
// width rather than the full number of rows. Noncontiguous rules remain local to
// the supplied single response. Missing operands produce no numeric value.
func (m *ModbusInterpreter) Field(words map[int]uint16, address int) (map[string]any, error) {
	if m == nil || len(words) > 2000 || address < 0 || address > 65535 {
		return nil, fmt.Errorf("invalid bounded response")
	}
	if _, ok := words[address]; !ok {
		return nil, nil
	}
	data := []byte{}
	for i := 0; i < 4 && address+i <= 65535; i++ {
		w, ok := words[address+i]
		if !ok {
			break
		}
		data = binary.BigEndian.AppendUint16(data, w)
	}
	row := decodeRegisters(data, address, m.order)[0]
	if label, ok := m.annotations.Labels[address]; ok {
		row["label"] = label
	}
	if rule, ok := m.rules[address]; ok {
		s, n, err := rule.evaluate(words, m.order)
		if err != nil {
			return nil, err
		}
		row["custom"] = s
		if n != nil {
			row["custom_numeric"] = *n
		}
	}
	return row, nil
}
