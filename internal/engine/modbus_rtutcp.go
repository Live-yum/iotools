package engine

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/goburrow/modbus"
)

// RTU-over-TCP carries RTU ADUs, including CRC, without an MBAP header.
// A separate implementation avoids accidentally using the TCP/MBAP transporter.
type rtuTCPHandler struct {
	packager *modbus.RTUClientHandler
	address  string
	ctx      context.Context
	timeout  time.Duration
}

func (h *rtuTCPHandler) Encode(pdu *modbus.ProtocolDataUnit) ([]byte, error) {
	return h.packager.Encode(pdu)
}
func (h *rtuTCPHandler) Decode(adu []byte) (*modbus.ProtocolDataUnit, error) {
	return h.packager.Decode(adu)
}
func (h *rtuTCPHandler) Verify(request, response []byte) error {
	return h.packager.Verify(request, response)
}
func (h *rtuTCPHandler) Send(request []byte) ([]byte, error) {
	d := net.Dialer{Timeout: h.timeout}
	c, e := d.DialContext(h.ctx, "tcp", h.address)
	if e != nil {
		return nil, e
	}
	defer c.Close()
	stop := context.AfterFunc(h.ctx, func() { c.Close() })
	defer stop()
	deadline := time.Now().Add(h.timeout)
	if ctxDeadline, ok := h.ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if e = c.SetDeadline(deadline); e != nil {
		return nil, e
	}
	for sent := 0; sent < len(request); {
		n, e := c.Write(request[sent:])
		if e != nil {
			return nil, e
		}
		sent += n
	}
	prefix := make([]byte, 3)
	if _, e = io.ReadFull(c, prefix); e != nil {
		return nil, e
	}

	if prefix[1] == 43 {
		if prefix[2] != 14 {
			return nil, fmt.Errorf("unsupported MEI response")
		}
		header := make([]byte, 5)
		if _, e = io.ReadFull(c, header); e != nil {
			return nil, e
		}
		response := append(prefix, header...)
		for i := 0; i < int(header[4]); i++ {
			field := make([]byte, 2)
			if _, e = io.ReadFull(c, field); e != nil {
				return nil, e
			}
			if len(response)+2+int(field[1])+2 > 256 {
				return nil, fmt.Errorf("oversized RTU device identification")
			}
			value := make([]byte, int(field[1]))
			if _, e = io.ReadFull(c, value); e != nil {
				return nil, e
			}
			response = append(response, field...)
			response = append(response, value...)
		}
		crc := make([]byte, 2)
		if _, e = io.ReadFull(c, crc); e != nil {
			return nil, e
		}
		return append(response, crc...), nil
	}
	remaining := 0
	if prefix[1]&0x80 != 0 {
		remaining = 2
	} else {
		switch prefix[1] {
		case 1, 2, 3, 4:
			remaining = int(prefix[2]) + 2
			if remaining > 252 {
				return nil, fmt.Errorf("invalid RTU response length")
			}
		case 5, 6, 15, 16:
			remaining = 5
		default:
			return nil, fmt.Errorf("unsupported RTU response function %d", prefix[1])
		}
	}
	tail := make([]byte, remaining)
	if _, e = io.ReadFull(c, tail); e != nil {
		return nil, e
	}
	return append(prefix, tail...), nil
}
