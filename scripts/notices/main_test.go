package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSelectedPackageNotices(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"LICENSE": "root license", "NOTICE.md": "project notice", "edl-v10": "EDL text", "epl-v20": "EPL text",
		"AUTHORS": "authors", "PATENTS": "patent grant", "sub/LICENSE-MIT": "ancestor license",
		"sub/imported/LICENSE.txt": "package license", "sub/imported/code.go": "package imported",
		"testdata/LICENSE": "unshipped fixture", "unused/LICENSE": "unshipped package", "NOTICEABLE.go": "not a notice",
	}
	for name, data := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := &module{Path: "example.com/dependency", Version: "v1.2.3", Dir: root}
	pkgs := []packageInfo{
		{ImportPath: m.Path + "/sub/imported", Dir: filepath.Join(root, "sub/imported"), Module: m},
		{ImportPath: m.Path + "/sub", Dir: filepath.Join(root, "sub"), Module: m},
	}
	var stream bytes.Buffer
	for _, p := range pkgs {
		if err := json.NewEncoder(&stream).Encode(p); err != nil {
			t.Fatal(err)
		}
	}
	c := collector{out: t.TempDir(), seenDirs: map[string]bool{}, seenFiles: map[string]string{}}
	if err := c.collect(json.NewDecoder(&stream)); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range c.records {
		got = append(got, r.Source)
		data, err := os.ReadFile(filepath.Join(c.out, r.File))
		if err != nil || string(data) != files[r.Source] {
			t.Fatalf("notice %s was not preserved verbatim: %q, %v", r.Source, data, err)
		}
	}
	sort.Strings(got)
	want := []string{"AUTHORS", "LICENSE", "NOTICE.md", "PATENTS", "edl-v10", "epl-v20", "sub/LICENSE-MIT", "sub/imported/LICENSE.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected notices = %v, want %v", got, want)
	}
	if err := c.ancestors(root, filepath.Dir(root), m.Path, m.Version); err == nil {
		t.Fatal("accepted package outside module")
	}
}

func TestTargetOnlyEnvironment(t *testing.T) {
	env := []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOPATH=/cache", "GOFLAGS=-tags=custom"}
	original := append([]string(nil), env...)
	got := targetEnv(env, "android", "arm64", "1")
	want := []string{"GOPATH=/cache", "GOFLAGS=-tags=custom", "GOOS=android", "GOARCH=arm64", "CGO_ENABLED=1"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(env, original) {
		t.Fatalf("target overrides = %v; host env = %v", got, env)
	}
	if got := targetEnv(env, "", "", ""); !reflect.DeepEqual(got, env) {
		t.Fatalf("desktop defaults changed: %v", got)
	}
}

func TestSQLiteBundledNotice(t *testing.T) {
	root := t.TempDir()
	intro := "/* SQLite version 3.53.4. */"
	dedication := "/*\n** The author disclaims copyright to this source code.\n** May you do good and not evil.\n*/"
	source := "#ifndef USE_LIBSQLITE3\n" + intro + "\n#define SQLITE_CORE 1\n/************** Begin file sqliteInt.h ***************************************/\n" + dedication + "\nint unrelated_code;\n"
	if err := os.WriteFile(filepath.Join(root, "sqlite3-binding.c"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	c := collector{out: t.TempDir(), seenFiles: map[string]string{}}
	if err := c.sqliteNotice(root, "github.com/mattn/go-sqlite3", "v1.14.52"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.out, c.records[0].File))
	if err != nil || string(data) != intro+"\n\n"+dedication+"\n" {
		t.Fatalf("SQLite notice = %q, %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(root, "sqlite3-binding.c"), []byte(strings.ReplaceAll(source, "The author disclaims copyright", "Changed license")), 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.sqliteNotice(root, "github.com/mattn/go-sqlite3", "v1.14.52"); err == nil {
		t.Fatal("silently accepted a changed SQLite dedication")
	}
}
