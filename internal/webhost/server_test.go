package webhost

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

type testClient struct {
	s             *Server
	http          *http.Client
	csrf, session string
	t             *testing.T
}

func fixture(t *testing.T) *testClient {
	return fixtureWithLease(t, 30*time.Second)
}
func fixtureWithLease(t *testing.T, lease time.Duration) *testClient {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(l, Options{Root: t.TempDir(), Version: "test", Assets: fstest.MapFS{"index.html": {Data: []byte("real test page")}, "main.dart.js": {Data: []byte("test")}, "flutter_bootstrap.js": {Data: []byte("test")}}, Lease: lease})
	if e != nil {
		t.Fatal(e)
	}
	h := &http.Server{Handler: s}
	go h.Serve(l)
	t.Cleanup(func() { s.Close(); h.Close() })
	jar, _ := cookiejar.New(nil)
	c := &testClient{s: s, http: &http.Client{Jar: jar, Timeout: 5 * time.Second}, t: t}
	r, e := c.http.Get(s.URL() + "/api/bootstrap")
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	json.NewDecoder(r.Body).Decode(&v)
	r.Body.Close()
	c.csrf = v["data"].(map[string]any)["csrf"].(string)
	return c
}
func (c *testClient) call(path string, input any) map[string]any {
	c.t.Helper()
	b, _ := json.Marshal(input)
	r, _ := http.NewRequest("POST", c.s.URL()+path, bytes.NewReader(b))
	r.Header.Set("Origin", c.s.URL())
	r.Header.Set("X-Iotools-CSRF", c.csrf)
	r.Header.Set("X-Iotools-Session", c.session)
	reply, e := c.http.Do(r)
	if e != nil {
		c.t.Fatal(e)
	}
	defer reply.Body.Close()
	var out map[string]any
	d := json.NewDecoder(reply.Body)
	d.UseNumber()
	if e = d.Decode(&out); e != nil {
		c.t.Fatal(e)
	}
	return out
}
func (c *testClient) ok(path string, input any) map[string]any {
	c.t.Helper()
	v := c.call(path, input)
	if v["ok"] != true {
		c.t.Fatalf("%s: %v", path, v)
	}
	return v
}
func (c *testClient) open(input any) {
	v := c.ok("/api/open", input)
	c.session = v["session"].(string)
	if strings.HasPrefix(v["data"].(map[string]any)["path"].(string), "/") {
		c.t.Fatal("absolute root leaked in state")
	}
}
func TestGatewayExactHTTPConfirmationAndLease(t *testing.T) {
	c := fixtureWithLease(t, 100*time.Millisecond)
	var count atomic.Int32
	received := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- string(b)
		count.Add(1)
		w.Write([]byte("中文😀"))
	}))
	defer target.Close()
	source := `{"version":1,"requests":[{"id":"post","name":"写入","protocol":"http","action":"POST","endpoint":"` + target.URL + `","params":{"json":{"n":18446744073709551615,"s":"18446744073709551615"}}}]}`
	os.WriteFile(filepath.Join(c.s.root, "test.json"), []byte(source), 0600)
	c.open(map[string]any{"path": "test.json"})
	p := c.ok("/api/command", map[string]any{"op": "preview", "request_id": "post"})["data"].(map[string]any)
	if count.Load() != 0 {
		t.Fatal("preview wrote")
	}
	if c.call("/api/command", map[string]any{"op": "run", "token": p["token"]})["ok"] == true {
		t.Fatal("write skipped confirmation")
	}
	c.ok("/api/command", map[string]any{"op": "run", "token": p["token"], "confirmed": true})
	select {
	case body := <-received:
		if !strings.Contains(body, `"n":18446744073709551615`) || !strings.Contains(body, `"s":"18446744073709551615"`) {
			t.Fatal(body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no actual HTTP")
	}
	time.Sleep(1200 * time.Millisecond)
	v := c.ok("/api/command", map[string]any{"op": "state"})["data"].(map[string]any)
	if v["paused"] != true {
		t.Fatal("lost browser lease did not pause")
	}
	c.ok("/api/lifecycle", map[string]any{"action": "resume"})
	if count.Load() != 1 {
		t.Fatal("resume replayed write")
	}
	if c.call("/api/command", map[string]any{"op": "run", "token": p["token"], "confirmed": true})["ok"] == true {
		t.Fatal("reused token")
	}
}
func TestGatewayHostOriginCSRFAndSessionIsolation(t *testing.T) {
	c := fixture(t)
	c.open(map[string]any{})
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Host = "evil.invalid" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.invalid") }, func(r *http.Request) { r.Header.Set("X-Iotools-CSRF", "wrong") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
		r, _ := http.NewRequest("POST", c.s.URL()+"/api/command", strings.NewReader(`{"op":"state"}`))
		r.Header.Set("Origin", c.s.URL())
		r.Header.Set("X-Iotools-CSRF", c.csrf)
		r.Header.Set("X-Iotools-Session", c.session)
		change(r)
		v, e := c.http.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		v.Body.Close()
		if v.StatusCode != 403 {
			t.Fatal(v.StatusCode)
		}
	}
	jar, _ := cookiejar.New(nil)
	other := &testClient{s: c.s, http: &http.Client{Jar: jar}, t: t, session: c.session}
	r, _ := other.http.Get(c.s.URL() + "/api/bootstrap")
	var env map[string]any
	json.NewDecoder(r.Body).Decode(&env)
	r.Body.Close()
	other.csrf = env["data"].(map[string]any)["csrf"].(string)
	if other.call("/api/command", map[string]any{"op": "state"})["ok"] == true {
		t.Fatal("other client accessed session")
	}
}
func TestGatewayFileImportDownloadAndRecoveryBounds(t *testing.T) {
	c := fixture(t)
	before, _ := c.s.listFiles()
	if c.call("/api/open", map[string]any{"path": "missing.yaml"})["ok"] == true {
		t.Fatal("missing recovery accepted")
	}
	after, _ := c.s.listFiles()
	if len(before) != len(after) {
		t.Fatal("missing recovery wrote sample")
	}
	for _, p := range []string{"../escape", "/tmp/outside", "C:/outside", "a\\b"} {
		if c.call("/api/platform", map[string]any{"method": "files.read", "args": map[string]any{"path": p}})["ok"] == true {
			t.Fatal(p)
		}
	}
	upload := func(data []byte, bundle bool) map[string]any {
		flag := "0"
		if bundle {
			flag = "1"
		}
		r, _ := http.NewRequest("POST", c.s.URL()+"/api/files/upload?name=test.bin&limit=1024&bundle="+flag, bytes.NewReader(data))
		r.Header.Set("X-Iotools-CSRF", c.csrf)
		r.Header.Set("Origin", c.s.URL())
		v, e := c.http.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer v.Body.Close()
		var out map[string]any
		json.NewDecoder(v.Body).Decode(&out)
		return out
	}
	imported := upload([]byte{0, 1, 255, 3}, false)
	if imported["ok"] != true {
		t.Fatal(imported)
	}
	p := imported["data"].(map[string]any)["path"]
	v := c.ok("/api/files/download", map[string]any{"path": p, "name": "二进制.bin", "limit": 1024})
	url := v["data"].(map[string]any)["url"].(string)
	reply, e := c.http.Get(c.s.URL() + url)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(reply.Body)
	reply.Body.Close()
	if !bytes.Equal(b, []byte{0, 1, 255, 3}) {
		t.Fatal(b)
	}
	reply, _ = c.http.Get(c.s.URL() + url)
	reply.Body.Close()
	if reply.StatusCode != 404 {
		t.Fatal("download reused")
	}
	var z bytes.Buffer
	w := zip.NewWriter(&z)
	f, _ := w.Create("../escape")
	f.Write([]byte("bad"))
	w.Close()
	if upload(z.Bytes(), true)["ok"] == true {
		t.Fatal("zip traversal accepted")
	}
	entries, _ := os.ReadDir(c.s.root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "bundle-") {
			t.Fatal("failed bundle not cleaned")
		}
	}
	if upload(bytes.Repeat([]byte{'x'}, 1025), false)["ok"] == true {
		t.Fatal("oversized import accepted")
	}
}
func TestGatewayRejectsWildcardListenerAndMissingAssets(t *testing.T) {
	l, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if _, e = New(l, Options{Root: t.TempDir()}); e == nil {
		t.Fatal("public listener accepted")
	}
}
