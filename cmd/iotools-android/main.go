//go:build android

package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"github.com/Live-yum/iotools/internal/mobileapi"
	_ "golang.org/x/crypto/x509roots/fallback"
	"sync"
	"unsafe"
)

var version = "android-dev"
var sessionMu sync.Mutex
var current *mobileapi.Session

func resultError(message string) *C.char {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": message})
	return C.CString(string(b))
}

//export IotoolsOpen
func IotoolsOpen(path *C.char, n C.int, flags C.int, root *C.char, rootN C.int) *C.char {
	if n < 1 || n > 16384 || rootN < 0 || rootN > 16384 {
		return resultError("配置路径无效")
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if current != nil {
		current.Close()
		current = nil
	}
	s, err := mobileapi.Open(string(C.GoBytes(unsafe.Pointer(path), n)), version, mobileapi.Options{RequireExisting: int(flags)&4 != 0, PrivateRoot: string(C.GoBytes(unsafe.Pointer(root), rootN)), ReadOnly: int(flags)&1 != 0, History: int(flags)&2 != 0, RTUTransport: androidUSB{}})
	if err != nil {
		return resultError(err.Error())
	}
	current = s
	return C.CString(s.Command(`{"op":"state"}`))
}

//export IotoolsCommand
func IotoolsCommand(data *C.char, n C.int) *C.char {
	if n < 1 || n > 8<<20 {
		return resultError("命令长度无效")
	}
	sessionMu.Lock()
	s := current
	sessionMu.Unlock()
	if s == nil {
		return resultError("请先打开工作区")
	}
	// Do not block lifecycle cancellation behind a local query or file command.
	return C.CString(s.Command(string(C.GoBytes(unsafe.Pointer(data), n))))
}

//export IotoolsLifecycle
func IotoolsLifecycle(action C.int) {
	sessionMu.Lock()
	s := current
	if action == 2 {
		current = nil
	}
	sessionMu.Unlock()
	if s == nil {
		return
	}
	switch action {
	case 0:
		s.Pause()
	case 1:
		s.Resume()
	case 2:
		s.Close()
	}
}
func main() {}
