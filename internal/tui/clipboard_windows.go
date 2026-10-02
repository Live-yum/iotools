//go:build windows

package tui

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

func setSystemClipboard(text string) (bool, error) {
	if len(text) > 1<<20 {
		return true, fmt.Errorf("复制内容超过1MiB")
	}
	encoded, e := syscall.UTF16FromString(text)
	if e != nil {
		return true, e
	}
	user := syscall.NewLazyDLL("user32.dll")
	kernel := syscall.NewLazyDLL("kernel32.dll")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	window, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	if window == 0 {
		return false, nil
	}
	opened, _, e := user.NewProc("OpenClipboard").Call(window)
	if opened == 0 {
		return true, fmt.Errorf("打开剪贴板失败：%v", e)
	}
	defer user.NewProc("CloseClipboard").Call()
	memory, _, e := kernel.NewProc("GlobalAlloc").Call(2, uintptr(len(encoded)*2))
	if memory == 0 {
		return true, e
	}
	owned := true
	defer func() {
		if owned {
			kernel.NewProc("GlobalFree").Call(memory)
		}
	}()
	pointer, _, e := kernel.NewProc("GlobalLock").Call(memory)
	if pointer == 0 {
		return true, e
	}
	move := kernel.NewProc("RtlMoveMemory")
	if e := move.Find(); e != nil {
		kernel.NewProc("GlobalUnlock").Call(memory)
		return true, e
	}
	move.Call(pointer, uintptr(unsafe.Pointer(&encoded[0])), uintptr(len(encoded)*2))
	runtime.KeepAlive(encoded)
	kernel.NewProc("GlobalUnlock").Call(memory)
	emptied, _, e := user.NewProc("EmptyClipboard").Call()
	if emptied == 0 {
		return true, e
	}
	accepted, _, e := user.NewProc("SetClipboardData").Call(13, memory)
	if accepted == 0 {
		return true, e
	}
	owned = false
	return true, nil
}
