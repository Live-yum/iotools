package engine

import (
	"encoding/binary"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
)

// PreviewModbusOperation exposes the exact bounded write semantics without I/O.
func PreviewModbusOperation(r config.Request) (map[string]any, error) {
	if r.Protocol != "modbus" {
		return nil, fmt.Errorf("preview requires Modbus")
	}
	if err := validateParams(r); err != nil {
		return nil, err
	}
	out := map[string]any{"unit": r.Int("unit", 1), "action": r.Action, "endpoint": r.Endpoint, "address": r.Int("address", 0)}
	switch r.Action {
	case "write-typed":
		words, err := EncodeModbusValue(r.String("value_type", ""), r.String("value", ""), r.String("word_order", "ABCD"), r.Int("address", 0))
		if err != nil {
			return nil, err
		}
		out["function_code"] = 16
		out["count"] = len(words)
		out["registers"] = words
		out["word_order"] = r.String("word_order", "ABCD")
		out["value_type"] = r.String("value_type", "")
	case "read-raw", "write-raw":
		p, err := ValidateModbusRaw(r)
		if err != nil {
			return nil, err
		}
		out["function_code"] = int(p[0])
		out["pdu_hex"] = fmt.Sprintf("%x", p)
		if p[0] == 43 {
			out["mei_type"] = int(p[1])
			out["read_code"] = int(p[2])
			out["object_id"] = int(p[3])
			break
		}
		out["address"] = int(binary.BigEndian.Uint16(p[1:3]))
		switch p[0] {
		case 1, 2, 3, 4:
			out["count"] = int(binary.BigEndian.Uint16(p[3:5]))
		case 5:
			out["count"] = 1
			out["coils"] = []bool{p[3] == 255}
		case 6:
			out["count"] = 1
			out["registers"] = []uint16{binary.BigEndian.Uint16(p[3:5])}
		case 15:
			n := int(binary.BigEndian.Uint16(p[3:5]))
			bits := make([]bool, n)
			for i := range bits {
				bits[i] = p[6+i/8]&(1<<uint(i%8)) != 0
			}
			out["count"] = n
			out["coils"] = bits
		case 16:
			n := int(binary.BigEndian.Uint16(p[3:5]))
			words := make([]uint16, n)
			for i := range words {
				words[i] = binary.BigEndian.Uint16(p[6+i*2 : 8+i*2])
			}
			out["count"] = n
			out["registers"] = words
		}
	case "read-write-registers":
		out["function_code"] = 23
		out["read_address"] = r.Int("read_address", 0)
		out["read_count"] = r.Int("read_count", 0)
		out["registers"] = r.Params["values"]
	case "write-register":
		out["function_code"] = 6
		out["count"] = 1
		out["value"] = r.Params["value"]
	case "write-coil":
		out["function_code"] = 5
		out["count"] = 1
		out["value"] = r.Params["value"]
	case "write-registers", "write-coils":
		out["values"] = r.Params["values"]
		if r.Action == "write-coils" {
			out["function_code"] = 15
		} else {
			out["function_code"] = 16
		}
	default:
		out["count"] = r.Int("count", 1)
	}
	return out, nil
}
