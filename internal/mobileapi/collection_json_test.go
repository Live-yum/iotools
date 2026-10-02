package mobileapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMobileCollectionJSONOpenImportSaveRecovery(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "imported.json")
	source := `{"version":1,"requests":[{"id":"x","protocol":"http","action":"GET","endpoint":"http:\/\/127.0.0.1\/中文","params":{"value":18446744073709551615,"literal":"\\\/"}}]}`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, "test", Options{PrivateRoot: root, RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.collection.Requests[0].Params["value"] != uint64(18446744073709551615) {
		t.Fatal("lost uint64")
	}
	got := mustOK(t, s, map[string]any{"op": "config.get"}).(map[string]any)
	if got["source"] != source {
		t.Fatalf("changed original source: %v", got)
	}
	mustOK(t, s, map[string]any{"op": "config.validate", "source": source})
	mustOK(t, s, map[string]any{"op": "config.import", "format": "iotools", "source": source})
	mustOK(t, s, map[string]any{"op": "config.save", "source": source})
	invalid := strings.Replace(source, `"version":1`, `"version":1,"version":1`, 1)
	if cmd(t, s, map[string]any{"op": "config.save", "source": invalid})["ok"] != false {
		t.Fatal("saved duplicate fields")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != source {
		t.Fatal("failed save changed original")
	}
	// Imported JSON references receive the same parser and private-root inspection.
	defs := `{"recipe":{"method":"GET","url":"http:\/\/127.0.0.1\/reference"}}`
	if err = os.WriteFile(filepath.Join(root, "defs.json"), []byte(defs), 0600); err != nil {
		t.Fatal(err)
	}
	imported := `{"requests":{"read":{"$ref":".\/defs.json#\/recipe"}}}`
	target := filepath.Join(root, "reference.json")
	if err = os.WriteFile(target, []byte(imported), 0600); err != nil {
		t.Fatal(err)
	}
	plan := mustOK(t, s, map[string]any{"op": "config.switch", "path": "reference.json"}).(map[string]any)
	mustOK(t, s, map[string]any{"op": "config.switch", "path": "reference.json", "token": plan["token"], "confirmed": true})
	if s.collection.Requests[0].Endpoint != "http://127.0.0.1/reference" {
		t.Fatal(s.collection.Requests)
	}
	recovered, err := Open(target, "test", Options{PrivateRoot: root, RequireExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	recovered.Close()
}
