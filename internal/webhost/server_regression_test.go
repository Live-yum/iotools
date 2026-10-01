package webhost

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func (c *testClient) request(method, path string, input io.Reader) *http.Response {
	c.t.Helper()
	r, e := http.NewRequest(method, c.s.URL()+path, input)
	if e != nil {
		c.t.Fatal(e)
	}
	r.Header.Set("Origin", c.s.URL())
	r.Header.Set("X-Iotools-CSRF", c.csrf)
	r.Header.Set("X-Iotools-Session", c.session)
	reply, e := c.http.Do(r)
	if e != nil {
		c.t.Fatal(e)
	}
	return reply
}
func (c *testClient) uploaded(path string, contents []byte) map[string]any {
	c.t.Helper()
	r := c.request("POST", path, bytes.NewReader(contents))
	defer r.Body.Close()
	var out map[string]any
	if e := json.NewDecoder(r.Body).Decode(&out); e != nil {
		c.t.Fatal(e)
	}
	return out
}
func (c *testClient) preview(id string) any {
	c.t.Helper()
	return c.ok("/api/command", map[string]any{"op": "preview", "request_id": id})["data"].(map[string]any)["token"]
}
func (c *testClient) waitDone() map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var events []any
	for time.Now().Before(deadline) {
		out := c.ok("/api/command", map[string]any{"op": "events"})["data"].(map[string]any)
		events = append(events, out["events"].([]any)...)
		for _, ev := range out["events"].([]any) {
			event := ev.(map[string]any)
			if event["kind"] == "done" {
				return event["data"].(map[string]any)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.t.Fatalf("no done event: %v", events)
	return nil
}
func TestGatewaySettingsOptionsAndReadOnly(t *testing.T) {
	c := fixture(t)
	for _, args := range []map[string]any{{"theme": "light", "history": true}, {"readOnly": true, "collection": "iotools.yaml"}} {
		c.ok("/api/platform", map[string]any{"method": "settings.save", "args": args})
	}
	got := c.ok("/api/platform", map[string]any{"method": "settings.get"})["data"].(map[string]any)
	if got["theme"] != "light" || got["history"] != true || got["readOnly"] != true || got["root"] != "" {
		t.Fatal(got)
	}
	for _, args := range []map[string]any{{"theme": "bad"}, {"history": "true"}, {"root": "/tmp"}, {"collection": "../outside"}} {
		if c.call("/api/platform", map[string]any{"method": "settings.save", "args": args})["ok"] == true {
			t.Fatal(args)
		}
	}
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte("ok")) }))
	defer target.Close()
	source := `{"version":1,"requests":[{"id":"write","protocol":"http","action":"POST","endpoint":"` + target.URL + `","params":{"json":{"hello":"test"}}}]}`
	if e := os.WriteFile(filepath.Join(c.s.root, "write.json"), []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	c.open(map[string]any{"path": "write.json", "readOnly": true, "history": true})
	state := c.ok("/api/command", map[string]any{"op": "state"})["data"].(map[string]any)
	options := state["options"].(map[string]any)
	if options["read_only"] != true || options["history"] != true {
		t.Fatal(state)
	}
	if c.call("/api/command", map[string]any{"op": "preview", "request_id": "write"})["ok"] == true || calls.Load() != 0 {
		t.Fatal("read-only write preview accepted")
	}
	c.ok("/api/command", map[string]any{"op": "options.set", "options": map[string]any{"read_only": false, "history": false}})
	token := c.preview("write")
	c.ok("/api/command", map[string]any{"op": "options.set", "options": map[string]any{"read_only": false, "history": false}})
	if c.call("/api/command", map[string]any{"op": "run", "token": token, "confirmed": true})["ok"] == true {
		t.Fatal("old preview survived option change")
	}
	c.ok("/api/command", map[string]any{"op": "run", "token": c.preview("write"), "confirmed": true})
	if got := c.waitDone(); got["status"] != "completed" || calls.Load() != 1 {
		t.Fatal(got, calls.Load())
	}
}
func TestGatewayCancelPauseAndCloseCancelRealHTTP(t *testing.T) {
	for _, action := range []string{"cancel", "pause", "close", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			c := fixture(t)
			started, cancelled := make(chan struct{}), make(chan struct{})
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
			defer target.Close()
			source := `{"version":1,"requests":[{"id":"slow","protocol":"http","action":"GET","timeout":"30s","endpoint":"` + target.URL + `"}]}`
			if e := os.WriteFile(filepath.Join(c.s.root, "slow.json"), []byte(source), 0600); e != nil {
				t.Fatal(e)
			}
			c.open(map[string]any{"path": "slow.json"})
			c.ok("/api/command", map[string]any{"op": "run", "token": c.preview("slow")})
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP did not start")
			}
			switch action {
			case "cancel":
				c.ok("/api/command", map[string]any{"op": "cancel"})
			case "shutdown":
				c.s.Close()
			default:
				c.ok("/api/lifecycle", map[string]any{"action": action})
			}
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("protocol I/O was not cancelled")
			}
			if action == "cancel" || action == "pause" {
				if got := c.waitDone(); got["status"] != "cancelled" {
					t.Fatal(got)
				}
			}
			if action == "pause" {
				c.ok("/api/lifecycle", map[string]any{"action": "resume"})
				state := c.ok("/api/command", map[string]any{"op": "state"})["data"].(map[string]any)
				if state["running"] != false || state["paused"] != false {
					t.Fatal(state)
				}
			}
		})
	}
}
func TestGatewayLeaseReclaimsAbandonedSessions(t *testing.T) {
	c := fixture(t)
	for range maxClients {
		c.open(map[string]any{})
	}
	if c.call("/api/open", map[string]any{})["ok"] == true {
		t.Fatal("active session limit bypassed")
	}
	c.s.mu.Lock()
	old := c.s.sessions[c.session]
	old.last = time.Now().Add(-2 * c.s.lease)
	c.s.mu.Unlock()
	c.open(map[string]any{})
	if !strings.Contains(old.engine.Command(`{"op":"state"}`), `session is closed`) {
		t.Fatal("expired session not closed")
	}
	c.s.mu.Lock()
	current := c.s.sessions[c.session]
	c.s.reapLocked(time.Now().Add(idleRetention + time.Second))
	count := len(c.s.sessions) + len(c.s.clients)
	c.s.mu.Unlock()
	if count != 0 || !strings.Contains(current.engine.Command(`{"op":"state"}`), `session is closed`) {
		t.Fatal("idle state not reaped")
	}
}
func TestGatewayRepeatedLeaseAndResumeRemainOrdered(t *testing.T) {
	c := fixture(t)
	c.open(map[string]any{})
	for range 25 {
		c.s.mu.Lock()
		current := c.s.sessions[c.session]
		current.last = time.Now().Add(-2 * c.s.lease)
		c.s.reapLocked(time.Now())
		c.s.mu.Unlock()
		c.ok("/api/lifecycle", map[string]any{"action": "resume"})
		if c.ok("/api/command", map[string]any{"op": "state"})["data"].(map[string]any)["paused"] != false {
			t.Fatal("resume lost")
		}
	}
	// Engine commands are also permitted to resume; expiry must still cancel.
	c.ok("/api/command", map[string]any{"op": "pause"})
	c.ok("/api/command", map[string]any{"op": "resume"})
	c.s.mu.Lock()
	c.s.sessions[c.session].last = time.Now().Add(-2 * c.s.lease)
	c.s.reapLocked(time.Now())
	c.s.mu.Unlock()
	if c.ok("/api/command", map[string]any{"op": "state"})["data"].(map[string]any)["paused"] != true {
		t.Fatal("engine resume disabled lease")
	}
}
func TestGatewayDownloadOwnerExpiryAndChangedFile(t *testing.T) {
	c := fixture(t)
	v := c.ok("/api/files/download", map[string]any{"text": "secret", "name": "text.txt"})
	url := v["data"].(map[string]any)["url"].(string)
	id := strings.TrimPrefix(url, "/api/download/")
	c.s.mu.Lock()
	ticket := c.s.tickets[id]
	c.s.mu.Unlock()
	req := httptest.NewRequest("GET", c.s.URL()+url, nil)
	out := httptest.NewRecorder()
	c.s.download(out, req, "other-owner")
	if out.Code != 404 {
		t.Fatal(out.Code)
	}
	c.s.mu.Lock()
	ticket.expiry = time.Now().Add(-time.Second)
	c.s.tickets[id] = ticket
	c.s.mu.Unlock()
	r := c.request("GET", url, nil)
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	if _, e := os.Stat(ticket.path); !os.IsNotExist(e) {
		t.Fatal("expired export leaked", e)
	}
	p := filepath.Join(c.s.root, "growing.txt")
	os.WriteFile(p, []byte("x"), 0600)
	v = c.ok("/api/files/download", map[string]any{"path": "growing.txt", "limit": 1})
	os.WriteFile(p, []byte("xx"), 0600)
	r = c.request("GET", v["data"].(map[string]any)["url"].(string), nil)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("changed size not rechecked", r.StatusCode)
	}
}
func TestGatewayDownloadCancelledBeforeTicketCommit(t *testing.T) {
	c := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", c.s.URL()+"/api/files/download", strings.NewReader(`{"text":"cancel me"}`)).WithContext(ctx)
	out := httptest.NewRecorder()
	c.s.prepareDownload(out, req, "owner")
	if out.Code != 503 {
		t.Fatal(out.Code)
	}
	entries, e := os.ReadDir(c.s.root)
	if e != nil || len(entries) != 0 {
		t.Fatal("cancelled download leaked", entries, e)
	}
}
func TestGatewayZIPPortableBoundsAndSuccessfulBundle(t *testing.T) {
	c := fixture(t)
	makeZip := func(names []string, contents []byte, mode fs.FileMode) []byte {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for _, name := range names {
			h := &zip.FileHeader{Name: name, Method: zip.Deflate}
			h.SetMode(mode)
			f, e := z.CreateHeader(h)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write(contents); e != nil {
				t.Fatal(e)
			}
		}
		if e := z.Close(); e != nil {
			t.Fatal(e)
		}
		return b.Bytes()
	}
	for _, bad := range []struct {
		names []string
		data  []byte
		mode  fs.FileMode
	}{
		{[]string{"../escape"}, []byte("x"), 0600}, {[]string{"/absolute"}, []byte("x"), 0600},
		{[]string{"a\\b"}, []byte("x"), 0600}, {[]string{"a:stream"}, []byte("x"), 0600},
		{[]string{"CON.txt"}, []byte("x"), 0600}, {[]string{"a./b"}, []byte("x"), 0600},
		{[]string{"a ", "a"}, []byte("x"), 0600}, {[]string{"A", "a"}, []byte("x"), 0600},
		{[]string{"link"}, []byte("target"), os.ModeSymlink | 0600},
		{[]string{"huge"}, bytes.Repeat([]byte("x"), 2048), 0600},
	} {
		v := c.uploaded("/api/files/upload?bundle=1&limit=1024", makeZip(bad.names, bad.data, bad.mode))
		if v["ok"] == true {
			t.Fatalf("bad archive accepted: %v", bad.names)
		}
		entries, _ := os.ReadDir(c.s.root)
		if len(entries) != 0 {
			t.Fatal("failed archive left files", entries)
		}
	}
	source := []byte(`{"version":1,"requests":[]}`)
	out := c.uploaded("/api/files/upload?bundle=1&limit=1024", makeZip([]string{"nested/测试.json"}, source, 0600))
	if out["ok"] != true {
		t.Fatal(out)
	}
	info := out["data"].(map[string]any)["files"].([]any)[0].(map[string]any)
	c.open(map[string]any{"path": info["path"]})
	read := c.ok("/api/platform", map[string]any{"method": "files.read", "args": map[string]any{"path": info["path"]}})
	if read["data"] != string(source) {
		t.Fatal(read)
	}
}
func TestGatewayPrivatePathsAndHeaders(t *testing.T) {
	c := fixture(t)
	for _, p := range []string{".", "..", "../outside", "/tmp/file", "a\\b", "file:stream", "CON.txt", "a. /b", "a\x00b"} {
		if _, e := c.s.private(p, false); e == nil {
			t.Fatal(p)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(c.s.root, "linked")
	if e := os.Symlink(outside, link); e == nil {
		if _, e = c.s.private("linked/file", false); e == nil {
			t.Fatal("symlink accepted")
		}
	}
	r := c.request("GET", "/", nil)
	r.Body.Close()
	for key, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
		if r.Header.Get(key) != want {
			t.Fatal(key, r.Header.Get(key))
		}
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("CSP missing")
	}
	for _, path := range []string{"/missing.js", "/../secret", "/assets/../../secret"} {
		r := c.request("GET", path, nil)
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal(path, r.StatusCode)
		}
	}
	for _, source := range []string{`{} {}`, `{"unknown":true}`, `null`} {
		r := c.request("POST", "/api/open", strings.NewReader(source))
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal("bad JSON accepted", source, r.StatusCode)
		}
	}
}
func TestGatewayNewRejectsMissingOrNonRegularAssetsAndRootAlias(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	valid := fstest.MapFS{"index.html": {Data: []byte("test")}, "main.dart.js": {Data: []byte("test")}, "flutter_bootstrap.js": {Data: []byte("test")}}
	for _, assets := range []fs.FS{nil, fstest.MapFS{"index.html": {Data: []byte("x")}}, fstest.MapFS{"index.html": {Mode: fs.ModeDir}, "main.dart.js": {Data: []byte("x")}, "flutter_bootstrap.js": {Data: []byte("x")}}} {
		if s, e := New(l, Options{Root: t.TempDir(), Assets: assets}); e == nil {
			s.Close()
			t.Fatal("invalid assets accepted")
		}
	}
	for _, root := range []string{"relative", string(filepath.Separator)} {
		if s, e := New(l, Options{Root: root, Assets: valid}); e == nil {
			s.Close()
			t.Fatal("root accepted", root)
		}
	}
	link := filepath.Join(t.TempDir(), "root-alias")
	if e := os.Symlink(string(filepath.Separator), link); e == nil {
		if s, e := New(l, Options{Root: link, Assets: valid}); e == nil {
			s.Close()
			t.Fatal("root alias accepted")
		}
	}
}
func TestGatewayServeStopsOnCloseOrCancellation(t *testing.T) {
	for _, action := range []string{"close", "cancel", "listener"} {
		t.Run(action, func(t *testing.T) {
			l, e := net.Listen("tcp4", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			s, e := New(l, Options{Root: t.TempDir(), Assets: fstest.MapFS{"index.html": {Data: []byte("x")}, "main.dart.js": {Data: []byte("x")}, "flutter_bootstrap.js": {Data: []byte("x")}}})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- s.Serve(ctx, l) }()
			r, e := http.Get(s.URL() + "/")
			if e != nil {
				t.Fatal(e)
			}
			r.Body.Close()
			switch action {
			case "close":
				s.Close()
			case "cancel":
				cancel()
			case "listener":
				l.Close()
			}
			select {
			case err := <-result:
				if action != "listener" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Serve did not stop")
			}
			out := httptest.NewRecorder()
			s.ServeHTTP(out, httptest.NewRequest("GET", s.URL()+"/api/bootstrap", nil))
			if out.Code != 503 {
				t.Fatal("closed gateway still accepts requests", out.Code)
			}
		})
	}
}
func TestCopyBoundedCancellationAndOverflowLeaveNoFile(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		p := filepath.Join(t.TempDir(), "copy")
		_, e := copyBounded(ctx, p, strings.NewReader("12345"), 4)
		cancel()
		if e == nil {
			t.Fatal("copy unexpectedly passed")
		}
		if cancelled && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatal("partial file leaked", e)
		}
	}
}
func TestGatewayConcurrentShutdown(t *testing.T) {
	c := fixture(t)
	c.open(map[string]any{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { c.s.Close() })
	}
	wg.Wait()
	c.s.mu.Lock()
	count := len(c.s.sessions) + len(c.s.tickets)
	c.s.mu.Unlock()
	if count != 0 {
		t.Fatal("closed gateway retained resources")
	}
}

