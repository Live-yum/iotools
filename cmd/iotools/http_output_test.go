package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPOutputPreservesBinaryAndExitStatus(t *testing.T) {
	raw := []byte{0, 255, 3}
	var stdout, stderr bytes.Buffer
	d := httpDisplay{verbose: true, stderr: &stderr}
	d.event(engine.Event{Kind: "response", Data: map[string]any{"status": 404, "headers": map[string]any{"x": "y"}, "raw_body_base64": base64.StdEncoding.EncodeToString(raw)}})
	err := d.finish(&stdout, fmt.Errorf("HTTP error"), true)
	var exit cliExitError
	if !bytes.Equal(stdout.Bytes(), raw) || !errors.As(err, &exit) || exit.code != 2 {
		t.Fatal(stdout.Bytes(), err)
	}
}
func TestHTTPOutputTransformedAndNoOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.json")
	d := httpDisplay{transformed: true, output: p}
	d.event(engine.Event{Kind: "transformed", Data: map[string]any{"body": map[string]any{"answer": 42}}})
	if err := d.finish(&bytes.Buffer{}, nil, false); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	if err := d.finish(&bytes.Buffer{}, nil, false); err == nil {
		t.Fatal("overwrite accepted")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("file changed")
	}
}
func TestHTTPCLIStreamOutput(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 5<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer server.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "request.yaml")
	out := filepath.Join(dir, "response.bin")
	text := "version: 1\nrequests:\n- id: get\n  protocol: http\n  action: GET\n  endpoint: " + server.URL + "\n"
	os.WriteFile(p, []byte(text), 0600)
	if err := run([]string{"--file", p, "--run", "get", "--output", out}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, payload) {
		t.Fatal("output mismatch")
	}
	if err := run([]string{"--file", p, "--run", "get", "--dry-run", "--output", out}); err == nil {
		t.Fatal("ignored output flag")
	}
}

func TestHTTPTransformedWithoutTransformKeepsHTTPStatus(t *testing.T) {
	d := httpDisplay{transformed: true}
	d.event(engine.Event{Kind: "response", Data: map[string]any{"status": 404, "raw_body_base64": base64.StdEncoding.EncodeToString([]byte("missing"))}})
	var out bytes.Buffer
	err := d.finish(&out, fmt.Errorf("HTTP failure"), true)
	var status cliExitError
	if out.String() != "missing" || !errors.As(err, &status) || status.code != 2 {
		t.Fatal(out.String(), err)
	}
	err = d.finish(&bytes.Buffer{}, &engine.HTTPResponseTransformError{Err: fmt.Errorf("transform failed")}, true)
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatal(err)
	}
}
