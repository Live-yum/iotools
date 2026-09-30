//go:build windows

package tui

import (
	"syscall"
	"testing"
)

// Availability only: CI never reads or overwrites a real user's clipboard.
func TestWindowsClipboardAPIsAvailable(t *testing.T) {
	for library, names := range map[string][]string{"kernel32.dll": {"GetConsoleWindow", "GlobalAlloc", "GlobalLock", "GlobalUnlock", "GlobalFree", "RtlMoveMemory"}, "user32.dll": {"OpenClipboard", "CloseClipboard", "EmptyClipboard", "SetClipboardData"}} {
		dll := syscall.NewLazyDLL(library)
		for _, name := range names {
			if e := dll.NewProc(name).Find(); e != nil {
				t.Fatalf("%s/%s: %v", library, name, e)
			}
		}
	}
}
