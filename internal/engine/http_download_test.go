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
	"testing"
)

func TestStreamingDownloadLargeAndNoOverwrite(t *testing.T) {
	const size = 6 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.CopyN(w, strings.NewReader(strings.Repeat("x", size)), size)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "response.bin")
	r := config.Request{Protocol: "http", Action: "GET", Endpoint: server.URL, Params: map[string]any{"response_file": path}}
	seen := false
	if e := Run(context.Background(), r, false, func(e Event) { seen = e.Kind == "response-file" }); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Size() != size || !seen {
		t.Fatal("stream output missing", e)
	}
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("overwrote download")
	}
	small := filepath.Join(t.TempDir(), "too-large")
	r.Params["response_file"] = small
	r.Params["max_response_bytes"] = 10
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("limit ignored")
	}
	if _, e := os.Stat(small); !os.IsNotExist(e) {
		t.Fatal("partial output retained")
	}
}
