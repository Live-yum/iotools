//go:build windows

package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/sys/windows"
	"net/netip"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

type windowsModbusPing struct{ createProc, sendProc, closeProc *windows.LazyProc }

func probeModbusPing(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dll := windows.NewLazySystemDLL("iphlpapi.dll")
	api := windowsModbusPing{dll.NewProc("IcmpCreateFile"), dll.NewProc("IcmpSendEcho"), dll.NewProc("IcmpCloseHandle")}
	for _, proc := range []*windows.LazyProc{api.createProc, api.sendProc, api.closeProc} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("%w: 系统ICMP API缺失: %v", ErrModbusICMPUnavailable, err)
		}
	}
	return probeModbusPingNative(ctx, target, api)
}
func windowsModbusPingError(err error) error {
	switch err {
	case syscall.Errno(5), syscall.Errno(50), syscall.Errno(10043), syscall.Errno(10047):
		return fmt.Errorf("%w: %v", ErrModbusICMPUnavailable, err)
	}
	return fmt.Errorf("ICMP系统调用失败: %v", err)
}
func (w windowsModbusPing) create() (uintptr, error) {
	h, _, err := w.createProc.Call()
	if h == 0 || h == ^uintptr(0) {
		return 0, windowsModbusPingError(err)
	}
	return h, nil
}
func (w windowsModbusPing) close(h uintptr) { _, _, _ = w.closeProc.Call(h) }
func (w windowsModbusPing) send(h uintptr, target string, nonce []byte, timeout time.Duration) (bool, error) {
	a, err := netip.ParseAddr(target)
	if err != nil || !a.Is4() {
		return false, fmt.Errorf("ICMP仅支持IPv4")
	}
	if len(nonce) != 32 || timeout <= 0 || timeout > 2*time.Second {
		return false, fmt.Errorf("ICMP载荷/时限无效")
	}
	ip := a.As4()
	reply := make([]byte, 256)
	milliseconds := (timeout + time.Millisecond - 1) / time.Millisecond
	count, _, callErr := w.sendProc.Call(h, uintptr(binary.LittleEndian.Uint32(ip[:])), uintptr(unsafe.Pointer(&nonce[0])), uintptr(len(nonce)), 0, uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(milliseconds))
	runtime.KeepAlive(nonce)
	runtime.KeepAlive(reply)
	if count == 0 {
		return false, windowsModbusPingError(callErr)
	}
	if status := binary.LittleEndian.Uint32(reply[4:8]); status != 0 {
		return false, fmt.Errorf("ICMP系统回复状态%d", status)
	}
	if count != 1 {
		return false, fmt.Errorf("ICMP回复数量不明确")
	}
	valid := validModbusWindowsPingReply(reply, uint64(uintptr(unsafe.Pointer(&reply[0]))), int(unsafe.Sizeof(uintptr(0))), target, nonce)
	runtime.KeepAlive(reply)
	return valid, nil
}
