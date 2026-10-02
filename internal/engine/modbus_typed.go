package engine

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"github.com/Live-yum/iotools/internal/config"
)

func encodeTypedRegisters(kind string, value any, order string) ([]any, error) {
	width := reprWidth(kind)
	if width == 0 || !validOrder(order) {
		return nil, fmt.Errorf("invalid value_type or word_order")
	}
	if value == nil {
		return nil, fmt.Errorf("typed write requires value")
	}
	text := fmt.Sprint(value)
	var raw uint64
	switch kind {
	case "u16", "u32", "u64":
		if kind == "u64" {
			switch value.(type) {
			case string, uint64, int, int64:
			default:
				return nil, fmt.Errorf("u64 values require an exact decimal string or integer")
			}
		}
		n, e := strconv.ParseUint(text, 10, width*16)
		if e != nil {
			return nil, fmt.Errorf("value outside %s integer range", kind)
		}
		raw = n
	case "i16", "i32", "i64":
		if kind == "i64" {
			switch value.(type) {
			case string, int, int64:
			default:
				return nil, fmt.Errorf("i64 values require an exact decimal string or integer")
			}
		}
		n, e := strconv.ParseInt(text, 10, width*16)
		if e != nil {
			return nil, fmt.Errorf("value outside %s integer range", kind)
		}
		raw = uint64(n)
	case "f16", "f32", "f64":
		n, e := strconv.ParseFloat(text, 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("typed float must be finite")
		}
		switch kind {
		case "f64":
			raw = math.Float64bits(n)
		case "f32":
			f := float32(n)
			if math.IsInf(float64(f), 0) {
				return nil, fmt.Errorf("f32 overflow")
			}
			raw = uint64(math.Float32bits(f))
		case "f16":
			bits, e := encodeHalf(n)
			if e != nil {
				return nil, e
			}
			raw = uint64(bits)
		}
	}
	data := make([]byte, width*2)
	for i := len(data) - 1; i >= 0; i-- {
		data[i] = byte(raw)
		raw >>= 8
	}
	data = orderedRegisterBytes(data, order)
	out := make([]any, width)
	for i := range out {
		out[i] = int(binary.BigEndian.Uint16(data[i*2:]))
	}
	return out, nil
}
func encodeHalf(n float64) (uint16, error) {
	sign := uint16(0)
	if math.Signbit(n) {
		sign = 0x8000
	}
	n = math.Abs(n)
	if n == 0 {
		return sign, nil
	}
	if n < math.Ldexp(1, -14) {
		v := math.RoundToEven(math.Ldexp(n, 24))
		return sign | uint16(v), nil
	}
	_, exp := math.Frexp(n)
	exp--
	mantissa := math.RoundToEven(math.Ldexp(n, 10-exp))
	if mantissa == 2048 {
		exp++
		mantissa = 1024
	}
	if exp > 15 {
		return 0, fmt.Errorf("f16 overflow")
	}
	return sign | uint16(exp+15)<<10 | uint16(mantissa-1024), nil
}
func prepareTypedWrite(r config.Request) (config.Request, error) {
	kind, ok := r.Params["value_type"].(string)
	if !ok {
		return r, fmt.Errorf("value_type required")
	}
	values, e := encodeTypedRegisters(kind, r.Params["value"], r.String("word_order", "ABCD"))
	if e != nil {
		return r, e
	}
	address, e := modbusExactParam(r, "address", 0, 65535)
	if e != nil {
		return r, e
	}
	if address+len(values) > 65536 {
		return r, fmt.Errorf("typed value crosses end of register range")
	}
	if _, e = modbusExactParam(r, "unit", 1, 247); e != nil {
		return r, e
	}
	if _, ok := r.Params["count"]; ok {
		count, e := modbusExactParam(r, "count", 1, 4)
		if e != nil || count != len(values) {
			return r, fmt.Errorf("typed count must equal representation width")
		}
	}
	p := map[string]any{}
	for k, v := range r.Params {
		p[k] = v
	}
	p["values"] = values
	p["count"] = len(values)
	r.Params = p
	r.Action = "write-registers"
	return r, nil
}
