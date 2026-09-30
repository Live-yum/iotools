package engine

import (
	"bytes"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadCollection accepts native iotools and Slumber v4/v5 source collections.
// Loading is side-effect free: templates, files and request chains execute only when run.
func LoadCollection(path string) (*config.Collection, []byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if e != nil {
		return nil, nil, e
	}
	c, e := ParseCollection(b)
	if e == nil {
		c.SourcePath, _ = filepath.Abs(path)
	}
	return c, b, e
}
func ParseCollection(data []byte) (*config.Collection, error) {
	root, e := decodeCollectionMap(data)
	if e != nil {
		return nil, e
	}
	if _, ok := root["version"]; ok {
		return config.Parse(data)
	}
	return importSlumber(root)
}
func decodeCollectionMap(data []byte) (map[string]any, error) {
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("collection exceeds 4 MiB")
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	var root map[string]any
	if e := d.Decode(&root); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("expected exactly one YAML document")
	}
	if root == nil {
		return nil, fmt.Errorf("collection must be a mapping")
	}
	return root, nil
}
func mapValue(v any, label string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", label)
	}
	return m, nil
}
func checkFields(m map[string]any, allowed string) error {
	for k := range m {
		if strings.HasPrefix(k, ".") {
			continue
		}
		if !strings.Contains(" "+allowed+" ", " "+k+" ") {
			return fmt.Errorf("unsupported field %q (not silently discarded)", k)
		}
	}
	return nil
}
func sortedKeys(m map[string]any) []string {
	k := make([]string, 0, len(m))
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

// resolveCollectionRefs handles local JSON pointers and YAML anchors without fetching remote content.
func resolveCollectionRefs(value any, root map[string]any, stack map[string]bool, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("collection reference depth exceeds 64")
	}
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		if ref, exists := v["$ref"]; exists {
			s, ok := ref.(string)
			if !ok || !strings.HasPrefix(s, "#/") {
				return nil, fmt.Errorf("only local #/ collection references are supported")
			}
			if stack[s] {
				return nil, fmt.Errorf("cyclic collection reference %q", s)
			}
			var target any = root
			for _, part := range strings.Split(s[2:], "/") {
				part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
				m, ok := target.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid reference %q", s)
				}
				target, ok = m[part]
				if !ok {
					return nil, fmt.Errorf("missing reference %q", s)
				}
			}
			stack[s] = true
			resolved, e := resolveCollectionRefs(target, root, stack, depth+1)
			delete(stack, s)
			if e != nil {
				return nil, e
			}
			base, ok := resolved.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("collection reference target must be mapping")
			}
			for k, v := range base {
				out[k] = v
			}
		}
		for k, item := range v {
			if k == "$ref" {
				continue
			}
			resolved, e := resolveCollectionRefs(item, root, stack, depth+1)
			if e != nil {
				return nil, e
			}
			out[k] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			a, e := resolveCollectionRefs(item, root, stack, depth+1)
			if e != nil {
				return nil, e
			}
			out[i] = a
		}
		return out, nil
	default:
		return value, nil
	}
}
func importSlumber(raw map[string]any) (*config.Collection, error) {
	if e := checkFields(raw, "name profiles requests crypto"); e != nil {
		return nil, e
	}
	root := map[string]any{}
	// Dot-prefixed composition definitions are resolved only when referenced.
	for k, v := range raw {
		if strings.HasPrefix(k, ".") {
			continue
		}
		r, e := resolveCollectionRefs(v, raw, map[string]bool{}, 0)
		if e != nil {
			return nil, e
		}
		root[k] = r
	}
	c := &config.Collection{Version: 1, Profiles: map[string]map[string]string{}}
	if p, ok := root["profiles"]; ok {
		profiles, e := mapValue(p, "profiles")
		if e != nil {
			return nil, e
		}
		for _, id := range sortedKeys(profiles) {
			p, e := mapValue(profiles[id], "profile")
			if e != nil {
				return nil, e
			}
			if e = checkFields(p, "name default data"); e != nil {
				return nil, e
			}
			vars := map[string]string{}
			if data, ok := p["data"]; ok {
				m, e := mapValue(data, "profile data")
				if e != nil {
					return nil, e
				}
				for k, v := range m {
					s, e := templateString(v)
					if e != nil {
						return nil, e
					}
					vars[k] = s
				}
			}
			c.Profiles[id] = vars
			if v, exists := p["default"]; exists {
				b, ok := v.(bool)
				if !ok {
					return nil, fmt.Errorf("profile default must be boolean")
				}
				if b {
					if c.DefaultProfile != "" {
						return nil, fmt.Errorf("multiple default profiles")
					}
					c.DefaultProfile = id
				}
			}
		}
	}
	requests, e := mapValue(root["requests"], "requests")
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	var walk func(map[string]any, string) error
	walk = func(nodes map[string]any, prefix string) error {
		for _, id := range sortedKeys(nodes) {
			if id == "" || seen[id] {
				return fmt.Errorf("folder/recipe IDs must be globally unique: %q", id)
			}
			seen[id] = true
			m, e := mapValue(nodes[id], "recipe "+id)
			if e != nil {
				return e
			}
			if children, ok := m["requests"]; ok {
				if e = checkFields(m, "name requests"); e != nil {
					return e
				}
				kids, e := mapValue(children, "folder requests")
				if e != nil {
					return e
				}
				name := id
				if s, ok := m["name"].(string); ok {
					name = s
				}
				if e = walk(kids, prefix+name+" / "); e != nil {
					return e
				}
				continue
			}
			if e = checkFields(m, "name method url query headers authentication body persist timeout request_crypto request_transforms response_transform"); e != nil {
				return fmt.Errorf("recipe %s: %w", id, e)
			}
			method, ok := m["method"].(string)
			if !ok || method == "" {
				return fmt.Errorf("recipe %s: method is required", id)
			}
			endpoint, ok := m["url"].(string)
			if !ok || endpoint == "" {
				return fmt.Errorf("recipe %s: url is required", id)
			}
			r := config.Request{ID: id, Name: prefix + id, Protocol: "http", Action: strings.ToUpper(method), Endpoint: endpoint, Params: map[string]any{}}
			if s, ok := m["name"].(string); ok {
				r.Name = prefix + s
			}
			if s, ok := m["timeout"].(string); ok {
				r.Timeout = s
			}
			for _, key := range []string{"headers", "query", "persist", "request_crypto", "request_transforms", "response_transform"} {
				if v, ok := m[key]; ok {
					r.Params[key] = v
				}
			}
			if crypto, ok := root["crypto"]; ok {
				r.Params["crypto"] = crypto
			}
			if a, ok := m["authentication"]; ok && a != nil {
				a, e := mapValue(a, "authentication")
				if e != nil {
					return e
				}
				switch a["type"] {
				case "basic":
					if e = checkFields(a, "type username password"); e != nil {
						return e
					}
					if _, ok := a["username"]; !ok {
						return fmt.Errorf("basic auth requires username")
					}
					r.Params["username"] = a["username"]
					if p, ok := a["password"]; ok {
						r.Params["password"] = p
					}
				case "bearer":
					if e = checkFields(a, "type token"); e != nil {
						return e
					}
					if _, ok := a["token"]; !ok {
						return fmt.Errorf("bearer auth requires token")
					}
					r.Params["bearer"] = a["token"]
				default:
					return fmt.Errorf("unsupported authentication type %v", a["type"])
				}
			}
			if body, ok := m["body"]; ok && body != nil {
				if b, ok := body.(map[string]any); ok {
					if e = checkFields(b, "type data"); e != nil {
						return e
					}
					v, exists := b["data"]
					if !exists {
						return fmt.Errorf("body data is required")
					}
					switch b["type"] {
					case "json":
						r.Params["json"] = v
					case "form_urlencoded", "form_multipart":
						r.Params[fmt.Sprint(b["type"])] = v
					case "stream":
						r.Params["body"] = v
					default:
						return fmt.Errorf("unsupported body type %v", b["type"])
					}
				} else {
					r.Params["body"] = body
				}
			}
			c.Requests = append(c.Requests, r)
		}
		return nil
	}
	if e = walk(requests, ""); e != nil {
		return nil, e
	}
	b, e := yaml.Marshal(c)
	if e != nil {
		return nil, e
	}
	return config.Parse(b)
}

// ImportCollection converts supported external formats to a native collection.
// It never fetches URLs or executes imported scripts.
func ImportCollection(data []byte, format string) (*config.Collection, error) {
	switch strings.ToLower(format) {
	case "slumber", "v4", "v5", "iotools":
		return ParseCollection(data)
	case "v3":
		return importSlumberV3(data)
	case "rest", "http", "vscode", "jetbrains":
		return importREST(data)
	case "openapi":
		return importOpenAPI(data)
	case "insomnia":
		return importInsomnia(data)
	default:
		return nil, fmt.Errorf("unsupported import format %q", format)
	}
}
