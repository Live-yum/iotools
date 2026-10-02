package engine

import (
	"github.com/Live-yum/iotools/internal/config"
	"testing"
)

func TestTemporaryOverridesNeverChangeCollection(t *testing.T) {
	r := config.Request{Protocol: "http", Action: "POST", Params: map[string]any{"headers": map[string]any{"Content-Type": "old", "X-Remove": "value"}, "query": map[string]any{"x": "old"}, "json": map[string]any{"old": true}}}
	c := &config.Collection{Profiles: map[string]map[string]string{"local": {"host": "old"}}, Requests: []config.Request{r}}
	body := `{"large":18446744073709551615}`
	updated, request, _, e := ApplyHTTPOverrides(c, r, "local", HTTPOverrides{Fields: []string{"host=new"}, Headers: []string{"content-type=application/json", "X-Remove"}, Query: []string{"x=1", "x=2"}, Body: &body})
	if e != nil {
		t.Fatal(e)
	}
	if c.Profiles["local"]["host"] != "old" || r.Params["headers"].(map[string]any)["Content-Type"] != "old" || r.Params["json"].(map[string]any)["old"] != true {
		t.Fatal("source collection mutated")
	}
	if updated.Profiles["local"]["host"] != "new" || len(request.Params["query"].(map[string]any)["x"].([]any)) != 2 {
		t.Fatal("temporary override not applied")
	}
	if _, ok := request.Params["headers"].(map[string]any)["X-Remove"]; ok {
		t.Fatal("header removal failed")
	}
	if _, _, _, e := ApplyHTTPOverrides(c, r, "local", HTTPOverrides{Form: []string{"x=y"}}); e == nil {
		t.Fatal("non-form override accepted")
	}
}

func TestTemporaryBodyReplacesStream(t *testing.T) {
	for _, key := range []string{"body_file", "body_stream"} {
		r := config.Request{Protocol: "http", Params: map[string]any{key: "private-file", "max_upload_bytes": 100}}
		body := "new"
		_, got, _, err := ApplyHTTPOverrides(&config.Collection{}, r, "", HTTPOverrides{Body: &body})
		if err != nil || got.Params["body"] != "new" || got.Params[key] != nil || got.Params["max_upload_bytes"] != nil {
			t.Fatalf("override: %v %v", got.Params, err)
		}
		if r.Params[key] != "private-file" {
			t.Fatal("source changed")
		}
	}
}

func TestTemporaryURLAndConflictingAuth(t *testing.T) {
	r := config.Request{Protocol: "http", Endpoint: "http://old", Params: map[string]any{"query": map[string]any{"q": "kept"}}}
	c := &config.Collection{}
	url := "http://new"
	_, got, _, err := ApplyHTTPOverrides(c, r, "", HTTPOverrides{URL: &url})
	if err != nil || got.Endpoint != url || got.Params["query"].(map[string]any)["q"] != "kept" || r.Endpoint != "http://old" {
		t.Fatal(got, err)
	}
	if _, _, _, err := ApplyHTTPOverrides(c, r, "", HTTPOverrides{Basic: &url, Bearer: &url}); err == nil {
		t.Fatal("conflicting auth accepted")
	}
}

func TestNextConfigStrictLocalPath(t *testing.T) {
	for _, v := range []any{"", "https://outside/config", "{{ ENV.PATH }}", "bad\npath", 123} {
		r := config.Request{Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"next_config": v}}
		if validateParams(r) == nil {
			t.Fatalf("invalid path accepted: %v", v)
		}
	}
}
