package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlumberCrossFileScalarSequenceAndOrderedSpread(t *testing.T) {
	dir := t.TempDir()
	defs := []byte("method: GET\nurls: ['http://127.0.0.1:1']\nrecipe: {method: GET, url: 'http://127.0.0.1:2'}\n")
	if e := os.WriteFile(filepath.Join(dir, "defs.yml"), defs, 0600); e != nil {
		t.Fatal(e)
	}
	input := []byte("requests:\n  before:\n    method: POST\n    $ref: './defs.yml#/recipe'\n  after:\n    $ref: './defs.yml#/recipe'\n    method: PUT\n  scalar:\n    method: {$ref: './defs.yml#/method'}\n    url: {$ref: './defs.yml#/urls/0'}\n")
	c, e := ParseCollectionAt(input, filepath.Join(dir, "main.yml"))
	if e != nil {
		t.Fatal(e)
	}
	actions := map[string]string{}
	for _, r := range c.Requests {
		actions[r.ID] = r.Action
	}
	if actions["before"] != "GET" || actions["after"] != "PUT" || actions["scalar"] != "GET" {
		t.Fatal(actions)
	}
	if _, e := ParseCollection(input); e == nil {
		t.Fatal("memory parser unexpectedly followed relative file")
	}
	if _, e := ParseCollectionAt([]byte("requests: {$ref: 'https://example.invalid/x#/requests'}"), filepath.Join(dir, "main.yml")); e == nil || !strings.Contains(e.Error(), "本地") {
		t.Fatal("remote ref not rejected", e)
	}
}
func TestSlumberReferenceCyclesAndNativeLiteral(t *testing.T) {
	if _, e := ParseCollection([]byte(".a: {$ref: '#/.b'}\n.b: {$ref: '#/.a'}\nrequests: {}")); e == nil {
		t.Fatal("cycle accepted")
	}
	c, e := ParseCollection([]byte("version: 1\nrequests:\n - id: native\n   protocol: http\n   action: POST\n   endpoint: http://127.0.0.1:1\n   params: {json: {$ref: 'literal sent to server'}}"))
	if e != nil {
		t.Fatal(e)
	}
	if c.Requests[0].Params["json"].(map[string]any)["$ref"] != "literal sent to server" {
		t.Fatal("native JSON reference interpreted")
	}
}
