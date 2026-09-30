package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/goburrow/modbus"
	"io"
	"net"
	"time"
)

// TCP requests use a context-bound connection per transaction: cancelling a
// read never leaves a stale response that could be consumed by a later write.
type tcpContextHandler struct {
	packager *modbus.TCPClientHandler
	address  string
	ctx      context.Context
	timeout  time.Duration
}

func (h *tcpContextHandler) Encode(p *modbus.ProtocolDataUnit) ([]byte, error) {
	return h.packager.Encode(p)
}
func (h *tcpContextHandler) Decode(b []byte) (*modbus.ProtocolDataUnit, error) {
	return h.packager.Decode(b)
}
func (h *tcpContextHandler) Verify(a, b []byte) error { return h.packager.Verify(a, b) }
func (h *tcpContextHandler) Send(request []byte) ([]byte, error) {
	c, e := (&net.Dialer{Timeout: h.timeout}).DialContext(h.ctx, "tcp", h.address)
	if e != nil {
		return nil, e
	}
	defer c.Close()
	stop := context.AfterFunc(h.ctx, func() { _ = c.Close() })
	defer stop()
	deadline := time.Now().Add(h.timeout)
	if d, ok := h.ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if e = c.SetDeadline(deadline); e != nil {
		return nil, e
	}
	for off := 0; off < len(request); {
		n, e := c.Write(request[off:])
		if e != nil {
			return nil, e
		}
		if n == 0 {
			return nil, io.ErrShortWrite
		}
		off += n
	}
	header := make([]byte, 7)
	if _, e = io.ReadFull(c, header); e != nil {
		return nil, e
	}
	length := int(binary.BigEndian.Uint16(header[4:6]))
	if length < 2 || length > 254 {
		return nil, fmt.Errorf("invalid Modbus TCP length")
	}
	body := make([]byte, length-1)
	if _, e = io.ReadFull(c, body); e != nil {
		return nil, e
	}
	return append(header, body...), nil
}
