package engine

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

// orderedRegisterBytes applies byte swaps within words, then reverses the word
// sequence for little-word order. One-word types never change byte order.
func orderedRegisterBytes(data []byte, order string) []byte {
	out := append([]byte(nil), data...)
	if len(out) == 2 {
		return out
	}
	if order == "BADC" || order == "DCBA" {
		for i := 0; i+1 < len(out); i += 2 {
			out[i], out[i+1] = out[i+1], out[i]
		}
	}
	if order == "CDAB" || order == "DCBA" {
		for i, j := 0, len(out)-2; i < j; i, j = i+2, j-2 {
			out[i], out[j] = out[j], out[i]
			out[i+1], out[j+1] = out[j+1], out[i+1]
		}
	}
	return out
}
func halfFloat(v uint16) float64 {
	sign := 1.0
	if v&0x8000 != 0 {
		sign = -1
	}
	exp, fraction := int((v>>10)&31), float64(v&1023)
	if exp == 31 {
		if fraction != 0 {
			return math.NaN()
		}
		return math.Inf(int(sign))
	}
	if exp == 0 {
		return sign * math.Ldexp(fraction, -24)
	}
	return sign * math.Ldexp(1+fraction/1024, exp-15)
}
func decimalBCD(v uint64, digits int) (uint64, bool) {
	var out uint64
	for i := digits - 1; i >= 0; i-- {
		n := (v >> uint(4*i)) & 15
		if n > 9 {
			return 0, false
		}
		out = out*10 + n
	}
	return out, true
}
func floatText(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
func decodeRegisters(data []byte, address int, order string) []map[string]any {
	rows := make([]map[string]any, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		v := binary.BigEndian.Uint16(data[i:])
		ascii := []byte{byte(v >> 8), byte(v)}
		for j, b := range ascii {
			if b < 33 || b > 126 {
				ascii[j] = '.'
			}
		}
		row := map[string]any{"address": address + i/2, "u16": v, "i16": int16(v), "u8": fmt.Sprintf("%d/%d", byte(v>>8), byte(v)), "i8": fmt.Sprintf("%d/%d", int8(v>>8), int8(v)), "hex": fmt.Sprintf("0x%04X", v), "binary": fmt.Sprintf("%016b", v), "ascii": string(ascii), "f16": floatText(halfFloat(v))}
		if b, ok := decimalBCD(uint64(v), 4); ok {
			row["bcd"] = b
		}
		if i+4 <= len(data) {
			b := orderedRegisterBytes(data[i:i+4], order)
			x := binary.BigEndian.Uint32(b)
			row["u32"] = x
			row["i32"] = int32(x)
			row["f32"] = strconv.FormatFloat(float64(math.Float32frombits(x)), 'g', -1, 32)
			row["hex32"] = fmt.Sprintf("0x%08X", x)
			row["u32_m10k"] = fmt.Sprintf("%d/%d", uint16(x>>16), uint16(x))
			row["i32_m10k"] = fmt.Sprintf("%d/%d", int16(x>>16), int16(x))
			if b, ok := decimalBCD(uint64(x), 8); ok {
				row["bcd32"] = b
			}
		}
		if i+8 <= len(data) {
			b := orderedRegisterBytes(data[i:i+8], order)
			x := binary.BigEndian.Uint64(b)
			row["u64"] = strconv.FormatUint(x, 10)
			row["i64"] = strconv.FormatInt(int64(x), 10)
			row["f64"] = floatText(math.Float64frombits(x))
		}
		rows = append(rows, row)
	}
	return rows
}
