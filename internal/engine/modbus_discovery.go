package engine

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
)

func modbusExactParam(r config.Request, key string, min, max int) (int, error) {
	v, ok := r.Params[key]
	if !ok {
		return 0, fmt.Errorf("explicit %s required", key)
	}
	n, e := exactInt(v)
	if e != nil || n < int64(min) || n > int64(max) {
		return 0, fmt.Errorf("%s must be integer %d..%d", key, min, max)
	}
	return int(n), nil
}
func modbusChildRequest(r config.Request) config.Request {
	p := map[string]any{}
	for k, v := range r.Params {
		switch k {
		case "units", "end_address", "match_value", "pdu_hex", "read_code", "object_id":
			continue
		}
		p[k] = v
	}
	r.Params = p
	r.Params["samples"] = 1
	return r
}

// Explicit bounded probing of one configured endpoint; no IP enumeration or
// automatic scan on startup. Every probe is read-only and cancellation-bound.
func runModbusRange(ctx context.Context, r config.Request, emit Emit) error {
	address, e := modbusExactParam(r, "address", 0, 65535)
	if e != nil {
		return e
	}
	if r.Action == "scan-units" {
		units, ok := r.Params["units"].([]any)
		if !ok || len(units) < 1 || len(units) > 32 {
			return fmt.Errorf("units must explicitly list 1..32 unit IDs")
		}
		ids := make([]int, len(units))
		seen := map[int]bool{}
		for i, v := range units {
			n, e := exactInt(v)
			if e != nil || n < 1 || n > 247 || seen[int(n)] {
				return fmt.Errorf("units must be unique integers 1..247")
			}
			ids[i] = int(n)
			seen[int(n)] = true
		}
		for _, unit := range ids {
			if e := ctx.Err(); e != nil {
				return e
			}
			child := modbusChildRequest(r)
			child.Action = "read-holding"
			child.Params["unit"] = unit
			child.Params["address"] = address
			child.Params["count"] = 1
			probe, cancel := context.WithTimeout(ctx, time.Second)
			err := runModbus(probe, child, nil)
			cancel()
			result := map[string]any{"unit": unit, "responsive": err == nil, "simulated": r.Endpoint == "mock://local"}
			if err != nil {
				result["error"] = err.Error()
			}
			send(emit, "unit-probe", result)
		}
		return ctx.Err()
	}
	if _, e = modbusExactParam(r, "unit", 1, 247); e != nil {
		return e
	}
	end, e := modbusExactParam(r, "end_address", address, 65535)
	if e != nil {
		return e
	}
	if end-address+1 > 16000 {
		return fmt.Errorf("sweep/search range limited to 16000 registers")
	}
	match := 0
	if r.Action == "search-holding" {
		match, e = modbusExactParam(r, "match_value", 0, 65535)
		if e != nil {
			return e
		}
	}
	chunk := r.Int("count", 125)
	for start := address; start <= end; start += chunk {
		if e = ctx.Err(); e != nil {
			return e
		}
		child := modbusChildRequest(r)
		child.Action = "read-holding"
		child.Params["address"] = start
		count := chunk
		if end-start+1 < count {
			count = end - start + 1
		}
		child.Params["count"] = count
		e = runModbus(ctx, child, func(event Event) {
			if r.Action != "search-holding" {
				if emit != nil {
					emit(event)
				}
				return
			}
			if event.Kind == "registers" {
				for _, row := range event.Data.([]map[string]any) {
					if row["u16"] == uint16(match) {
						send(emit, "register-match", row)
					}
				}
			} else if event.Kind == "simulation" && emit != nil {
				emit(event)
			}
		})
		if e != nil {
			return e
		}
		send(emit, "sweep-progress", map[string]any{"through": start + count - 1, "end_address": end, "simulated": r.Endpoint == "mock://local"})
	}
	return nil
}

