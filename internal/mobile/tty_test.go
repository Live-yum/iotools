package mobile

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTransportUnicodeLimitsResizeAndDrain(t *testing.T) {
	tty := NewTTY(80, 24)
	if err := tty.Input([]byte("客服😀")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 128)
	n, e := tty.Read(b)
	if e != nil || string(b[:n]) != "客服😀" {
		t.Fatal(n, e)
	}
	if e = tty.Input(make([]byte, 65537)); e == nil {
		t.Fatal("input bound")
	}
	called := false
	tty.NotifyResize(func() { called = true })
	if e = tty.Resize(120, 40); e != nil || !called {
		t.Fatal(e)
	}
	if _, e = tty.Write([]byte("\x1b[31m中文")); e != nil {
		t.Fatal(e)
	}
	n = tty.Output(b)
	if string(b[:n]) != "\x1b[31m中文" {
		t.Fatal(string(b[:n]))
	}
	done := make(chan error, 1)
	go func() { _, e := tty.Read(b); done <- e }()
	tty.Drain()
	select {
	case e := <-done:
		if e != io.EOF {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("drain did not unblock")
	}
}
func TestConfigImportValidationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "iotools.yaml")
	stage := filepath.Join(dir, "import.yaml")
	old := []byte("version: 1\nrequests: []\n")
	os.WriteFile(target, old, 0600)
	os.WriteFile(stage, []byte("bad: ["), 0600)
	if e := ImportConfig(stage, target); e == nil {
		t.Fatal("invalid config imported")
	}
	now, _ := os.ReadFile(target)
	if !bytes.Equal(old, now) {
		t.Fatal("original lost")
	}
	os.WriteFile(stage, []byte("version: 1\nprofiles: {}\nrequests: []\n"), 0600)
	if e := ImportConfig(stage, target); e != nil {
		t.Fatal(e)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "config-before-import-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	now, _ = os.ReadFile(backups[0])
	if !bytes.Equal(now, old) {
		t.Fatal("backup differs")
	}
}
func TestRealTUISessionStartsAndStopsWithoutTTYProcess(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	path := filepath.Join(t.TempDir(), "iotools.yaml")
	session, err := Start(path, "test-mobile", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	deadline := time.Now().Add(4 * time.Second)
	scratch := make([]byte, 65536)
	for time.Now().Before(deadline) {
		n := session.TTY.Output(scratch)
		output.Write(scratch[:n])
		if bytes.Contains(output.Bytes(), []byte("HTTP")) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bytes.Contains(output.Bytes(), []byte("HTTP")) {
		session.Stop()
		t.Fatal("real tview output missing", output.String())
	}
	session.Stop()
	select {
	case <-session.Done():
		if err := session.Err(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("TUI stop hung")
	}
}
