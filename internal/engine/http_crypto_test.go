package engine

import (
	"context"
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPCryptoWireAndDerivedView(t *testing.T) {
	const encrypted = "UEI1Z8QQS25WGasdhDJA4g=="
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Query().Get("phone") != encrypted || r.Header.Get("X-Phone") != encrypted || !strings.Contains(string(b), encrypted) {
			t.Error("wire encryption mismatch")
		}
		io.WriteString(w, `{"phone":"`+encrypted+`","id":9007199254740993}`)
	}))
	defer s.Close()
	codecs := map[string]any{"am": map[string]any{"algorithm": "aes-128-cbc", "key": map[string]any{"value": "0123456789abcdef"}, "iv": map[string]any{"value": "0123456789abcdef"}}}
	r := config.Request{Protocol: "http", Action: "POST", Endpoint: s.URL + "?phone=13800138000", Params: map[string]any{"crypto": codecs, "body": `{"phone":"13800138000"}`, "headers": map[string]any{"X-Phone": "13800138000"}, "request_transforms": []any{map[string]any{"target": "json", "name": "$.phone", "crypto": "am", "type": "encrypt"}, map[string]any{"target": "header", "name": "X-Phone", "crypto": "am", "type": "encrypt"}, map[string]any{"target": "query", "name": "phone", "crypto": "am", "type": "encrypt"}}, "response_transform": []any{map[string]any{"type": "decrypt", "crypto": "am", "paths": []string{"$.phone"}}}}}
	var events []Event
	if e := Run(context.Background(), r, true, func(e Event) { events = append(events, e) }); e != nil {
		t.Fatal(e)
	}
	if len(events) != 2 || events[0].Kind != "response" || events[1].Kind != "transformed" {
		t.Fatal(events)
	}
	raw := events[0].Data.(map[string]any)["body"].(map[string]any)
	view := events[1].Data.(map[string]any)["body"].(map[string]any)
	if raw["phone"] != encrypted || view["phone"] != "13800138000" || view["id"] != json.Number("9007199254740993") {
		t.Fatal("raw/derived mismatch")
	}
}
func TestHTTPCryptoFailsBeforeNetwork(t *testing.T) {
	hits := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer s.Close()
	r := config.Request{Protocol: "http", Action: "POST", Endpoint: s.URL, Params: map[string]any{"request_crypto": "missing"}}
	if e := Run(context.Background(), r, true, nil); e == nil || hits != 0 {
		t.Fatal("invalid encryption sent request")
	}
}
func TestHTTPCryptoFailedResponseNoPartialDerived(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"a":"aGk=","b":"!"}`) }))
	defer s.Close()
	r := config.Request{Protocol: "http", Action: "GET", Endpoint: s.URL, Params: map[string]any{"crypto": map[string]any{"b64": map[string]any{"algorithm": "base64"}}, "response_transform": []any{map[string]any{"type": "decode", "crypto": "b64", "paths": []string{"$.a", "$.b"}}}}}
	var events []Event
	if e := Run(context.Background(), r, false, func(e Event) { events = append(events, e) }); e == nil {
		t.Fatal("invalid result passed")
	}
	if len(events) != 1 || events[0].Kind != "response" {
		t.Fatal("partial derived result leaked")
	}
}
