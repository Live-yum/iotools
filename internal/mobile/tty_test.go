package mobile

import (
	"bytes"
	"fmt"
	"github.com/rivo/tview"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
func TestMobileOptionsAreExplicit(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	path := filepath.Join(t.TempDir(), "iotools.yaml")
	s, err := StartWithOptions(path, "mobile", 80, 24, Options{ReadOnly: true, History: true})
	if err != nil {
		t.Fatal(err)
	}
	// No history is written merely by opening the TUI with an explicit history option.
	if _, err = os.Stat(filepath.Join(filepath.Dir(path), "history.sqlite")); !os.IsNotExist(err) {
		t.Fatal("startup created history unexpectedly", err)
	}
	s.Stop()
	select {
	case <-s.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("stop failed")
	}
}

func TestPauseCancelsRealHTTPButPreservesUnsavedEditor(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	began := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(began); <-r.Context().Done(); close(canceled) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "iotools.yaml")
	yaml := fmt.Sprintf("version: 1\nrequests:\n- id: test\n  name: HTTP客服\n  protocol: http\n  action: GET\n  endpoint: %s\n  timeout: 30s\n", server.URL)
	os.WriteFile(path, []byte(yaml), 0600)
	session, err := Start(path, "mobile", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Stop()
	waitOutput := func(needle string) {
		t.Helper()
		end := time.Now().Add(4 * time.Second)
		var all bytes.Buffer
		buf := make([]byte, 65536)
		for time.Now().Before(end) {
			n := session.TTY.Output(buf)
			all.Write(buf[:n])
			if strings.Contains(all.String(), needle) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("missing terminal output %s", needle)
	}
	waitOutput("HTTP")
	if err = session.TTY.Input([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-began:
	case <-time.After(4 * time.Second):
		t.Fatal("HTTP not started")
	}
	session.Pause()
	select {
	case <-canceled:
	case <-time.After(4 * time.Second):
		t.Fatal("background did not cancel HTTP")
	}
	time.Sleep(80 * time.Millisecond)
	session.TTY.Input([]byte("\x1bOS"))
	waitOutput("Ctrl-S")
	session.TTY.Input([]byte("\x1b[200~# 草稿😀\r\x1b[201~"))
	time.Sleep(60 * time.Millisecond)
	session.Pause()
	session.Resume()
	time.Sleep(60 * time.Millisecond)
	session.TTY.Input([]byte("\x13"))
	end := time.Now().Add(4 * time.Second)
	saved := false
	for time.Now().Before(end) {
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte("草稿😀")) {
			saved = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !saved {
		var draft string
		session.ui.App.QueueUpdateDraw(func() {
			if a, ok := session.ui.App.GetFocus().(*tview.TextArea); ok {
				draft = a.GetText()
			} else {
				draft = fmt.Sprintf("%T", session.ui.App.GetFocus())
			}
		})
		t.Fatal("pause/resume lost unsaved draft", draft)
	}
	select {
	case <-session.Done():
		t.Fatal("pause stopped whole UI")
	default:
	}
	session.Stop()
	select {
	case <-session.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("stop failed")
	}
}
