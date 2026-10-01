//go:build cgo && !android

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func decodeNativeReply(t *testing.T, output string, ok bool) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result["ok"] != ok {
		t.Fatalf("unexpected reply: %s", output)
	}
	return result
}

func TestNativeInitializationRecoveryAndSessionIsolation(t *testing.T) {
	r := newSessionRegistry()
	root := t.TempDir()
	path := filepath.Join(root, "初始-😀.yaml")
	id, reply := r.open(path, root, 4)
	if id != 0 {
		t.Fatal("missing recovery opened")
	}
	decodeNativeReply(t, reply, false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("recovery created missing configuration", err)
	}
	id, reply = r.open(path, root, 0)
	if id == 0 {
		t.Fatal(reply)
	}
	decodeNativeReply(t, reply, true)
	t.Cleanup(func() { r.lifecycle(id, 2) })
	second, reply := r.open(path, root, 5)
	if second == 0 || second == id {
		t.Fatal("invalid second session", second, reply)
	}
	t.Cleanup(func() { r.lifecycle(second, 2) })
	if r.lifecycle(id, 0) != nativeOK {
		t.Fatal("pause failed")
	}
	_, reply = r.command(second, `{"op":"state"}`)
	data := decodeNativeReply(t, reply, true)["data"].(map[string]any)
	if data["paused"] != false || data["options"].(map[string]any)["read_only"] != true {
		t.Fatal("sessions are not isolated", data)
	}
	if r.lifecycle(id, 2) != nativeOK || r.lifecycle(id, 2) != nativeInvalidSession {
		t.Fatal("close must invalidate the session exactly once")
	}
	status, reply := r.command(id, `{"op":"state"}`)
	if status != nativeInvalidSession {
		t.Fatal("closed session accepted a command")
	}
	decodeNativeReply(t, reply, false)
	third, reply := r.open(path, root, 4)
	if third == 0 || third == id || third == second {
		t.Fatal("session ID was reused", third, reply)
	}
	defer r.lifecycle(third, 2)
	if r.lifecycle(id, 2) != nativeInvalidSession {
		t.Fatal("stale handle closed a new session")
	}
	_, reply = r.command(third, `{"op":"state"}`)
	decodeNativeReply(t, reply, true)
}

func TestNativeRejectsUnsafeAndUnboundedInputs(t *testing.T) {
	r := newSessionRegistry()
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	for name, tc := range map[string]struct {
		path, root string
		flags      uint32
	}{
		"empty path":        {"", root, 0},
		"empty root":        {path, "", 0},
		"relative path":     {"config.yaml", root, 0},
		"relative root":     {path, "private", 0},
		"NUL path":          {path + "\x00", root, 0},
		"NUL root":          {path, root + "\x00", 0},
		"invalid UTF8 path": {path + "\xff", root, 0},
		"invalid UTF8 root": {path, root + "\xff", 0},
		"long path":         {strings.Repeat("a", maxNativePath+1), root, 0},
		"long root":         {path, strings.Repeat("a", maxNativePath+1), 0},
		"unknown flags":     {path, root, 8},
		"outside path":      {filepath.Join(t.TempDir(), "outside.yaml"), root, 0},
		"filesystem root":   {path, filepath.VolumeName(root) + string(os.PathSeparator), 0},
	} {
		t.Run(name, func(t *testing.T) {
			id, reply := r.open(tc.path, tc.root, tc.flags)
			if id != 0 {
				r.lifecycle(id, 2)
				t.Fatal("unsafe open succeeded")
			}
			decodeNativeReply(t, reply, false)
		})
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid open changed the filesystem", err)
	}
	for _, input := range []string{"", "\xff", strings.Repeat("x", maxNativeCommand+1)} {
		status, reply := r.command(0, input)
		if status != nativeInvalidArgument {
			t.Fatal("invalid command accepted")
		}
		decodeNativeReply(t, reply, false)
	}
	for _, action := range []int32{-1, 3, 1 << 30} {
		if r.lifecycle(0, action) != nativeInvalidArgument {
			t.Fatal("invalid lifecycle accepted")
		}
	}
}

