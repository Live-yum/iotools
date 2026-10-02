package engine

import (
	"bytes"
	"context"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPRedirectExplicitOriginAndHeaderIsolation(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials forwarded")
		}
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	r := config.Request{Protocol: "http", Action: "GET", Endpoint: source.URL, Params: map[string]any{"follow_redirects": true, "headers": map[string]any{"X-API-Key": "secret", "Authorization": "secret"}}}
	if err := Run(context.Background(), r, false, func(Event) {}); err == nil || hits.Load() != 0 {
		t.Fatal("unapproved target contacted", err)
	}
	r.Params["redirect_origins"] = []any{target.URL}
	if err := Run(context.Background(), r, false, func(Event) {}); err != nil || hits.Load() != 1 {
		t.Fatal(err, hits.Load())
	}
}
func TestHTTPRedirectLimitAndModificationConfirmation(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/again", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	r := config.Request{Protocol: "http", Action: "POST", Endpoint: server.URL, Params: map[string]any{"follow_redirects": true, "max_redirects": 2, "body": "body"}}
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{AuthorizeRequestWrite: func(context.Context, config.Request) (bool, error) { return false, nil }})
	if err := Run(ctx, r, true, func(Event) {}); err == nil || hits.Load() != 1 {
		t.Fatal("redirect write was not confirmed", err, hits.Load())
	}
	hits.Store(0)
	if err := Run(context.Background(), r, true, func(Event) {}); err == nil || !strings.Contains(err.Error(), "max_redirects") || hits.Load() != 3 {
		t.Fatal(err, hits.Load())
	}
}
func TestHTTPInsecureExceptionIsExactAndRuntimeOptIn(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("ok")) }))
	defer server.Close()
	r := config.Request{Protocol: "http", Action: "GET", Endpoint: server.URL, Params: map[string]any{"ignore_certificate_hosts": []any{"127.0.0.1"}}}
	if err := Run(context.Background(), r, false, func(Event) {}); err == nil || hits.Load() != 0 {
		t.Fatal("insecure request bypassed approval", err)
	}
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{AllowInsecureTLS: true})
	if err := Run(ctx, r, false, func(Event) {}); err != nil || hits.Load() != 1 {
		t.Fatal(err, hits.Load())
	}
	r.Params["ignore_certificate_hosts"] = []any{"localhost"}
	if err := Run(ctx, r, false, func(Event) {}); err == nil || hits.Load() != 1 {
		t.Fatal("unlisted host bypassed TLS", err)
	}
	r.Params["ignore_certificate_hosts"] = []any{"*.example.com"}
	if _, err := httpInsecureHosts(r); err == nil {
		t.Fatal("wildcard accepted")
	}
}

func TestHTTPStreamRedirectReplay(t *testing.T) {
	body := []byte("stream body")
	file := t.TempDir() + "/body"
	os.WriteFile(file, body, 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, body) {
			t.Error("body mismatch")
		}
		if r.URL.Path != "/final" {
			http.Redirect(w, r, "/final", 307)
		} else {
			w.Write([]byte("done"))
		}
	}))
	defer server.Close()
	r := config.Request{Protocol: "http", Action: "POST", Endpoint: server.URL, Params: map[string]any{"body_file": file, "follow_redirects": true}}
	if err := Run(context.Background(), r, true, func(Event) {}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPRedirectPolicyRejectsDowngradeAndBadOptions(t *testing.T) {
	r := config.Request{Protocol: "http", Params: map[string]any{"follow_redirects": true, "redirect_origins": []any{"http://example.invalid"}}}
	policy, err := httpRedirectPolicy(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := http.NewRequest("GET", "https://example.invalid/start", nil)
	next, _ := http.NewRequest("GET", "http://example.invalid/end", nil)
	if err := policy(next, []*http.Request{first}); err == nil {
		t.Fatal("TLS downgrade accepted")
	}
	for _, params := range []map[string]any{{"follow_redirects": "yes"}, {"max_redirects": 1.5}, {"redirect_origins": []any{"https://example.invalid/path"}}} {
		r.Params = params
		if _, err := httpRedirectPolicy(context.Background(), r); err == nil {
			t.Fatal("bad policy accepted", params)
		}
	}
}
