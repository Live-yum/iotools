package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = "version: 1\nprofiles:\n  dev: {host: 'http://localhost:8080'}\nrequests:\n - id: health\n   protocol: http\n   action: GET\n   endpoint: ${host}/health\n   params: {bearer: '${env:IOTOOLS_TEST_TOKEN}'}\n"

func TestParseResolve(t *testing.T) {
	t.Setenv("IOTOOLS_TEST_TOKEN", "private-test-value")
	c, e := Parse([]byte(valid))
	if e != nil {
		t.Fatal(e)
	}
	r, e := c.Resolve(c.Requests[0], "dev")
	if e != nil {
		t.Fatal(e)
	}
	if r.Endpoint != "http://localhost:8080/health" || r.String("bearer", "") != "private-test-value" {
		t.Fatalf("resolution failed: %+v", r)
	}
	if c.Requests[0].String("bearer", "") != "${env:IOTOOLS_TEST_TOKEN}" {
		t.Fatal("resolution mutated source")
	}
	if _, e = c.Resolve(c.Requests[0], "missing"); e == nil {
		t.Fatal("missing profile accepted")
	}
}
func TestRejectInvalid(t *testing.T) {
	for _, s := range []string{"version: 2\nrequests: []", "version: 1\nunknown: yes", valid + "---\nversion: 1", "version: 1\nrequests: [{id: a, protocol: telnet, action: read, endpoint: x}]", "version: 1\nrequests: [{id: a, protocol: http, action: GET, endpoint: x, timeout: 0s}]"} {
		if _, e := Parse([]byte(s)); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestSavePreservesOnValidationError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "collection.yaml")
	if e := Save(p, []byte(valid)); e != nil {
		t.Fatal(e)
	}
	if e := Save(p, []byte("version: 999")); e == nil {
		t.Fatal("invalid save succeeded")
	}
	b, _ := os.ReadFile(p)
	if string(b) != valid {
		t.Fatal("invalid save damaged original")
	}
}
func TestWriteClassification(t *testing.T) {
	for _, r := range []Request{{Protocol: "http", Action: "POST"}, {Protocol: "mqtt", Action: "publish"}, {Protocol: "kafka", Action: "delete-topic"}, {Protocol: "modbus", Action: "write-register"}, {Protocol: "opcua", Action: "call"}} {
		if !r.Mutates() {
			t.Errorf("missed write: %+v", r)
		}
	}
	if (Request{Protocol: "http", Action: "GET"}).Mutates() {
		t.Fatal("GET marked write")
	}
}
func TestUnusedCryptoSecretsAreLazy(t *testing.T) {
	c := &Collection{Profiles: map[string]map[string]string{"local": {}}}
	r := Request{Protocol: "http", Endpoint: "http://127.0.0.1", Params: map[string]any{"request_crypto": "plain64", "crypto": map[string]any{"plain64": map[string]any{"type": "base64"}, "unused-prod": map[string]any{"type": "aes-256-cbc", "key": "${env:IOTOOLS_MISSING_CRYPTO_KEY}"}}}}
	resolved, e := c.Resolve(r, "local")
	if e != nil {
		t.Fatal(e)
	}
	defs := resolved.Params["crypto"].(map[string]any)
	if len(defs) != 1 || defs["plain64"] == nil {
		t.Fatal("unused codec not isolated")
	}
	if len(r.Params["crypto"].(map[string]any)) != 2 {
		t.Fatal("source codec definitions changed")
	}
	r.Params["request_crypto"] = "unused-prod"
	if _, e = c.Resolve(r, "local"); e == nil {
		t.Fatal("used missing secret was ignored")
	}
}
func TestRequestEditPreservesUnrelatedComments(t *testing.T) {
	source := []byte("# collection comment\n" + valid)
	r := Request{ID: "health", Name: "中文名称", Protocol: "http", Action: "GET", Endpoint: "${host}/new"}
	b, e := ReplaceRequest(source, "health", r)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "# collection comment") || !strings.Contains(string(b), "中文名称") {
		t.Fatalf("comments/edit missing: %s", b)
	}
	if _, e = ReplaceRequest(source, "health", Request{ID: "", Protocol: "http", Action: "GET", Endpoint: "x"}); e == nil {
		t.Fatal("invalid edited request accepted")
	}
}

func TestResponseCryptoReferencesResolveLazily(t *testing.T) {
	c := &Collection{Version: 1, Profiles: map[string]map[string]string{"local": {}}, Requests: []Request{{ID: "x", Protocol: "http", Action: "GET", Endpoint: "http://localhost", Params: map[string]any{"crypto": map[string]any{"unused": map[string]any{"key": "${env:IOTOOLS_MISSING_TEST_SECRET}"}, "used": map[string]any{"key": "${env:IOTOOLS_USED_TEST_SECRET}"}}, "response_transform": []any{map[string]any{"type": "decode", "crypto": "used"}}}}}}
	t.Setenv("IOTOOLS_USED_TEST_SECRET", "a-secret")
	r, e := c.Resolve(c.Requests[0], "local")
	if e != nil {
		t.Fatal(e)
	}
	defs := r.Params["crypto"].(map[string]any)
	if len(defs) != 1 || defs["used"].(map[string]any)["key"] != "a-secret" {
		t.Fatal("used codec not resolved lazily")
	}
	if len(c.Requests[0].Params["crypto"].(map[string]any)) != 2 {
		t.Fatal("source definitions changed")
	}
}
func TestReplaceRequestPreservesUnrelatedSource(t *testing.T) {
	source := []byte("# customer comment\n" + valid + " - id: second\n   protocol: http\n   action: GET\n   endpoint: http://localhost/other # keep this\n")
	c, e := Parse(source)
	if e != nil {
		t.Fatal(e)
	}
	r := c.Requests[0]
	r.Name = "已修改"
	b, e := ReplaceRequest(source, r.ID, r)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "# customer comment") || !strings.Contains(string(b), "# keep this") {
		t.Fatal("unrelated comments lost")
	}
	result, e := Parse(b)
	if e != nil || result.Requests[0].Name != "已修改" || result.Requests[1].ID != "second" {
		t.Fatal("request replacement failed")
	}
	r.ID = "second"
	if _, e = ReplaceRequest(source, "health", r); e == nil {
		t.Fatal("duplicate ID accepted")
	}
}

func TestDefaultProfileValidation(t *testing.T) {
	if _, e := Parse([]byte("version: 1\ndefault_profile: missing\nrequests: []\n")); e == nil {
		t.Fatal("missing default profile accepted")
	}
	c, e := Parse([]byte("version: 1\ndefault_profile: local\nprofiles: {local: {host: localhost}}\nrequests: []\n"))
	if e != nil || c.DefaultProfile != "local" {
		t.Fatalf("%+v %v", c, e)
	}
}
