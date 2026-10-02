package mobileapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePrivateRootCollection(t *testing.T, path, id string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := "version: 1\nrequests:\n  - id: " + id + "\n    protocol: http\n    action: GET\n    endpoint: http://127.0.0.1\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func canonicalPrivateRootPath(t *testing.T, path string) string {
	t.Helper()
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

func TestPrivateRootNestedRecoveryAndSiblingSwitch(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "imported", "group", "collection.yaml")
	sibling := filepath.Join(root, "other", "collection.yaml")
	writePrivateRootCollection(t, nested, "nested")
	writePrivateRootCollection(t, sibling, "sibling")
	// A basename-only reopen would silently load this unrelated collection.
	writePrivateRootCollection(t, filepath.Join(root, "collection.yaml"), "wrong")
	canonicalRoot := canonicalPrivateRootPath(t, root)
	canonicalNested := canonicalPrivateRootPath(t, nested)
	canonicalSibling := canonicalPrivateRootPath(t, sibling)
	s, err := Open(nested, "test", Options{PrivateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if s.root != canonicalRoot || s.path != canonicalNested || s.collection.Requests[0].ID != "nested" {
		t.Fatalf("nested recovery changed root/path/source: %q %q %+v", s.root, s.path, s.collection.Requests)
	}
	mustOK(t, s, map[string]any{"op": "options.set", "options": map[string]any{"history": true}})
	if s.root != canonicalRoot {
		t.Fatal("public options changed private root")
	}
	plan := mustOK(t, s, map[string]any{"op": "config.switch", "path": "other/collection.yaml"}).(map[string]any)
	if s.path != canonicalNested {
		t.Fatal("preview switched configuration")
	}
	state := mustOK(t, s, map[string]any{"op": "config.switch", "path": "other/collection.yaml", "token": plan["token"], "confirmed": true}).(map[string]any)
	if state["path"] != canonicalSibling || s.root != canonicalRoot || s.collection.Requests[0].ID != "sibling" {
		t.Fatalf("sibling switch failed: %v", state)
	}
	// Reopening the switched nested path preserves the same sandbox again.
	reopened, err := Open(s.path, "test", Options{PrivateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.root != canonicalRoot || reopened.path != canonicalSibling {
		t.Fatal("recovery narrowed the private root")
	}
	mustOK(t, reopened, map[string]any{"op": "file.read", "path": "imported/group/collection.yaml"})
}

func TestPrivateRootDefaultAndMissingInitialization(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested", "collection.yaml")
	writePrivateRootCollection(t, nested, "nested")
	canonicalNested := canonicalPrivateRootPath(t, nested)
	s, err := Open(nested, "test", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.root != filepath.Dir(canonicalNested) || s.path != canonicalNested {
		t.Fatal("default dirname boundary changed")
	}
	if cmd(t, s, map[string]any{"op": "file.read", "path": "../collection.yaml"})["ok"] != false {
		t.Fatal("default root escaped")
	}
	missing := filepath.Join(root, "nested", "new.yaml")
	if recovered, err := Open(missing, "test", Options{PrivateRoot: root, RequireExisting: true}); err == nil {
		recovered.Close()
		t.Fatal("recovery accepted a missing configuration")
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("failed recovery created a sample configuration", err)
	}
	initialized, err := Open(missing, "test", Options{PrivateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer initialized.Close()
	if initialized.path != filepath.Join(filepath.Dir(canonicalNested), "new.yaml") || len(initialized.collection.Requests) == 0 {
		t.Fatal("missing-file initialization did not preserve nested path")
	}
	if _, err := os.Stat(filepath.Join(root, "new.yaml")); !os.IsNotExist(err) {
		t.Fatal("initialization created a file at the wrong level", err)
	}
}

func TestPrivateRootRejectsOutsideAndSymlinkPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "private")
	inside := filepath.Join(root, "nested", "collection.yaml")
	outside := filepath.Join(base, "private-other", "collection.yaml")
	writePrivateRootCollection(t, inside, "inside")
	writePrivateRootCollection(t, outside, "outside")
	for _, link := range []struct{ target, path string }{
		{filepath.Dir(outside), filepath.Join(root, "outside-link")},
		{filepath.Dir(inside), filepath.Join(root, "inside-link")},
		{inside, filepath.Join(root, "file-link.yaml")},
	} {
		if err := os.Symlink(link.target, link.path); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct{ path, root string }{
		"relative root":    {inside, "private"},
		"missing root":     {inside, filepath.Join(base, "missing")},
		"file root":        {inside, inside},
		"relative config":  {"nested/collection.yaml", root},
		"outside prefix":   {outside, root},
		"parent traversal": {root + "/../private-other/collection.yaml", root},
		"outside symlink":  {filepath.Join(root, "outside-link", "collection.yaml"), root},
		"inside symlink":   {filepath.Join(root, "inside-link", "collection.yaml"), root},
		"file symlink":     {filepath.Join(root, "file-link.yaml"), root},
		"new outside":      {filepath.Join(base, "private-other", "new.yaml"), root},
		"new symlink":      {filepath.Join(root, "outside-link", "new.yaml"), root},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(tc.path, "test", Options{PrivateRoot: tc.root})
			if err == nil {
				s.Close()
				t.Fatal("unsafe open accepted")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(base, "private-other", "new.yaml")); !os.IsNotExist(err) {
		t.Fatal("rejected open created an outside file", err)
	}
}

func TestPrivateRootCanonicalAliasAndPublicJSONIsolation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "private")
	path := filepath.Join(root, "nested", "collection.yaml")
	writePrivateRootCollection(t, path, "nested")
	canonicalRoot := canonicalPrivateRootPath(t, root)
	canonicalPath := canonicalPrivateRootPath(t, path)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, suppliedPath := range []string{canonicalPath, filepath.Join(alias, "nested", "collection.yaml")} {
		s, err := Open(suppliedPath, "test", Options{PrivateRoot: alias})
		if err != nil {
			t.Fatal(err)
		}
		if s.root != canonicalRoot || s.path != canonicalPath {
			t.Fatal("trusted root alias was not canonicalized")
		}
		for field, value := range map[string]any{"private_root": base, "PrivateRoot": base, "require_existing": true, "RequireExisting": true} {
			if cmd(t, s, map[string]any{"op": "options.set", "options": map[string]any{field: value}})["ok"] != false {
				t.Fatal("public command accepted a host-only option", field)
			}
		}
		if s.root != canonicalRoot {
			t.Fatal("rejected command changed private root")
		}
		b, err := json.Marshal(Options{PrivateRoot: alias, RequireExisting: true})
		if err != nil || string(b) != `{"read_only":false,"history":false}` {
			t.Fatal("host-only options leaked into JSON", string(b), err)
		}
		s.Close()
	}
}
