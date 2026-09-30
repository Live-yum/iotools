//go:build android

package tui

import (
	"fmt"
	"sync"
)

var mobileClipboardMu sync.Mutex
var mobileClipboard func(string) error

// SetAndroidClipboard connects explicit TUI copy confirmations to the native app.
// It never reads the device clipboard.
func SetAndroidClipboard(writer func(string) error) {
	mobileClipboardMu.Lock()
	mobileClipboard = writer
	mobileClipboardMu.Unlock()
}
func setSystemClipboard(text string) (bool, error) {
	mobileClipboardMu.Lock()
	writer := mobileClipboard
	mobileClipboardMu.Unlock()
	if writer == nil {
		return true, fmt.Errorf("Android剪贴板尚未就绪")
	}
	return true, writer(text)
}
