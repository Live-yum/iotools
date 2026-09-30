package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

var ErrModbusICMPUnavailable = errors.New("非特权ICMP Ping不可用；未提权或切换原始套接字")

func PrepareModbusDiscoveryMethod(target string, port, timeout, concurrency int, method string) (ModbusDiscoveryPlan, error) {
	p, err := PrepareModbusDiscovery(target, port, timeout, concurrency)
	if err != nil {
		return p, err
	}
	if method != "tcp" && method != "ping" {
		return p, fmt.Errorf("发现方式必须为tcp或ping")
	}
	p.Method = method
	if method == "ping" {
		for _, target := range p.Targets {
			a, _ := netip.ParseAddr(target)
			if !a.Is4() {
				return p, fmt.Errorf("ICMP Ping仅支持明确IPv4目标；IPv6可用TCP方式")
			}
		}
	}
	return p, nil
}
func DiscoverModbusNetwork(ctx context.Context, p ModbusDiscoveryPlan, emit func(ModbusDiscoveryResult)) error {
	if p.Method == "ping" {
		return discoverModbusPing(ctx, p, probeModbusPing, emit)
	}
	return DiscoverModbusTCP(ctx, p, emit)
}
func discoverModbusPing(ctx context.Context, p ModbusDiscoveryPlan, probe func(context.Context, string) error, emit func(ModbusDiscoveryResult)) error {
	checked, err := PrepareModbusDiscoveryMethod(stringsJoinTargets(p.Targets), p.Port, p.TimeoutMS, p.Concurrency, "ping")
	if err != nil {
		return err
	}
	return discoverModbusTargets(ctx, checked, probe, emit)
}
func newModbusPingPayload() ([]byte, uint16, uint16, error) {
	b := make([]byte, 36)
	if _, err := rand.Read(b); err != nil {
		return nil, 0, 0, err
	}
	return b[4:], binary.BigEndian.Uint16(b[:2]), binary.BigEndian.Uint16(b[2:4]), nil
}
func modbusPingDeadline(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return deadline, nil
}
func modbusPingSource(peer net.Addr, target string) bool {
	var ip net.IP
	switch p := peer.(type) {
	case *net.UDPAddr:
		ip = p.IP
	case *net.IPAddr:
		ip = p.IP
	default:
		return false
	}
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	want, err := netip.ParseAddr(target)
	return err == nil && a.Unmap() == want.Unmap()
}

// Echo replies are small and exact: type/code, checksum, source, ID, sequence,
// and the cryptographically random per-probe payload must all match.
func validModbusPingReply(packet []byte, peer net.Addr, target string, id, seq uint16, nonce []byte) bool {
	if len(packet) != 8+len(nonce) || len(nonce) != 32 || packet[0] != 0 || packet[1] != 0 || !modbusPingSource(peer, target) || binary.BigEndian.Uint16(packet[4:6]) != id || binary.BigEndian.Uint16(packet[6:8]) != seq || !bytes.Equal(packet[8:], nonce) {
		return false
	}
	return modbusPingChecksum(packet) == 0
}
func modbusPingChecksum(p []byte) uint16 {
	var sum uint32
	for len(p) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(p))
		p = p[2:]
	}
	if len(p) > 0 {
		sum += uint32(p[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}
func modbusPingRequest(id, seq uint16, nonce []byte) []byte {
	b := make([]byte, 8+len(nonce))
	b[0] = 8
	binary.BigEndian.PutUint16(b[4:6], id)
	binary.BigEndian.PutUint16(b[6:8], seq)
	copy(b[8:], nonce)
	binary.BigEndian.PutUint16(b[2:4], modbusPingChecksum(b))
	return b
}
