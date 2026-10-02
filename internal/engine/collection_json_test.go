package engine

import (
	"github.com/Live-yum/iotools/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectionJSONSlashesAcrossEntrypoints(t *testing.T) {
	source := []byte(`{"version":1,"requests":[{"id":"x","protocol":"http","action":"GET","endpoint":"http:\/\/127.0.0.1\/中文","params":{"value":18446744073709551615,"literal":"\\\/"}}]}`)
	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	c, raw, err := LoadCollection(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(source) || c.Requests[0].Endpoint != "http://127.0.0.1/中文" || c.Requests[0].Params["value"] != uint64(18446744073709551615) || c.Requests[0].Params["literal"] != `\/` {
		t.Fatalf("changed source semantics: %#v", c)
	}
	r := c.Requests[0]
	r.Name = "edited"
	edited, err := config.ReplaceRequest(source, "x", r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseCollectionAt(edited, path); err != nil {
		t.Fatal(err)
	}
	_, err = AppendSelectedMTUIRequests(source, []config.Request{{ID: "registers", Protocol: "modbus", Action: "read-holding", Endpoint: "mock://local", Params: map[string]any{"address": 0, "count": 1, "unit": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := strings.Replace(string(source), `"version":1`, `"version":1,"version":1`, 1)
	if _, err = ParseCollectionAt([]byte(duplicate), path); err == nil {
		t.Fatal("accepted duplicate keys")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(source) {
		t.Fatal("read/import rewrote original")
	}
}

func TestCollectionJSONSlashesInReferencedDocuments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.json")
	defs := `{"recipe":{"method":"GET","url":"http:\/\/127.0.0.1\/中文"}}`
	if err := os.WriteFile(filepath.Join(dir, "defs.json"), []byte(defs), 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"requests":{"read":{"$ref":".\/defs.json#\/recipe"}}}`)
	c, err := ParseCollectionAt(source, path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Requests[0].Endpoint != "http://127.0.0.1/中文" {
		t.Fatal(c.Requests)
	}
	if _, err = importSlumberV3([]byte(`{"requests":{"read":{"method":"GET","url":"http:\/\/127.0.0.1\/legacy"}}}`)); err != nil {
		t.Fatal(err)
	}
}
