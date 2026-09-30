//go:build android

package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/base64"
	"fmt"
	"github.com/Live-yum/iotools/internal/mobile"
	"github.com/Live-yum/iotools/internal/tui"
	_ "golang.org/x/crypto/x509roots/fallback"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

var version = "android-dev"
var mu sync.Mutex
var current *mobile.Session
var lastError string
var clipboardMu sync.Mutex
var clipboardText string

func fail(err error) C.int {
	if err != nil {
		lastError = err.Error()
		return -1
	}
	lastError = ""
	return 0
}

//export IotoolsStart
func IotoolsStart(path *C.char, cols, rows, flags C.int) C.int {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		select {
		case <-current.Done():
		default:
			return 1
		}
	}
	name := C.GoString(path)
	os.Setenv("TERM", "xterm-256color")
	os.Setenv("COLORTERM", "truecolor")
	os.Setenv("HOME", filepath.Dir(name))
	if err := os.Chdir(filepath.Dir(name)); err != nil {
		return fail(err)
	}
	tui.SetAndroidClipboard(func(text string) error {
		if len(text) > 1<<20 {
			return fmt.Errorf("复制超过1MiB")
		}
		clipboardMu.Lock()
		clipboardText = text
		clipboardMu.Unlock()
		return nil
	})
	s, err := mobile.StartWithOptions(name, version, int(cols), int(rows), mobile.Options{ReadOnly: int(flags)&1 != 0, History: int(flags)&2 != 0})
	if err != nil {
		return fail(err)
	}
	current = s
	return fail(nil)
}

//export IotoolsInput
func IotoolsInput(data *C.char, n C.int) C.int {
	if n < 0 || n > 65536 {
		return -1
	}
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return -1
	}
	return fail(current.TTY.Input(C.GoBytes(unsafe.Pointer(data), n)))
}

//export IotoolsRead
func IotoolsRead(data *C.char, n C.int) C.int {
	if data == nil || n < 1 || n > 65536 {
		return 0
	}
	mu.Lock()
	s := current
	mu.Unlock()
	if s == nil {
		return 0
	}
	return C.int(s.TTY.Output(unsafe.Slice((*byte)(unsafe.Pointer(data)), int(n))))
}

//export IotoolsResize
func IotoolsResize(cols, rows C.int) C.int {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return 0
	}
	return fail(current.TTY.Resize(int(cols), int(rows)))
}

//export IotoolsPause
func IotoolsPause() {
	mu.Lock()
	s := current
	mu.Unlock()
	if s != nil {
		s.Pause()
	}
}

//export IotoolsResume
func IotoolsResume() {
	mu.Lock()
	s := current
	mu.Unlock()
	if s != nil {
		s.Resume()
	}
}

//export IotoolsOptions
func IotoolsOptions(flags C.int, path *C.char) {
	mu.Lock()
	s := current
	mu.Unlock()
	if s != nil {
		s.ApplyOptions(mobile.Options{ReadOnly: int(flags)&1 != 0, History: int(flags)&2 != 0}, C.GoString(path))
	}
}

//export IotoolsValidate
func IotoolsValidate(path *C.char) C.int {
	mu.Lock()
	defer mu.Unlock()
	return fail(mobile.ValidateConfig(C.GoString(path)))
}

//export IotoolsStop
func IotoolsStop() {
	mu.Lock()
	s := current
	mu.Unlock()
	if s != nil {
		go s.Stop()
	}
}

//export IotoolsState
func IotoolsState() C.int {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return 0
	}
	select {
	case <-current.Done():
		if err := current.Err(); err != nil {
			lastError = err.Error()
		}
		return 0
	default:
		return 1
	}
}

//export IotoolsError
func IotoolsError() *C.char { mu.Lock(); defer mu.Unlock(); return C.CString(lastError) }

//export IotoolsImport
func IotoolsImport(staged, target *C.char) C.int {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		select {
		case <-current.Done():
		default:
			return fail(fmt.Errorf("请先停止终端再导入"))
		}
	}
	return fail(mobile.ImportConfig(C.GoString(staged), C.GoString(target)))
}

//export IotoolsClipboard
func IotoolsClipboard() *C.char {
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	text := clipboardText
	clipboardText = ""
	return C.CString(base64.StdEncoding.EncodeToString([]byte(text)))
}
func main() {}
