package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
)

// Windows returns a native-width ICMP_ECHO_REPLY for synchronous IcmpSendEcho.
// Never dereference the reply's pointer: accept it only as an offset wholly
// contained in our bounded reply buffer, then compare the exact nonce bytes.
func validModbusWindowsPingReply(buf []byte, base uint64, pointerBytes int, target string, nonce []byte) bool {
	header := 28
	if pointerBytes == 8 {
		header = 40
	} else if pointerBytes != 4 {
		return false
	}
	if len(buf) < header || len(nonce) != 32 {
		return false
	}
	a, err := netip.ParseAddr(target)
	if err != nil || !a.Is4() {
		return false
	}
	want := a.As4()
	if !bytes.Equal(buf[:4], want[:]) || binary.LittleEndian.Uint32(buf[4:8]) != 0 || int(binary.LittleEndian.Uint16(buf[12:14])) != len(nonce) {
		return false
	}
	ptr := uint64(binary.LittleEndian.Uint32(buf[16:20]))
	if pointerBytes == 8 {
		ptr = binary.LittleEndian.Uint64(buf[16:24])
	}
	if ptr < base || ptr-base < uint64(header) || ptr-base > uint64(len(buf)-len(nonce)) {
		return false
	}
	offset := int(ptr - base)
	return bytes.Equal(buf[offset:offset+len(nonce)], nonce)
}

type modbusNativePingAPI interface {
	create() (uintptr, error)
	close(uintptr)
	send(uintptr, string, []byte, time.Duration) (bool, error)
}

func probeModbusPingNative(ctx context.Context, target string, api modbusNativePingAPI) error {
	deadline, err := modbusPingDeadline(ctx)
	if err != nil {
		return err
	}
	nonce, _, _, err := newModbusPingPayload()
	if err != nil {
		return err
	}
	handle, err := api.create()
	if err != nil {
		return err
	}
	defer api.close(handle)
	if err = ctx.Err(); err != nil {
		return err
	}
	// The synchronous system call is bounded by the reviewed per-host timeout,
	// at most2s. Cancellation prevents any next probe; in-flight OS work drains.
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return context.DeadlineExceeded
	}
	ok, err := api.send(handle, target, nonce, timeout)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("ICMP回复状态/来源/随机载荷校验失败")
	}
	return nil
}
