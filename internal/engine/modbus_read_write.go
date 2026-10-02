package engine

import (
	"encoding/binary"
	"fmt"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
)

func validateModbusReadWrite(r config.Request) (readAddress, readCount int, words []uint16, err error) {
	readAddress, err = modbusExactParam(r, "read_address", 0, 65535)
	if err != nil {
		return
	}
	readCount, err = modbusExactParam(r, "read_count", 1, 125)
	if err != nil {
		return
	}
	address, e := modbusExactParam(r, "address", 0, 65535)
	if e != nil {
		err = e
		return
	}
	if _, e = modbusExactParam(r, "unit", 1, 247); e != nil {
		err = e
		return
	}
	values, ok := r.Params["values"].([]any)
	if !ok || len(values) < 1 || len(values) > 121 || address+len(values) > 65536 || readAddress+readCount > 65536 {
		err = fmt.Errorf("FC23 requires explicit bounded read range and 1..121 write words")
		return
	}
	if _, ok := r.Params["count"]; ok {
		count, e := modbusExactParam(r, "count", 1, 121)
		if e != nil || count != len(values) {
			err = fmt.Errorf("FC23 count must equal exact write word count")
			return
		}
	}
	words = make([]uint16, len(values))
	for i, v := range values {
		n, e := exactInt(v)
		if e != nil || n < 0 || n > 65535 {
			err = fmt.Errorf("FC23 write words must be exact integers 0..65535")
			return
		}
		words[i] = uint16(n)
	}
	return
}

// One FC23 transaction, with no secondary read and no retry. The reply is
// checked before indexing; malformed/short responses are never accepted as a
// successful write (the write may nevertheless have reached the device).
func runModbusReadWrite(r config.Request, h modbus.ClientHandler, emit Emit) error {
	readAddress, readCount, words, err := validateModbusReadWrite(r)
	if err != nil {
		return err
	}
	data := make([]byte, 9+2*len(words))
	binary.BigEndian.PutUint16(data, uint16(readAddress))
	binary.BigEndian.PutUint16(data[2:], uint16(readCount))
	binary.BigEndian.PutUint16(data[4:], uint16(r.Int("address", 0)))
	binary.BigEndian.PutUint16(data[6:], uint16(len(words)))
	data[8] = byte(len(words) * 2)
	for i, w := range words {
		binary.BigEndian.PutUint16(data[9+2*i:], w)
	}
	request, err := h.Encode(&modbus.ProtocolDataUnit{FunctionCode: 23, Data: data})
	if err != nil {
		return err
	}
	response, err := h.Send(request)
	if err != nil {
		return err
	}
	if err = h.Verify(request, response); err != nil {
		return err
	}
	pdu, err := h.Decode(response)
	if err != nil {
		return err
	}
	if pdu.FunctionCode != 23 {
		return fmt.Errorf("FC23 unexpected/exception function 0x%02x (write outcome may be unknown)", pdu.FunctionCode)
	}
	if len(pdu.Data) != 1+2*readCount || int(pdu.Data[0]) != 2*readCount {
		return fmt.Errorf("FC23 malformed readback (write outcome may be unknown; do not retry automatically)")
	}
	rows := decodeRegisters(pdu.Data[1:], readAddress, r.String("word_order", "ABCD"))
	matching := any(nil)
	if readAddress == r.Int("address", 0) && readCount == len(words) {
		same := true
		for i, w := range words {
			if rows[i]["u16"].(uint16) != w {
				same = false
			}
		}
		matching = same
	}
	send(emit, "readback", map[string]any{"function": 23, "unit": r.Int("unit", 1), "write_address": r.Int("address", 0), "write_count": len(words), "read_address": readAddress, "read_count": readCount, "matches_written": matching, "message": "FC23 response; no additional read or retry"})
	send(emit, "registers", rows)
	return nil
}
