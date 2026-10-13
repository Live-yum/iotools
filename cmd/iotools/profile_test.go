package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
)

func TestInitExplicitProfileOverridesDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "iotools.yaml")
	if err := run([]string{"--file", p, "--init"}); err != nil {
		t.Fatal(err)
	}
	c, _, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Profiles["alternate"] = map[string]string{"http": "http://127.0.0.1:18080"}
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ profile, want string }{
		{"", "http://127.0.0.1:8080/health"},
		{"local", "http://127.0.0.1:8080/health"},
		{"alternate", "http://127.0.0.1:18080/health"},
	} {
		t.Run("profile="+tc.profile, func(t *testing.T) {
			args := []string{"--file", p, "--run", "http-get", "--dry-run"}
			if tc.profile != "" {
				args = append(args, "--profile", tc.profile)
			}
			out := captureProfileCLI(t, args)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("preview %q missing endpoint %q", out, tc.want)
			}
		})
	}
	if err := run([]string{"--file", p, "--profile", "missing", "--run", "http-get", "--dry-run"}); err == nil || !strings.Contains(err.Error(), `unknown profile "missing"`) {
		t.Fatalf("missing profile error = %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatalf("profile selection changed file: %v", err)
	}
}

func captureProfileCLI(t *testing.T, args []string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	original := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = original }()
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
