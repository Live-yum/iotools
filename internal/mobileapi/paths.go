package mobileapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"go.yaml.in/yaml/v3"
)

func (s *Session) resolvePrivate(name string) (string, error) {
	if strings.TrimSpace(name) == "" || len(name) > 4096 || strings.ContainsRune(name, 0) || strings.Contains(name, "://") {
		return "", errors.New("a private local file path is required")
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(s.root, name)
	}
	name = filepath.Clean(name)
	return name, s.privatePath(name)
}

// Check every existing component so a symlink cannot escape the private root.
func (s *Session) privatePath(name string) error {
	if !filepath.IsAbs(name) {
		return errors.New("file path must be absolute")
	}
	rel, e := filepath.Rel(s.root, filepath.Clean(name))
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("file path is outside the app-private directory")
	}
	current := s.root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink file paths are not supported")
		}
	}
	return nil
}
func (s *Session) prepareRequest(r *config.Request) error { return s.scopeRequest(r, false) }
func (s *Session) preparePreviewRequest(r *config.Request) error {
	return s.scopeRequest(r, r.Protocol == "http")
}
func (s *Session) scopeRequest(r *config.Request, templates bool) error {
	if r.Protocol == "modbus" && strings.HasPrefix(r.Endpoint, "rtu://") && !s.options.NativeSerial {
		return errors.New("当前宿主不支持本地串口路径，请选择已授权 USB 设备或 RTU-over-TCP")
	}
	for key, value := range r.Params {
		if key == "next_config" || strings.HasSuffix(key, "_file") {
			text, ok := value.(string)
			if !ok || text == "" {
				return fmt.Errorf("%s must be a private file path", key)
			}
			// HTTP workflow resolves templates before this final check. A source preview
			// cannot authorize a path whose value is still dynamic.
			if strings.Contains(text, "{{") {
				if templates {
					continue
				}
				return fmt.Errorf("%s requires a concrete app-private path", key)
			}
			name, e := s.resolvePrivate(text)
			if e != nil {
				return fmt.Errorf("%s: %w", key, e)
			}
			r.Params[key] = name
		}
	}
	return nil
}
func (s *Session) parseCollection(data []byte) (*config.Collection, error) {
	visited := map[string]bool{}
	total := 0
	var inspect func([]byte, string, int) error
	inspect = func(data []byte, path string, depth int) error {
		total += len(data)
		if depth > 64 || len(data) > 4<<20 || total > 16<<20 {
			return errors.New("collection reference limit exceeded")
		}
		var doc yaml.Node
		if e := yaml.Unmarshal(config.NormalizeJSONForYAML(data), &doc); e != nil {
			return e
		}
		seen := map[*yaml.Node]bool{}
		steps := 0
		var walk func(*yaml.Node, int) error
		walk = func(n *yaml.Node, nesting int) error {
			steps++
			if nesting > 64 || steps > 200000 {
				return errors.New("collection nesting limit exceeded")
			}
			if seen[n] {
				return nil
			}
			seen[n] = true
			if n.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(n.Content); i += 2 {
					if n.Content[i].Value == "$ref" {
						value := n.Content[i+1].Value
						base, _, found := strings.Cut(value, "#")
						if found && base != "" {
							if strings.HasPrefix(base, "~/") {
								return errors.New("集合引用必须使用应用私有目录路径，不能使用主目录缩写")
							}
							if !filepath.IsAbs(base) {
								base = filepath.Join(filepath.Dir(path), base)
							}
							base = filepath.Clean(base)
							if e := s.privatePath(base); e != nil {
								return e
							}
							if !visited[base] {
								visited[base] = true
								if len(visited) > 64 {
									return errors.New("too many collection reference files")
								}
								b, e := readBounded(base, 4<<20)
								if e != nil {
									return e
								}
								if e = inspect(b, base, depth+1); e != nil {
									return e
								}
							}
						}
					}
				}
			}
			for _, child := range n.Content {
				if e := walk(child, nesting+1); e != nil {
					return e
				}
			}
			if n.Alias != nil {
				return walk(n.Alias, nesting+1)
			}
			return nil
		}
		return walk(&doc, 0)
	}
	visited[s.path] = true
	if e := inspect(data, s.path, 0); e != nil {
		return nil, e
	}
	c, e := engine.ParseCollectionAt(data, s.path)
	if e != nil {
		return nil, e
	}
	c.SourcePath = s.path
	return c, nil
}
