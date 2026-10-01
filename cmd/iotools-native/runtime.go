//go:build cgo && !android

package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Live-yum/iotools/internal/mobileapi"
	_ "golang.org/x/crypto/x509roots/fallback"
)

const (
	nativeOK = iota
	nativeInvalidArgument
	nativeInvalidSession
	nativeResourceLimit
)

const (
	maxNativePath     = 16 << 10
	maxNativeCommand  = 8 << 20
	maxNativeReply    = 16 << 20
	maxNativeSessions = 16
)

var version = "native-dev"

type nativeSession interface {
	Command(string) string
	Pause()
	Resume()
	Close()
}

type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[uint64]nativeSession
	next     uint64
	opening  int
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: make(map[uint64]nativeSession), next: 1}
}

var nativeSessions = newSessionRegistry()

func nativeError(message string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": message})
	return string(b)
}

func validateNativePath(path, label string) error {
	if len(path) == 0 || len(path) > maxNativePath || !utf8.ValidString(path) || strings.IndexByte(path, 0) >= 0 {
		return errors.New(label + " must be valid bounded UTF-8 without NUL bytes")
	}
	if !filepath.IsAbs(path) {
		return errors.New(label + " must be absolute")
	}
	return nil
}

func (r *sessionRegistry) open(path, root string, flags uint32) (uint64, string) {
	if flags & ^uint32(7) != 0 {
		return 0, nativeError("unknown open flags")
	}
	if err := validateNativePath(path, "configuration path"); err != nil {
		return 0, nativeError(err.Error())
	}
	if err := validateNativePath(root, "app-private root"); err != nil {
		return 0, nativeError(err.Error())
	}
	if clean := filepath.Clean(root); filepath.Dir(clean) == clean {
		return 0, nativeError("app-private root cannot be a filesystem root")
	}
	// Reserve capacity before doing disk I/O, without blocking lifecycle calls.
	r.mu.Lock()
	if len(r.sessions)+r.opening >= maxNativeSessions || r.next == 0 {
		r.mu.Unlock()
		return 0, nativeError("native session limit reached")
	}
	r.opening++
	r.mu.Unlock()
	s, err := mobileapi.Open(path, version, mobileapi.Options{
		ReadOnly:        flags&1 != 0,
		History:         flags&2 != 0,
		RequireExisting: flags&4 != 0,
		PrivateRoot:     root,
		NativeSerial:    runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "darwin",
	})
	r.mu.Lock()
	r.opening--
	if err != nil {
		r.mu.Unlock()
		return 0, nativeError(err.Error())
	}
	if r.next == 0 {
		r.mu.Unlock()
		s.Close()
		return 0, nativeError("native session IDs exhausted")
	}
	id := r.next
	r.next++
	r.sessions[id] = s
	r.mu.Unlock()
	return id, s.Command(`{"op":"state"}`)
}

func (r *sessionRegistry) command(id uint64, input string) (int32, string) {
	if len(input) == 0 || len(input) > maxNativeCommand || !utf8.ValidString(input) {
		return nativeInvalidArgument, nativeError("command must be 1 byte to 8 MiB of valid UTF-8")
	}
	r.mu.Lock()
	s := r.sessions[id]
	r.mu.Unlock()
	if s == nil {
		return nativeInvalidSession, nativeError("native session is closed or unknown")
	}
	// Never hold the registry mutex through engine commands: pause/close/cancel
	// must be able to interrupt an outstanding operation from another isolate.
	return nativeOK, s.Command(input)
}

func (r *sessionRegistry) lifecycle(id uint64, action int32) int32 {
	if action < 0 || action > 2 {
		return nativeInvalidArgument
	}
	r.mu.Lock()
	s := r.sessions[id]
	if action == 2 {
		delete(r.sessions, id)
	}
	r.mu.Unlock()
	if s == nil {
		return nativeInvalidSession
	}
	switch action {
	case 0:
		s.Pause()
	case 1:
		s.Resume()
	case 2:
		s.Close()
	}
	return nativeOK
}
