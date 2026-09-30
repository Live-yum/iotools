//go:build linux || darwin

package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fakeModbusPingPacket struct {
	packet    []byte
	reads     int
	closed    chan struct{}
	once      sync.Once
	wait      bool
	localPort int
}

func (f *fakeModbusPingPacket) ReadFrom(out []byte) (int, net.Addr, error) {
	if f.wait {
		<-f.closed
		return 0, nil, net.ErrClosed
	}
	b := append([]byte{}, f.packet...)
	b[0] = 0
	b[2] = 0
	b[3] = 0
	peer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}
	switch f.reads {
	case 0:
		peer.IP = net.ParseIP("127.0.0.2")
	case 1:
		b[4] ^= 1
	case 2:
		b[8] ^= 1
	}
	f.reads++
	binary.BigEndian.PutUint16(b[2:4], modbusPingChecksum(b))
	return copy(out, b), peer, nil
}
func (f *fakeModbusPingPacket) WriteTo(b []byte, a net.Addr) (int, error) {
	f.packet = append([]byte{}, b...)
	return len(b), nil
}
func (f *fakeModbusPingPacket) Close() error                     { f.once.Do(func() { close(f.closed) }); return nil }
func (f *fakeModbusPingPacket) LocalAddr() net.Addr              { return &net.UDPAddr{Port: f.localPort} }
func (f *fakeModbusPingPacket) SetDeadline(time.Time) error      { return nil }
func (f *fakeModbusPingPacket) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeModbusPingPacket) SetWriteDeadline(time.Time) error { return nil }
func TestModbusPingDatagramKernelIDAndCancel(t *testing.T) {
	for _, kernel := range []bool{true, false} {
		f := &fakeModbusPingPacket{closed: make(chan struct{}), localPort: 4567}
		if err := probeModbusPingDatagram(context.Background(), "127.0.0.1", func() (net.PacketConn, error) { return f, nil }, kernel); err != nil || f.reads != 4 {
			t.Fatal(err, f.reads)
		}
		if kernel && binary.BigEndian.Uint16(f.packet[4:6]) != 4567 {
			t.Fatal("kernel ID ignored")
		}
	}
	f := &fakeModbusPingPacket{closed: make(chan struct{}), localPort: 4567, wait: true}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- probeModbusPingDatagram(ctx, "127.0.0.1", func() (net.PacketConn, error) { close(started); return f, nil }, true)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	if !errors.Is(modbusICMPDatagramError(syscall.EACCES), ErrModbusICMPUnavailable) || errors.Is(modbusICMPDatagramError(context.DeadlineExceeded), ErrModbusICMPUnavailable) {
		t.Fatal("wrong unavailable classification")
	}
}
