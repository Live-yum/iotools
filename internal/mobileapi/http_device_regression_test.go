package mobileapi

import (
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMobileAESApprovedHistoryWorkflow(t *testing.T) {
	wire := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		wire <- string(b)
		fmt.Fprint(w, "tPXT09vO41jl5nnDRzL/o2IXTqn+Ik2TLqQ7mHjsQGJVm+cnEb4WHA0oNdfc/Mam")
	}))
	defer server.Close()
	r := config.Request{ID: "aes", Protocol: "http", Action: "POST", Endpoint: server.URL, Timeout: "5s", Params: map[string]any{
		"body": "工业 AES 请求 😀", "crypto": map[string]any{"aes": map[string]any{"algorithm": "aes-128-cbc", "key": map[string]any{"value": "0123456789abcdef", "encoding": "text"}, "iv": map[string]any{"value": "fedcba9876543210", "encoding": "text"}, "ciphertext_encoding": "base64"}},
		"request_transforms": []any{map[string]any{"type": "encrypt", "crypto": "aes", "target": "body"}},
		"response_transform": []any{map[string]any{"type": "decrypt", "crypto": "aes", "scope": "body", "text_encoding": "utf8", "parse": "json"}}}}
	s := testSession(t, r)
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true}})
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "aes"), "confirmed": true})
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		data := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
		for _, v := range data["events"].([]any) {
			e := v.(map[string]any)
			if e["kind"] == "interaction" {
				t.Fatalf("unchanged approved request asked again: %v", e)
			}
			if e["kind"] == "done" {
				if e["data"].(map[string]any)["status"] != "completed" {
					t.Fatalf("workflow failed: %v", e)
				}
				select {
				case got := <-wire:
					if got != "qaecBiJg5qYkn1be0PS0WKhxPv6lLp69C1xwHFRMubE=" {
						t.Fatalf("wire=%q", got)
					}
				default:
					t.Fatal("no wire request")
				}

				rows := mustOK(t, s, map[string]any{"op": "history.list", "request_id": "aes"}).([]any)
				if len(rows) != 1 {
					t.Fatalf("history rows=%v", rows)
				}
				row := rows[0].(map[string]any)
				if row["created_at"] == "" {
					t.Fatal("history timestamp missing")
				}
				detail := mustOK(t, s, map[string]any{"op": "history.get", "history_id": row["id"]}).(map[string]any)
				if detail["transformed"] != `{"result":"AES 解密成功中文😀"}` {
					t.Fatalf("stored transformed=%v", detail["transformed"])
				}
				mustOK(t, s, map[string]any{"op": "history.query", "sql": "SELECT recipe,status FROM http_history"})
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no done event")
}

func TestInvalidResponsePlanNeverSendsApprovedWrite(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	s := testSession(t, config.Request{ID: "bad", Protocol: "http", Action: "POST", Endpoint: server.URL, Params: map[string]any{"crypto": map[string]any{"b64": map[string]any{"algorithm": "base64"}}, "response_transform": []any{map[string]any{"type": "decode", "crypto": "b64", "scope": "body"}}}})
	mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "bad"), "confirmed": true})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "failed" || hits.Load() != 0 {
		t.Fatalf("invalid plan performed I/O: hits=%d events=%v", hits.Load(), events)
	}
}
