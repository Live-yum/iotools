package engine

import (
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLargeFileStreamAndSlumberImport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.bin")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	const size = 6 << 20
	if e = f.Truncate(size); e != nil {
		t.Fatal(e)
	}
	f.Close()
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, e := io.Copy(io.Discard, r.Body)
		if e != nil {
			t.Error(e)
		}
		count.Store(n)
		if r.ContentLength != size {
			t.Errorf("ContentLength %d", r.ContentLength)
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	collectionPath := filepath.Join(dir, "slumber.yml")
	data := []byte("requests:\n  upload:\n    method: POST\n    url: " + server.URL + "\n    body:\n      type: stream\n      data: \"{{ file('body.bin') }}\"\n")
	if e = os.WriteFile(collectionPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	c, _, e := LoadCollection(collectionPath)
	if e != nil {
		t.Fatal(e)
	}
	r := c.Requests[0]
	if e = RunCollection(context.Background(), c, r, "", false, nil); e == nil {
		t.Fatal("stream upload bypassed write gate")
	}
	if e = RunCollection(context.Background(), c, r, "", true, nil); e != nil {
		t.Fatal(e)
	}
	if count.Load() != size {
		t.Fatal("large file wasn't streamed")
	}
	command, e := GenerateCurl(context.Background(), c, r, "", false, false)
	if e != nil || !strings.Contains(command, "--data-binary '@") {
		t.Fatalf("%s %v", command, e)
	}
}
func TestStreamingRejectsImplicitCryptoAndSize(t *testing.T) {
	r := config.Request{Protocol: "http", Params: map[string]any{"body_file": "missing", "request_crypto": "aes"}}
	if _, _, _, e := openHTTPBodyFile(context.Background(), r); e == nil {
		t.Fatal("stream silently bypassed body encryption")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	os.WriteFile(path, []byte("abcd"), 0600)
	r.Params = map[string]any{"body_file": path, "max_upload_bytes": 3}
	if _, _, _, e := openHTTPBodyFile(context.Background(), r); e == nil {
		t.Fatal("oversize allowed")
	}
}
