package mobileapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
)

func decodeWireNumbers(t *testing.T, source string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHTTPJSONNumberAndStringSurviveConfigurationEditAndWire(t *testing.T) {
	wire := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		wire <- string(body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	s := testSession(t, config.Request{ID: "json", Protocol: "http", Action: "POST", Endpoint: server.URL, Params: map[string]any{
		"json": map[string]any{"number": uint64(18446744073709551615), "text": "18446744073709551615", "nested": []any{int64(-9223372036854775808)}},
	}})
	for _, operation := range []string{"state", "config.get"} {
		result := decodeWireNumbers(t, s.Command(`{"op":"`+operation+`"}`))
		data := result["data"].(map[string]any)
		if operation == "config.get" {
			data = data["collection"].(map[string]any)
		}
		request := data["requests"].([]any)[0].(map[string]any)
		payload := request["params"].(map[string]any)["json"].(map[string]any)
		if payload["number"] != json.Number("18446744073709551615") || payload["text"] != "18446744073709551615" {
			t.Fatalf("%s changed JSON number/string types: %#v", operation, payload)
		}
		request["name"] = "只修改名称"
		mustOK(t, s, map[string]any{"op": "request.save", "request": request})
	}
	preview := decodeWireNumbers(t, s.Command(`{"op":"preview","request_id":"json"}`))
	data := preview["data"].(map[string]any)
	request := data["request"].(map[string]any)
	if request["params"].(map[string]any)["json"].(map[string]any)["number"] != json.Number("18446744073709551615") {
		t.Fatal("preview changed numeric payload")
	}
	mustOK(t, s, map[string]any{"op": "run", "token": data["token"], "confirmed": true})
	events := awaitDone(t, s)
	if events[len(events)-1]["data"].(map[string]any)["status"] != "completed" {
		t.Fatal(events)
	}
	select {
	case body := <-wire:
		payload := decodeWireNumbers(t, body)
		if payload["number"] != json.Number("18446744073709551615") || payload["text"] != "18446744073709551615" || payload["nested"].([]any)[0] != json.Number("-9223372036854775808") {
			t.Fatalf("wire changed values/types: %s", body)
		}
	default:
		t.Fatal("no actual loopback HTTP request")
	}
}