func exchangeModbusPDU(h modbus.ClientHandler, pdu []byte) ([]byte, error) {
	if len(pdu) < 1 || len(pdu) > 253 {
		return nil, fmt.Errorf("PDU must contain 1..253 bytes")
	}
	request, e := h.Encode(&modbus.ProtocolDataUnit{FunctionCode: pdu[0], Data: pdu[1:]})
	if e != nil {
		return nil, e
	}
	response, e := h.Send(request)
	if e != nil {
		return nil, e
	}
	if e = h.Verify(request, response); e != nil {
		return nil, e
	}
	decoded, e := h.Decode(response)
	if e != nil {
		return nil, e
	}
	if decoded.FunctionCode == pdu[0]|0x80 {
		if len(decoded.Data) != 1 {
			return nil, fmt.Errorf("malformed Modbus exception")
		}
		return nil, fmt.Errorf("Modbus exception %d", decoded.Data[0])
	}
	if decoded.FunctionCode != pdu[0] {
		return nil, fmt.Errorf("response function mismatch")
	}
	return append([]byte{decoded.FunctionCode}, decoded.Data...), nil
}
func validateRawPDU(r config.Request) ([]byte, error) {
	s, ok := r.Params["pdu_hex"].(string)
	if !ok {
		return nil, fmt.Errorf("pdu_hex must be a hex string including function code")
	}
	p, e := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if e != nil || len(p) < 1 || len(p) > 253 {
		return nil, fmt.Errorf("PDU must be 1..253 hex bytes")
	}
	fc := p[0]
	if r.Action == "read-raw" {
		if fc == 43 {
			if len(p) != 4 || p[1] != 14 || p[2] < 1 || p[2] > 4 {
				return nil, fmt.Errorf("read-raw FC43 only supports MEI14 read-code1..4")
			}
			return p, nil
		}
		if fc < 1 || fc > 4 || len(p) != 5 {
			return nil, fmt.Errorf("read-raw only permits FC1/2/3/4 and FC43/14")
		}
		a, n := int(binary.BigEndian.Uint16(p[1:3])), int(binary.BigEndian.Uint16(p[3:5]))
		if n < 1 || n > 125 || a+n > 65536 {
			return nil, fmt.Errorf("raw read range invalid")
		}
		return p, nil
	}
	if r.Action != "write-raw" {
		return nil, fmt.Errorf("invalid raw action")
	}
	address, e := modbusExactParam(r, "address", 0, 65535)
	if e != nil {
		return nil, e
	}
	count, e := modbusExactParam(r, "count", 1, 125)
	if e != nil {
		return nil, e
	}
	if len(p) < 5 || int(binary.BigEndian.Uint16(p[1:3])) != address || address+count > 65536 {
		return nil, fmt.Errorf("raw write address must match explicit scope")
	}
	switch fc {
	case 5, 6:
		if len(p) != 5 || count != 1 {
			return nil, fmt.Errorf("single raw write requires count1 and 5-byte PDU")
		}
		if fc == 5 {
			v := binary.BigEndian.Uint16(p[3:5])
			if v != 0 && v != 0xff00 {
				return nil, fmt.Errorf("raw coil must be 0000 or FF00")
			}
		}
	case 15, 16:
		if len(p) < 6 || int(binary.BigEndian.Uint16(p[3:5])) != count {
			return nil, fmt.Errorf("raw write quantity must match count")
		}
		n := (count + 7) / 8
		if fc == 16 {
			if count > 123 {
				return nil, fmt.Errorf("raw register write maximum123")
			}
			n = count * 2
		}
		if int(p[5]) != n || len(p) != 6+n {
			return nil, fmt.Errorf("raw write payload length mismatch")
		}
		if fc == 15 && count%8 != 0 && p[len(p)-1]>>uint(count%8) != 0 {
			return nil, fmt.Errorf("unused coil bits must be zero")
		}
	default:
		return nil, fmt.Errorf("write-raw only permits bounded FC5/6/15/16; unknown functions cannot bypass safety scope")
	}
	return p, nil
}
func runModbusRaw(r config.Request, h modbus.ClientHandler, emit Emit) error {
	p, e := validateRawPDU(r)
	if e != nil {
		return e
	}
	reply, e := exchangeModbusPDU(h, p)
	if e != nil {
		return e
	}
	if r.Action == "read-raw" && p[0] <= 4 {
		expected := (int(binary.BigEndian.Uint16(p[3:5])) + 7) / 8
		if p[0] >= 3 {
			expected = int(binary.BigEndian.Uint16(p[3:5])) * 2
		}
		if len(reply) != expected+2 || int(reply[1]) != expected {
			return fmt.Errorf("raw response byte count mismatch")
		}
	} else if r.Action == "write-raw" {
		if len(reply) != 5 || !bytesEqual(reply[1:5], p[1:5]) {
			return fmt.Errorf("raw write acknowledgement mismatch")
		}
	}
	send(emit, "raw-pdu", map[string]any{"request_hex": hex.EncodeToString(p), "response_hex": hex.EncodeToString(reply), "simulated": r.Endpoint == "mock://local"})
	return nil
}
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func runModbusDeviceID(ctx context.Context, r config.Request, h modbus.ClientHandler, emit Emit) error {
	code, object := 1, 0
	var e error
	if _, ok := r.Params["read_code"]; ok {
		code, e = modbusExactParam(r, "read_code", 1, 4)
		if e != nil {
			return e
		}
	}
	if _, ok := r.Params["object_id"]; ok {
		object, e = modbusExactParam(r, "object_id", 0, 255)
		if e != nil {
			return e
		}
	}
	visited := map[int]bool{}
	for page := 0; page < 256; page++ {
		if e = ctx.Err(); e != nil {
			return e
		}
		if visited[object] {
			return fmt.Errorf("device ID pagination loop")
		}
		visited[object] = true
		reply, e := exchangeModbusPDU(h, []byte{43, 14, byte(code), byte(object)})
		if e != nil {
			return e
		}
		if len(reply) < 7 || reply[1] != 14 || int(reply[2]) != code || (reply[4] != 0 && reply[4] != 255) {
			return fmt.Errorf("malformed device ID response")
		}
		offset := 7
		objects := map[int]string{}
		for i := 0; i < int(reply[6]); i++ {
			if offset+2 > len(reply) {
				return fmt.Errorf("truncated device ID object")
			}
			id, n := int(reply[offset]), int(reply[offset+1])
			offset += 2
			if offset+n > len(reply) {
				return fmt.Errorf("truncated device ID string")
			}
			if _, ok := objects[id]; ok {
				return fmt.Errorf("duplicate device ID object")
			}
			objects[id] = string(reply[offset : offset+n])
			offset += n
		}
		if offset != len(reply) {
			return fmt.Errorf("extra device ID bytes")
		}
		send(emit, "device-identification", map[string]any{"unit": r.Int("unit", 1), "conformity": reply[3], "objects": objects, "simulated": r.Endpoint == "mock://local"})
		if reply[4] == 0 {
			return nil
		}
		object = int(reply[5])
	}
	return fmt.Errorf("device ID page limit exceeded")
}
