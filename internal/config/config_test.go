package config

import (
	"os"
	"path/filepath"
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
