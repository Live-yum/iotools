package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestModbusPingValidationAndUnavailableStops(t *testing.T) {
	if _, err := PrepareModbusDiscoveryMethod("::1", 502, 500, 1, "ping"); err == nil {
		t.Fatal("IPv6 Ping accepted")
	}
	if _, err := PrepareModbusDiscoveryMethod("127.0.0.1", 502, 500, 1, "raw"); err == nil {
		t.Fatal("raw method accepted")
	}
	p, _ := PrepareModbusDiscoveryMethod("127.0.0.0/29", 502, 100, 1, "ping")
	var calls atomic.Int32
	err := discoverModbusPing(context.Background(), p, func(context.Context, string) error { calls.Add(1); return ErrModbusICMPUnavailable }, nil)
	if !errors.Is(err, ErrModbusICMPUnavailable) || calls.Load() != 1 {
		t.Fatal("unavailable retried", err, calls.Load())
	}
	p.Targets = []string{"::1"}
	calls.Store(0)
	if err = discoverModbusPing(context.Background(), p, func(context.Context, string) error { calls.Add(1); return nil }, nil); err == nil || calls.Load() != 0 {
		t.Fatal("invalid plan probed")
	}
}
func TestModbusPingReplySourceIDSequenceNonceAndChecksum(t *testing.T) {
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	b := modbusPingRequest(7, 9, nonce)
	b[0] = 0
	b[2] = 0
	b[3] = 0
	binary.BigEndian.PutUint16(b[2:4], modbusPingChecksum(b))
	peer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}
	valid := func(b []byte) bool { return validModbusPingReply(b, peer, "127.0.0.1", 7, 9, nonce) }
	if !valid(b) {
		t.Fatal("valid reply rejected")
	}
	for _, index := range []int{0, 1, 2, 4, 6, 8, 39} {
		bad := append([]byte{}, b...)
		bad[index] ^= 1
		if valid(bad) {
			t.Fatal("wrong packet accepted", index)
		}
	}
	if valid(b[:len(b)-1]) || valid(append(b, 0)) || validModbusPingReply(b, &net.IPAddr{IP: net.ParseIP("127.0.0.2")}, "127.0.0.1", 7, 9, nonce) {
		t.Fatal("wrong source/length")
	}
}
func TestModbusWindowsPingReplyLayoutBounds(t *testing.T) {
	nonce := make([]byte, 32)
	nonce[0] = 19
	for _, width := range []int{4, 8} {
		header := 28
		if width == 8 {
			header = 40
		}
		base := uint64(4096)
		buf := make([]byte, 256)
		copy(buf[:4], []byte{127, 0, 0, 1})
		binary.LittleEndian.PutUint16(buf[12:14], 32)
		if width == 8 {
			binary.LittleEndian.PutUint64(buf[16:24], base+uint64(header))
		} else {
			binary.LittleEndian.PutUint32(buf[16:20], uint32(base)+uint32(header))
		}
		copy(buf[header:], nonce)
		if !validModbusWindowsPingReply(buf, base, width, "127.0.0.1", nonce) {
			t.Fatal(width)
		}
		for _, index := range []int{0, 4, 12, 16, header} {
			bad := append([]byte{}, buf...)
			bad[index] ^= 1
			if validModbusWindowsPingReply(bad, base, width, "127.0.0.1", nonce) {
				t.Fatal("malformed native accepted", width, index)
			}
		}
		bad := append([]byte{}, buf...)
		for i := 16; i < 16+width; i++ {
			bad[i] = 255
		}
		if validModbusWindowsPingReply(bad, base, width, "127.0.0.1", nonce) {
			t.Fatal("out of bounds native pointer accepted")
		}
	}
}

type fakeModbusNativePing struct {
	created, closed, sent int
	reply                 bool
	err                   error
	cancel                context.CancelFunc
	nonces                [][]byte
}

func (f *fakeModbusNativePing) create() (uintptr, error) { f.created++; return 42, nil }
func (f *fakeModbusNativePing) close(h uintptr) {
	if h == 42 {
		f.closed++
	}
}
func (f *fakeModbusNativePing) send(h uintptr, target string, nonce []byte, d time.Duration) (bool, error) {
	f.sent++
	f.nonces = append(f.nonces, append([]byte{}, nonce...))
	if f.cancel != nil {
		f.cancel()
	}
	return f.reply, f.err
}
func TestModbusNativePingCancellationAndHandleLifecycle(t *testing.T) {
	f := &fakeModbusNativePing{reply: true}
	for n := 0; n < 2; n++ {
		if err := probeModbusPingNative(context.Background(), "127.0.0.1", f); err != nil {
			t.Fatal(err)
		}
	}
	if f.created != 2 || f.closed != 2 || f.sent != 2 || string(f.nonces[0]) == string(f.nonces[1]) {
		t.Fatal(f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := probeModbusPingNative(ctx, "127.0.0.1", f); !errors.Is(err, context.Canceled) || f.created != 2 {
		t.Fatal("canceled created API", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	f.cancel = cancel
	if err := probeModbusPingNative(ctx, "127.0.0.1", f); !errors.Is(err, context.Canceled) || f.closed != 3 {
		t.Fatal("late OS response accepted", err)
	}
	f.cancel = nil
	f.reply = false
	if err := probeModbusPingNative(context.Background(), "127.0.0.1", f); err == nil {
		t.Fatal("unvalidated success")
	}
}
func TestModbusPingLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := probeModbusPing(ctx, "127.0.0.1")
	if errors.Is(err, ErrModbusICMPUnavailable) {
		t.Skipf("explicit OS permission/platform unavailability: %v", err)
	}
	if err != nil {
		t.Fatalf("loopback ICMP failed (not skipped): %v", err)
	}
}
