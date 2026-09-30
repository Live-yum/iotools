// Package config implements source-first collections. It never executes templates.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Collection struct {
	Version  int                          `yaml:"version" json:"version"`
	Profiles map[string]map[string]string `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Requests []Request                    `yaml:"requests" json:"requests"`
}

type Request struct {
	ID       string         `yaml:"id" json:"id"`
	Name     string         `yaml:"name,omitempty" json:"name,omitempty"`
	Protocol string         `yaml:"protocol" json:"protocol"`
	Action   string         `yaml:"action" json:"action"`
	Endpoint string         `yaml:"endpoint" json:"endpoint"`
	Timeout  string         `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Params   map[string]any `yaml:"params,omitempty" json:"params,omitempty"`
}

func Parse(data []byte) (*Collection, error) {
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("collection exceeds 4 MiB")
	}
	var c Collection
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one YAML document")
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("unsupported collection version %d (expected 1)", c.Version)
	}
	seen := map[string]bool{}
	for _, r := range c.Requests {
		if r.ID == "" || seen[r.ID] {
			return nil, fmt.Errorf("request IDs must be nonempty and unique: %q", r.ID)
		}
		seen[r.ID] = true
		switch r.Protocol {
		case "http", "kafka", "mqtt", "modbus", "opcua":
		default:
			return nil, fmt.Errorf("%s: unknown protocol %q", r.ID, r.Protocol)
		}
		if r.Action == "" || r.Endpoint == "" {
			return nil, fmt.Errorf("%s: action and endpoint are required", r.ID)
		}
		if _, err := r.Duration(); err != nil {
			return nil, err
		}
	}
	return &c, nil
}
func Load(path string) (*Collection, []byte, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, nil, e
	}
	c, e := Parse(b)
	return c, b, e
}
func (r Request) Duration() (time.Duration, error) {
	if r.Timeout == "" {
		return 15 * time.Second, nil
	}
	d, e := time.ParseDuration(r.Timeout)
	if e != nil || d <= 0 || d > 24*time.Hour {
		return 0, fmt.Errorf("%s: timeout must be positive and at most 24h", r.ID)
	}
	return d, nil
}
func (r Request) String(k, def string) string {
	v, ok := r.Params[k]
	if !ok {
		return def
	}
	return fmt.Sprint(v)
}
func (r Request) Bool(k string) bool { v, _ := r.Params[k].(bool); return v }
func (r Request) Int(k string, def int) int {
	v, ok := r.Params[k]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, e := strconv.Atoi(n)
		if e == nil {
			return i
		}
	}
	return def
}
func (r Request) Strings(k string) []string {
	v := r.Params[k]
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		a := make([]string, len(x))
		for i, s := range x {
			a[i] = fmt.Sprint(s)
		}
		return a
	case string:
		return []string{x}
	}
	return nil
}
func (r Request) Mutates() bool {
	switch r.Protocol {
	case "http":
		return !strings.EqualFold(r.Action, "GET") && !strings.EqualFold(r.Action, "HEAD") && !strings.EqualFold(r.Action, "OPTIONS")
	case "mqtt":
		return r.Action == "publish" || r.Action == "clean-retained"
	case "kafka":
		return r.Action == "produce" || r.Action == "create-topic" || r.Action == "delete-topic" || r.Action == "alter-topic" || r.Action == "register-schema" || r.Action == "update-connector"
	case "modbus":
		return strings.HasPrefix(r.Action, "write")
	case "opcua":
		return r.Action == "write" || r.Action == "call"
	}
	return false
}

var template = regexp.MustCompile(`\$\{([^}]+)\}`)

func (c *Collection) Resolve(r Request, profile string) (Request, error) {
	vars := map[string]string{}
	if profile != "" {
		var ok bool
		vars, ok = c.Profiles[profile]
		if !ok {
			return r, fmt.Errorf("unknown profile %q", profile)
		}
	}
	var first error
	resolve := func(s string) string {
		return template.ReplaceAllStringFunc(s, func(m string) string {
			k := m[2 : len(m)-1]
			var value string
			var ok bool
			if strings.HasPrefix(k, "env:") {
				value, ok = os.LookupEnv(strings.TrimPrefix(k, "env:"))
			} else {
				value, ok = vars[k]
			}
			if !ok && first == nil {
				first = fmt.Errorf("unresolved variable %q", k)
			}
			return value
		})
	}
	r.Endpoint = resolve(r.Endpoint)
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return resolve(x)
		case []any:
			out := make([]any, len(x))
			for i, v := range x {
				out[i] = walk(v)
			}
			return out
		case map[string]any:
			out := map[string]any{}
			for k, v := range x {
				out[k] = walk(v)
			}
			return out
		default:
			return v
		}
	}
	if r.Params != nil {
		r.Params = walk(r.Params).(map[string]any)
	}
	return r, first
}

// Save validates before changing disk, writes a private temp file, and replaces the
// collection atomically on supported filesystems. There is no plaintext backup.
func Save(path string, data []byte) error {
	if _, e := Parse(data); e != nil {
		return e
	}
	info, e := os.Lstat(path)
	if e == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace a symlink")
	}
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".iotools-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(name, path)
}
