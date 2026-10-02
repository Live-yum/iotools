package engine

// MessagePack decoding is an independent implementation of the public binary
// format. Resource bounds are checked before allocations; no external runtime.
import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"
)

const mqttMessagePackNodes = 16384
const mqttMessagePackDepth = 32

type mqttMessagePackDecoder struct {
	data      []byte
	at, nodes int
}

func mqttDecodeMessagePack(data []byte) (any, error) {
	if len(data) == 0 || len(data) > maxBody {
		return nil, fmt.Errorf("MessagePack payload length outside1..4MiB")
	}
	d := mqttMessagePackDecoder{data: data}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if d.at != len(data) {
		return nil, fmt.Errorf("MessagePack trailing data")
	}
	return v, nil
}
func (d *mqttMessagePackDecoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.data)-d.at {
		return nil, fmt.Errorf("truncated MessagePack")
	}
	b := d.data[d.at : d.at+n]
	d.at += n
	return b, nil
}
func (d *mqttMessagePackDecoder) number(n int) (uint64, error) {
	b, e := d.take(n)
	if e != nil {
		return 0, e
	}
	switch n {
	case 1:
		return uint64(b[0]), nil
	case 2:
		return uint64(binary.BigEndian.Uint16(b)), nil
	case 4:
		return uint64(binary.BigEndian.Uint32(b)), nil
	case 8:
		return binary.BigEndian.Uint64(b), nil
	}
	return 0, fmt.Errorf("invalid MessagePack number")
}
func (d *mqttMessagePackDecoder) bytes(n int, text bool) (any, error) {
	b, e := d.take(n)
	if e != nil {
		return nil, e
	}
	if text && utf8.Valid(b) {
		return string(b), nil
	}
	kind := "binary"
	if text {
		kind = "invalid-utf8-string"
	}
	return map[string]any{"messagepack_type": kind, "base64": base64.StdEncoding.EncodeToString(b)}, nil
}
func (d *mqttMessagePackDecoder) collection(n, depth int, isMap bool) (any, error) {
	nodes := n
	if isMap {
		if n > mqttMessagePackNodes/2 {
			return nil, fmt.Errorf("MessagePack map exceeds node limit")
		}
		nodes = n * 2
	}
	if n < 0 || nodes > mqttMessagePackNodes-d.nodes || nodes > len(d.data)-d.at {
		return nil, fmt.Errorf("MessagePack collection exceeds bounds")
	}
	if !isMap {
		out := make([]any, n)
		for i := range out {
			v, e := d.value(depth + 1)
			if e != nil {
				return nil, e
			}
			out[i] = v
		}
		return out, nil
	}
	out := make(map[string]any, n)
	for i := 0; i < n; i++ {
		key, e := d.value(depth + 1)
		if e != nil {
			return nil, e
		}
		var name string
		switch k := key.(type) {
		case string:
			name = k
		case json.Number:
			name = string(k)
		case bool:
			name = strconv.FormatBool(k)
		case nil:
			name = "null"
		default:
			return nil, fmt.Errorf("MessagePack composite map keys unsupported")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("duplicate MessagePack display key")
		}
		v, e := d.value(depth + 1)
		if e != nil {
			return nil, e
		}
		out[name] = v
	}
	return out, nil
}
func (d *mqttMessagePackDecoder) extension(n int) (any, error) {
	tag, e := d.number(1)
	if e != nil {
		return nil, e
	}
	b, e := d.take(n)
	if e != nil {
		return nil, e
	}
	return map[string]any{"messagepack_type": "extension", "extension_type": int8(tag), "base64": base64.StdEncoding.EncodeToString(b)}, nil
}
func (d *mqttMessagePackDecoder) value(depth int) (any, error) {
	if depth > mqttMessagePackDepth || d.nodes >= mqttMessagePackNodes {
		return nil, fmt.Errorf("MessagePack depth/node limit")
	}
	d.nodes++
	marker, e := d.number(1)
	if e != nil {
		return nil, e
	}
	m := byte(marker)
	if m < 0x80 {
		return json.Number(strconv.FormatUint(marker, 10)), nil
	}
	if m >= 0xe0 {
		return json.Number(strconv.FormatInt(int64(int8(m)), 10)), nil
	}
	if m >= 0xa0 && m <= 0xbf {
		return d.bytes(int(m&31), true)
	}
	if m >= 0x90 && m <= 0x9f {
		return d.collection(int(m&15), depth, false)
	}
	if m >= 0x80 && m <= 0x8f {
		return d.collection(int(m&15), depth, true)
	}
	switch m {
	case 0xc0:
		return nil, nil
	case 0xc2:
		return false, nil
	case 0xc3:
		return true, nil
	case 0xca, 0xcb:
		width := 4
		if m == 0xcb {
			width = 8
		}
		bits, e := d.number(width)
		if e != nil {
			return nil, e
		}
		var f float64
		if width == 4 {
			f = float64(math.Float32frombits(uint32(bits)))
		} else {
			f = math.Float64frombits(bits)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return map[string]any{"messagepack_type": "non-finite-float", "value": strconv.FormatFloat(f, 'g', -1, 64)}, nil
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	case 0xcc, 0xcd, 0xce, 0xcf:
		n := 1 << uint(m-0xcc)
		value, e := d.number(n)
		if e != nil {
			return nil, e
		}
		return json.Number(strconv.FormatUint(value, 10)), nil
	case 0xd0, 0xd1, 0xd2, 0xd3:
		n := 1 << uint(m-0xd0)
		value, e := d.number(n)
		if e != nil {
			return nil, e
		}
		var signed int64
		switch n {
		case 1:
			signed = int64(int8(value))
		case 2:
			signed = int64(int16(value))
		case 4:
			signed = int64(int32(value))
		case 8:
			signed = int64(value)
		}
		return json.Number(strconv.FormatInt(signed, 10)), nil
	case 0xc4, 0xc5, 0xc6, 0xd9, 0xda, 0xdb:
		base := byte(0xc4)
		text := false
		if m >= 0xd9 {
			base = 0xd9
			text = true
		}
		length, e := d.number(1 << uint(m-base))
		if e != nil || length > uint64(len(d.data)-d.at) {
			return nil, fmt.Errorf("MessagePack string/binary length invalid")
		}
		return d.bytes(int(length), text)
	case 0xdc, 0xdd, 0xde, 0xdf:
		width := 2
		if m == 0xdd || m == 0xdf {
			width = 4
		}
		n, e := d.number(width)
		if e != nil || n > mqttMessagePackNodes {
			return nil, fmt.Errorf("MessagePack collection length invalid")
		}
		return d.collection(int(n), depth, m >= 0xde)
	case 0xd4, 0xd5, 0xd6, 0xd7, 0xd8:
		return d.extension(1 << uint(m-0xd4))
	case 0xc7, 0xc8, 0xc9:
		n, e := d.number(1 << uint(m-0xc7))
		if e != nil || n > uint64(len(d.data)-d.at) {
			return nil, fmt.Errorf("MessagePack extension length invalid")
		}
		return d.extension(int(n))
	default:
		return nil, fmt.Errorf("reserved MessagePack marker")
	}
}
func mqttPayloadMetadata(result map[string]any, payload []byte) {
	result["received_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if _, ok := result["payload_json"]; ok {
		result["payload_format"] = "json"
		return
	}
	if utf8.Valid(payload) {
		result["payload_format"] = "text"
		return
	}
	result["payload_format"] = "binary"
	if value, err := mqttDecodeMessagePack(payload); err == nil {
		result["payload_format"] = "messagepack"
		result["payload_messagepack"] = value
	}
}
