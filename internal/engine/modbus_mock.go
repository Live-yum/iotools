package engine

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/goburrow/modbus"
)

// The explicitly selected local simulator uses no socket, serial port or file.
// Writes live only for this process, with independent unit/register spaces.
var localModbusMock = struct {
	sync.Mutex
	registers map[uint32]uint16
	coils     map[uint32]bool
}{registers: map[uint32]uint16{}, coils: map[uint32]bool{}}

const maxMockChanges = 262144

type mockModbusHandler struct{ packager *modbus.TCPClientHandler }

func (h *mockModbusHandler) Encode(pdu *modbus.ProtocolDataUnit) ([]byte, error) {
	return h.packager.Encode(pdu)
}
func (h *mockModbusHandler) Decode(adu []byte) (*modbus.ProtocolDataUnit, error) {
	return h.packager.Decode(adu)
}
func (h *mockModbusHandler) Verify(request, response []byte) error {
	return h.packager.Verify(request, response)
}
func (h *mockModbusHandler) Send(request []byte) ([]byte, error) {
	if len(request) >= 8 && request[7] == 43 {
		if len(request) != 11 || request[8] != 14 || request[9] < 1 || request[9] > 4 {
			return nil, fmt.Errorf("invalid mock device identification request")
		}
		objects := []string{"iotools", "Explicit local simulator", "1"}
		response := []byte{43, 14, request[9], 1, 0, 0, 0}
		for id, text := range objects {
			if id < int(request[10]) {
				continue
			}
			if request[9] == 4 && id != int(request[10]) {
				continue
			}
			response = append(response, byte(id), byte(len(text)))
			response = append(response, []byte(text)...)
			response[6]++
		}
		header := append([]byte(nil), request[:7]...)
		binary.BigEndian.PutUint16(header[4:6], uint16(len(response)+1))
		return append(header, response...), nil
	}
	if len(request) < 12 {
		return nil, fmt.Errorf("invalid simulator request")
	}
	unit, fc := request[6], request[7]
	address := int(binary.BigEndian.Uint16(request[8:10]))
	arg := int(binary.BigEndian.Uint16(request[10:12]))
	key := func(a int) uint32 { return uint32(unit)<<16 | uint32(a) }
	localModbusMock.Lock()
	defer localModbusMock.Unlock()
	response := []byte{fc}
	switch fc {
	case 1, 2, 3, 4:
		if arg < 1 || arg > 125 || address+arg > 65536 {
			return nil, fmt.Errorf("invalid simulator read range")
		}
		if fc == 3 || fc == 4 {
			response = append(response, byte(arg*2))
			for a := address; a < address+arg; a++ {
				v := uint16(a)
				if fc == 3 {
					if stored, ok := localModbusMock.registers[key(a)]; ok {
						v = stored
					}
				}
				response = append(response, byte(v>>8), byte(v))
			}
		} else {
			n := (arg + 7) / 8
			response = append(response, byte(n))
			response = append(response, make([]byte, n)...)
			for i := 0; i < arg; i++ {
				v := (address+i)%2 == 1
				if fc == 1 {
					if stored, ok := localModbusMock.coils[key(address+i)]; ok {
						v = stored
					}
				}
				if v {
					response[2+i/8] |= 1 << uint(i%8)
				}
			}
		}
	case 5, 6, 15, 16:
		count := 1
		var words []uint16
		var bits []bool
		if fc == 5 {
			if arg != 0 && arg != 0xff00 {
				return nil, fmt.Errorf("invalid simulator coil")
			}
			bits = []bool{arg != 0}
		}
		if fc == 6 {
			words = []uint16{uint16(arg)}
		}
		if fc == 15 || fc == 16 {
			count = arg
			if count < 1 || address+count > 65536 || len(request) < 13 {
				return nil, fmt.Errorf("invalid simulator write range")
			}
			bytesRequired := (count + 7) / 8
			if fc == 16 {
				if count > 123 {
					return nil, fmt.Errorf("too many simulator registers")
				}
				bytesRequired = count * 2
			} else if count > 1968 {
				return nil, fmt.Errorf("too many simulator coils")
			}
			if int(request[12]) != bytesRequired || len(request) != 13+bytesRequired {
				return nil, fmt.Errorf("invalid simulator write payload")
			}
			if fc == 16 {
				words = make([]uint16, count)
				for i := range words {
					words[i] = binary.BigEndian.Uint16(request[13+i*2:])
				}
			} else {
				bits = make([]bool, count)
				for i := range bits {
					bits[i] = request[13+i/8]&(1<<uint(i%8)) != 0
				}
			}
		}
		additions := 0
		for i := 0; i < count; i++ {
			if len(words) > 0 {
				if _, ok := localModbusMock.registers[key(address+i)]; !ok {
					additions++
				}
			} else {
				if _, ok := localModbusMock.coils[key(address+i)]; !ok {
					additions++
				}
			}
		}
		if len(localModbusMock.registers)+len(localModbusMock.coils)+additions > maxMockChanges {
			return nil, fmt.Errorf("simulator capacity reached; restart process to clear")
		}
		for i, v := range words {
			localModbusMock.registers[key(address+i)] = v
		}
		for i, v := range bits {
			localModbusMock.coils[key(address+i)] = v
		}
		response = append(response, request[8:12]...)
	default:
		return nil, fmt.Errorf("unsupported simulator function %d", fc)
	}
	header := append([]byte(nil), request[:7]...)
	binary.BigEndian.PutUint16(header[4:6], uint16(len(response)+1))
	return append(header, response...), nil
}