func TestGatewayServeRejectsDifferentListener(t *testing.T) {
	c := fixture(t)
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if e = c.s.Serve(context.Background(), l); e == nil {
		t.Fatal("different listener accepted")
	}
}

func TestGatewayInterruptedUploadCleansPartialFile(t *testing.T) {
	c := fixture(t)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, "POST", c.s.URL()+"/api/files/upload?name=partial.bin&limit=1024", reader)
	if e != nil {
		t.Fatal(e)
	}
	request.ContentLength = 1024
	request.Header.Set("Origin", c.s.URL())
	request.Header.Set("X-Iotools-CSRF", c.csrf)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, _ := c.http.Do(request)
		if response != nil {
			response.Body.Close()
		}
	}()
	if _, e = writer.Write([]byte("partial")); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, e := os.ReadDir(c.s.root)
		if e != nil {
			t.Fatal(e)
		}
		if len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload did not begin")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	writer.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled upload did not return")
	}
	for {
		entries, e := os.ReadDir(c.s.root)
		if e != nil {
			t.Fatal(e)
		}
		if len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled upload left partial files", entries)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestGatewayGeneratedNamesStayPortable(t *testing.T) {
	for _, name := range []string{"", ".", " .. ", "CON.txt", "CON .txt", "COM¹.log", "CONIN$", "a/b\\c\x00", "中文😀.bin", strings.Repeat("a", 119) + ".tail", strings.Repeat("a", 119) + " tail", strings.Repeat("文", 60)} {
		got := safeName(name)
		if !portablePath(got) || len(got) > 121 {
			t.Fatalf("unsafe generated name %q -> %q", name, got)
		}
	}
}
