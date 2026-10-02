package engine

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/goburrow/modbus"
)

// ModbusRTUSettings describes the line configuration that the user explicitly
// opened on the platform USB host. The platform must reject mismatched settings
// rather than silently reconfigure an already-open device.
type ModbusRTUSettings struct {
	Baud     int
	DataBits int
	StopBits int
	Parity   string
}

// ModbusRTUTransport exchanges one complete RTU ADU on a port already selected,
// permitted and opened by the user. Exchange must honor the timeout (1..5000 ms),
// must not retry writes, and must reject another endpoint or serial settings.
// Close must be safe concurrently with Exchange and interrupt blocked I/O. It is
// called on cancellation/deadline only, not after a successful transaction.
// Android implements this with UsbManager and a userspace USB serial driver;
// desktop serial devices continue using the existing rtu:// transport.
type ModbusRTUTransport interface {
	Exchange(context.Context, string, []byte, ModbusRTUSettings, time.Duration) ([]byte, error)
	Close() error
}

type modbusRTUTransportKey struct{}

// WithModbusRTUTransport provides a platform transport to one execution context.
// It is deliberately not a global registration, so sessions cannot accidentally
// reuse another workspace's device. Installing it performs no device I/O.
func WithModbusRTUTransport(ctx context.Context, transport ModbusRTUTransport) context.Context {
	return context.WithValue(ctx, modbusRTUTransportKey{}, transport)
}

func validateModbusUSBEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "usb" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return fmt.Errorf("USB endpoint must be usb://device-id/port-index selected in Android")
	}
	device, e1 := strconv.ParseUint(u.Host, 10, 31)
	port, e2 := strconv.ParseUint(strings.TrimPrefix(u.Path, "/"), 10, 8)
	if e1 != nil || e2 != nil || u.Host != strconv.FormatUint(device, 10) || u.Path != "/"+strconv.FormatUint(port, 10) {
		return fmt.Errorf("USB endpoint must be usb://device-id/port-index selected in Android")
	}
	return nil
}

func newModbusUSBHandler(ctx context.Context, r config.Request, unit int, timeout time.Duration) (modbus.ClientHandler, error) {
	if err := validateModbusUSBEndpoint(r.Endpoint); err != nil {
		return nil, err
	}
	transport, _ := ctx.Value(modbusRTUTransportKey{}).(ModbusRTUTransport)
	if transport == nil {
		return nil, fmt.Errorf("Android USB transport is unavailable; explicitly select, permit and open an attached USB serial port first")
	}
	settings := ModbusRTUSettings{Baud: r.Int("baud", 9600), DataBits: r.Int("data_bits", 8), StopBits: r.Int("stop_bits", 1), Parity: strings.ToUpper(r.String("parity", "N"))}
	if settings.Baud < 300 || settings.Baud > 4000000 || settings.DataBits != 8 || (settings.StopBits != 1 && settings.StopBits != 2) || (settings.Parity != "N" && settings.Parity != "E" && settings.Parity != "O") {
		return nil, fmt.Errorf("USB RTU requires baud 300..4000000, 8 data bits, 1 or 2 stop bits, and parity N/E/O")
	}
	if timeout <= 0 || timeout > 5*time.Second {
		return nil, fmt.Errorf("USB request_timeout_ms must be 1..5000")
	}
	p := modbus.NewRTUClientHandler("")
	p.SlaveId = byte(unit)
	return &modbusUSBHandler{packager: p, ctx: ctx, endpoint: r.Endpoint, settings: settings, timeout: timeout, transport: transport}, nil
}

type modbusUSBHandler struct {
	packager  *modbus.RTUClientHandler
	ctx       context.Context
	endpoint  string
	settings  ModbusRTUSettings
	timeout   time.Duration
	transport ModbusRTUTransport
}

func (h *modbusUSBHandler) Encode(pdu *modbus.ProtocolDataUnit) ([]byte, error) {
	return h.packager.Encode(pdu)
}
func (h *modbusUSBHandler) Decode(adu []byte) (*modbus.ProtocolDataUnit, error) {
	return h.packager.Decode(adu)
}
func (h *modbusUSBHandler) Verify(request, response []byte) error {
	return h.packager.Verify(request, response)
}
func (h *modbusUSBHandler) Send(request []byte) ([]byte, error) {
	if err := h.ctx.Err(); err != nil {
		return nil, err
	}
	if len(request) < 4 || len(request) > 256 {
		return nil, fmt.Errorf("USB RTU request must contain 4..256 bytes")
	}
	ctx, cancel := context.WithTimeout(h.ctx, h.timeout)
	defer cancel()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = h.transport.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	deadline, _ := ctx.Deadline()
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	response, err := h.transport.Exchange(ctx, h.endpoint, append([]byte(nil), request...), h.settings, timeout)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, err
	}
	if len(response) < 4 || len(response) > 256 {
		return nil, fmt.Errorf("USB RTU response must contain 4..256 bytes")
	}
	return append([]byte(nil), response...), nil
}