func TestNativeConcurrentOpenCapacityAndReleasedSlots(t *testing.T) {
	r := newSessionRegistry()
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	id, output := r.open(path, root, 0)
	if id == 0 {
		t.Fatal(output)
	}
	r.lifecycle(id, 2)
	var wg sync.WaitGroup
	ids := make(chan uint64, maxNativeSessions*3)
	for range maxNativeSessions * 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _ := r.open(path, root, 4)
			if id != 0 {
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)
	if len(ids) != maxNativeSessions {
		t.Fatalf("opened %d sessions, want %d", len(ids), maxNativeSessions)
	}
	seen := map[uint64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate handle")
		}
		seen[id] = true
		if r.lifecycle(id, 2) != nativeOK {
			t.Fatal("close failed")
		}
	}
	id, output = r.open(path, root, 4)
	if id == 0 || seen[id] {
		t.Fatal("closed slots not reusable with fresh IDs", output)
	}
	r.lifecycle(id, 2)
	if len(r.sessions) != 0 || r.opening != 0 {
		t.Fatal("session registry leaked")
	}
}

func TestNativeConcurrentCommandsAndLifecycle(t *testing.T) {
	r := newSessionRegistry()
	root := t.TempDir()
	id, output := r.open(filepath.Join(root, "config.yaml"), root, 0)
	if id == 0 {
		t.Fatal(output)
	}
	var wg sync.WaitGroup
	for _, input := range []string{`{"op":"state"}`, `{"op":"events"}`, `{"op":"cancel"}`} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				status, reply := r.command(id, input)
				if status != nativeOK && status != nativeInvalidSession {
					t.Errorf("unexpected concurrent command status %d: %s", status, reply)
				}
				if !json.Valid([]byte(reply)) {
					t.Errorf("invalid concurrent reply: %s", reply)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			r.lifecycle(id, 0)
			r.lifecycle(id, 1)
		}
		r.lifecycle(id, 2)
	}()
	wg.Wait()
	if r.lifecycle(id, 2) != nativeInvalidSession {
		t.Fatal("concurrent close left a live handle")
	}
}

func TestNativeIDsNeverWrapAndReuse(t *testing.T) {
	r := newSessionRegistry()
	r.next = ^uint64(0)
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	id, output := r.open(path, root, 0)
	if id != ^uint64(0) {
		t.Fatal("last available ID not allocated", id, output)
	}
	r.lifecycle(id, 2)
	id, output = r.open(path, root, 4)
	if id != 0 {
		t.Fatal("session IDs wrapped")
	}
	decodeNativeReply(t, output, false)
}

type blockedNativeSession struct {
	entered chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (s *blockedNativeSession) Command(string) string {
	close(s.entered)
	<-s.done
	return `{"ok":true}`
}
func (s *blockedNativeSession) Pause()  { s.once.Do(func() { close(s.done) }) }
func (s *blockedNativeSession) Resume() {}
func (s *blockedNativeSession) Close()  { s.Pause() }

func TestNativeLifecycleDoesNotWaitBehindCommand(t *testing.T) {
	for _, action := range []int32{0, 2} {
		r := newSessionRegistry()
		s := &blockedNativeSession{entered: make(chan struct{}), done: make(chan struct{})}
		r.sessions[1] = s
		completed := make(chan struct{})
		go func() { r.command(1, `{"op":"blocked"}`); close(completed) }()
		<-s.entered
		lifecycleDone := make(chan int32, 1)
		go func() { lifecycleDone <- r.lifecycle(1, action) }()
		select {
		case status := <-lifecycleDone:
			if status != nativeOK {
				t.Fatal(status)
			}
		case <-time.After(2 * time.Second):
			s.Close()
			t.Fatal("lifecycle blocked behind command")
		}
		select {
		case <-completed:
		case <-time.After(2 * time.Second):
			t.Fatal("command was not cancelled")
		}
	}
}
