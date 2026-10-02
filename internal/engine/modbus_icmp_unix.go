//go:build linux || darwin

package engine

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/icmp"
	"net"
	"runtime"
	"syscall"
	"time"
)

func probeModbusPing(ctx context.Context, target string) error {
	return probeModbusPingDatagram(ctx, target, func() (net.PacketConn, error) { return icmp.ListenPacket("udp4", "0.0.0.0") }, runtime.GOOS == "linux")
}
func probeModbusPingDatagram(ctx context.Context, target string, open func() (net.PacketConn, error), kernelID bool) error {
	deadline, err := modbusPingDeadline(ctx)
	if err != nil {
		return err
	}
	nonce, id, seq, err := newModbusPingPayload()
	if err != nil {
		return err
	}
	c, err := open()
	if err != nil {
		return modbusICMPDatagramError(err)
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	if err = c.SetDeadline(deadline); err != nil {
		return err
	}
	// Linux ping sockets replace the echo ID with their bound local port.
	if kernelID {
		local, ok := c.LocalAddr().(*net.UDPAddr)
		if !ok || local.Port < 1 || local.Port > 65535 {
			return fmt.Errorf("无法确认内核ICMP标识；拒绝未关联回复")
		}
		id = uint16(local.Port)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	packet := modbusPingRequest(id, seq, nonce)
	n, err := c.WriteTo(packet, &net.UDPAddr{IP: net.ParseIP(target)})
	if err != nil {
		return modbusICMPDatagramError(err)
	}
	if n != len(packet) {
		return fmt.Errorf("ICMP数据报未完整发送")
	}
	buffer := make([]byte, 512)
	for attempts := 0; attempts < 256; attempts++ {
		n, peer, e := c.ReadFrom(buffer)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			return modbusICMPDatagramError(e)
		}
		if validModbusPingReply(buffer[:n], peer, target, id, seq, nonce) {
			return nil
		}
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
	}
	return fmt.Errorf("收到过多不匹配的ICMP回复；未判定可达")
}

func modbusICMPDatagramError(err error) error {
	for _, cause := range []error{syscall.EPERM, syscall.EACCES, syscall.EAFNOSUPPORT, syscall.EPROTONOSUPPORT, syscall.ESOCKTNOSUPPORT, syscall.ENOPROTOOPT, syscall.ENOSYS} {
		if errors.Is(err, cause) {
			return fmt.Errorf("%w: %v", ErrModbusICMPUnavailable, err)
		}
	}
	return err
}
