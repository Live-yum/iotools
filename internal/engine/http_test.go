package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func TestHTTPRealRoundTripAndWriteGate(t *testing.T) {
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			io.Copy(w, r.Body)
		} else {
			io.WriteString(w, `{"healthy":true}`)
		}
	}))
	defer s.Close()
	r := config.Request{Protocol: "http", Action: "POST", Endpoint: s.URL, Params: map[string]any{"json": map[string]any{"ok": true}}}
	if e := Run(context.Background(), r, false, nil); e == nil || hits != 0 {
		t.Fatal("write gate failed")
	}
	var events []Event
	if e := Run(context.Background(), r, true, func(e Event) { events = append(events, e) }); e != nil {
		t.Fatal(e)
	}
	if len(events) != 1 || hits != 1 {
		t.Fatalf("unexpected roundtrip: %+v", events)
	}
	m := events[0].Data.(map[string]any)
	if m["body"].(map[string]any)["ok"] != true {
		t.Fatal("JSON response lost")
	}
}
func TestHTTPCancelAndBadStatus(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer s.Close()
	r := config.Request{Protocol: "http", Action: "GET", Endpoint: s.URL}
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("HTTP 400 marked success")
	}
	r.Endpoint += "/wait"
	r.Timeout = "30ms"
	start := time.Now()
	if e := Run(context.Background(), r, false, nil); e == nil {
		t.Fatal("timeout marked success")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation too slow")
	}
}
func TestHTTPRejectsUntrustedTLSAndOversize(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer s.Close()
	if e := Run(context.Background(), config.Request{Protocol: "http", Action: "GET", Endpoint: s.URL}, false, nil); e == nil {
		t.Fatal("untrusted TLS accepted")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", maxBody+1)) }))
	defer large.Close()
	if e := Run(context.Background(), config.Request{Protocol: "http", Action: "GET", Endpoint: large.URL}, false, nil); e == nil {
		t.Fatal("oversize response accepted")
	}
}
