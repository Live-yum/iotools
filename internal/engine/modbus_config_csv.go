package engine

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// ModbusCSVCell includes the register space so equal addresses in different
// spaces cannot alias. CSV dumps carry no trustworthy endpoint or unit metadata.
type ModbusCSVCell struct {
	Type    string
	Address int
}
type ModbusCSVValue struct {
	Value uint16
	Time  string
}
type ModbusCSVDiff struct {
	Cell   ModbusCSVCell
	Before uint16
	After  *uint16
	Time   string
}

// ParseMTUICSV accepts upstream dump columns and native dumps. Raw precedence is
// u16, hex, i16, bits. Duplicate cells follow the upstream last-row-wins rule.
func ParseMTUICSV(data []byte, hexAddress bool) (map[ModbusCSVCell]ModbusCSVValue, error) {
	if len(data) == 0 || len(data) > 4<<20 {
		return nil, fmt.Errorf("CSV需1..4MiB")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !bytes.Contains(data, []byte("\n")) {
		data = bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	if len(header) < 3 || len(header) > 128 || strings.TrimSpace(header[0]) != "type" {
		return nil, fmt.Errorf("CSV第一列必须为type，最多128列")
	}
	positions := map[string]int{}
	for i, s := range header {
		s = strings.TrimSpace(s)
		if _, ok := positions[s]; ok {
			return nil, fmt.Errorf("重复CSV列:%s", s)
		}
		positions[s] = i
	}
	addr, ok := positions["address"]
	if !ok {
		return nil, fmt.Errorf("CSV缺少address")
	}
	rawName := ""
	rawIndex := 0
	for _, key := range []string{"u16", "hex", "i16", "bits", "binary"} {
		if i, ok := positions[key]; ok {
			rawName, rawIndex = key, i
			break
		}
	}
	if rawName == "" {
		return nil, fmt.Errorf("CSV需要u16/hex/i16/bits原始值列")
	}
	out := map[ModbusCSVCell]ModbusCSVValue{}
	rows := 0
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		rows++
		if err != nil {
			return nil, fmt.Errorf("CSV记录%d: %w", rows+1, err)
		}
		if rows > 262144 {
			return nil, fmt.Errorf("CSV最多262144行")
		}
		if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
			continue
		}
		if len(record) != len(header) {
			return nil, fmt.Errorf("CSV记录%d列数不匹配", rows+1)
		}
		kind := strings.ToLower(strings.TrimSpace(record[0]))
		switch kind {
		case "holding", "input", "coil", "discrete":
		case "coils":
			kind = "coil"
		default:
			return nil, fmt.Errorf("CSV记录%d寄存器类型无效", rows+1)
		}
		addressText := strings.TrimSpace(record[addr])
		base := 10
		if hexAddress {
			base = 16
		}
		if strings.HasPrefix(strings.ToLower(addressText), "0x") {
			base = 16
			addressText = addressText[2:]
		}
		address, e := strconv.ParseUint(addressText, base, 16)
		if e != nil {
			return nil, fmt.Errorf("CSV记录%d地址无效", rows+1)
		}
		text := strings.TrimSpace(record[rawIndex])
		var value uint64
		switch rawName {
		case "u16":
			value, e = strconv.ParseUint(text, 10, 16)
		case "i16":
			var n int64
			n, e = strconv.ParseInt(text, 10, 16)
			value = uint64(uint16(int16(n)))
		case "hex":
			if strings.HasPrefix(strings.ToLower(text), "0x") {
				text = text[2:]
			}
			if len(text) != 4 {
				return nil, fmt.Errorf("CSV记录%d hex需4位", rows+1)
			}
			value, e = strconv.ParseUint(text, 16, 16)
		case "bits", "binary":
			groups := strings.Split(text, " ")
			if len(groups) == 4 {
				for _, g := range groups {
					if len(g) != 4 {
						return nil, fmt.Errorf("bits需四组四位")
					}
				}
				text = strings.Join(groups, "")
			} else if rawName == "bits" {
				return nil, fmt.Errorf("bits需四组四位")
			}
			if len(text) != 16 {
				return nil, fmt.Errorf("binary需16位")
			}
			value, e = strconv.ParseUint(text, 2, 16)
		}
		if e != nil || ((kind == "coil" || kind == "discrete") && value > 1) {
			return nil, fmt.Errorf("CSV记录%d原始值无效", rows+1)
		}
		timestamp := ""
		if i, ok := positions["time"]; ok {
			timestamp = strings.TrimSpace(record[i])
			if timestamp == "now" || strings.HasSuffix(timestamp, " ago") {
				timestamp = ""
			}
			if len(timestamp) > 128 {
				return nil, fmt.Errorf("CSV时间字段过长")
			}
		}
		out[ModbusCSVCell{kind, int(address)}] = ModbusCSVValue{uint16(value), timestamp}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("CSV没有数据行")
	}
	return out, nil
}

// DiffMTUICSV classifies only snapshot cells, as upstream does. Nil After means
// unread locally, never deleted from a device. No device reads are performed.
func DiffMTUICSV(before map[ModbusCSVCell]ModbusCSVValue, current map[ModbusCSVCell]uint16) []ModbusCSVDiff {
	cells := make([]ModbusCSVCell, 0, len(before))
	for cell := range before {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Type == cells[j].Type {
			return cells[i].Address < cells[j].Address
		}
		return cells[i].Type < cells[j].Type
	})
	out := make([]ModbusCSVDiff, 0, len(cells))
	for _, cell := range cells {
		old := before[cell]
		entry := ModbusCSVDiff{Cell: cell, Before: old.Value, Time: old.Time}
		if n, ok := current[cell]; ok {
			value := n
			entry.After = &value
		}
		out = append(out, entry)
	}
	return out
}
