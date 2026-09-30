package engine

import (
	"bytes"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type collectionReferences struct {
	documents    map[string]*yaml.Node
	active       map[*yaml.Node]bool
	resolved     map[*yaml.Node]*yaml.Node
	bytes, steps int
}

func (r *collectionReferences) parse(data []byte) (*yaml.Node, error) {
	r.bytes += len(data)
	if len(data) > 4<<20 || r.bytes > 16<<20 {
		return nil, fmt.Errorf("集合引用超过4MiB单文件/16MiB总量")
	}
	var doc yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(data))
	if e := d.Decode(&doc); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("需要单份YAML文档")
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("空集合文档")
	}
	return doc.Content[0], nil
}
func (r *collectionReferences) source(path string) (*yaml.Node, error) {
	if n := r.documents[path]; n != nil {
		return n, nil
	}
	if len(r.documents) >= 64 {
		return nil, fmt.Errorf("集合引用最多64个文件")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if e != nil {
		return nil, e
	}
	n, e := r.parse(data)
	if e != nil {
		return nil, e
	}
	r.documents[path] = n
	return n, nil
}
func (r *collectionReferences) resolve(node *yaml.Node, path string, depth int) (*yaml.Node, error) {
	r.steps++
	if depth > 64 || r.steps > 200000 {
		return nil, fmt.Errorf("集合引用深度/展开数量超过限制")
	}
	if out := r.resolved[node]; out != nil {
		return out, nil
	}
	if r.active[node] {
		return nil, fmt.Errorf("循环集合引用（%s:%d）", path, node.Line)
	}
	r.active[node] = true
	defer delete(r.active, node)
	out := *node
	out.Content = nil
	if node.Kind == yaml.AliasNode {
		return r.resolve(node.Alias, path, depth+1)
	}
	if node.Kind == yaml.MappingNode {
		positions := map[string]int{}
		seen := map[string]bool{}
		put := func(k, v *yaml.Node) {
			if i, ok := positions[k.Value]; ok {
				out.Content[i+1] = v
			} else {
				positions[k.Value] = len(out.Content)
				out.Content = append(out.Content, k, v)
			}
		}
		for i := 0; i < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			if seen[k.Value] {
				return nil, fmt.Errorf("重复YAML键 %s", k.Value)
			}
			seen[k.Value] = true
			if k.Value == "$ref" {
				if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
					return nil, fmt.Errorf("$ref必须是字符串")
				}
				base, pointer, ok := strings.Cut(v.Value, "#")
				if !ok || !strings.HasPrefix(pointer, "/") {
					return nil, fmt.Errorf("$ref需要文件路径#/路径")
				}
				targetPath := path
				if base != "" {
					if path == "" {
						return nil, fmt.Errorf("跨文件引用需要实际集合路径")
					}
					if strings.Contains(base, "://") {
						return nil, fmt.Errorf("集合引用只支持本地文件，不进行网络下载")
					}
					if strings.HasPrefix(base, "~/") {
						home, e := os.UserHomeDir()
						if e != nil {
							return nil, e
						}
						base = filepath.Join(home, base[2:])
					}
					if !filepath.IsAbs(base) {
						base = filepath.Join(filepath.Dir(path), base)
					}
					targetPath = filepath.Clean(base)
				}
				target := r.documents[targetPath]
				if target == nil {
					var e error
					target, e = r.source(targetPath)
					if e != nil {
						return nil, e
					}
				}
				for _, part := range strings.Split(pointer[1:], "/") {
					if part == "" {
						return nil, fmt.Errorf("引用路径不能包含空段")
					}
					// A referenced ancestor may itself be a reference; resolve it before descent.
					if target.Kind == yaml.MappingNode {
						for j := 0; j < len(target.Content); j += 2 {
							if target.Content[j].Value == "$ref" {
								var e error
								target, e = r.resolve(target, targetPath, depth+1)
								if e != nil {
									return nil, e
								}
								break
							}
						}
					}
					var next *yaml.Node
					switch target.Kind {
					case yaml.MappingNode:
						for j := 0; j < len(target.Content); j += 2 {
							if target.Content[j].Value == part {
								next = target.Content[j+1]
								break
							}
						}
					case yaml.SequenceNode:
						index, e := strconv.Atoi(part)
						if e == nil && index >= 0 && index < len(target.Content) {
							next = target.Content[index]
						}
					}
					if next == nil {
						return nil, fmt.Errorf("引用路径不存在：%s", v.Value)
					}
					target = next
				}
				resolved, e := r.resolve(target, targetPath, depth+1)
				if e != nil {
					return nil, e
				}
				if len(node.Content) == 2 {
					r.resolved[node] = resolved
					return resolved, nil
				}
				if resolved.Kind != yaml.MappingNode {
					return nil, fmt.Errorf("$ref与其他字段合并时目标必须是映射")
				}
				for j := 0; j < len(resolved.Content); j += 2 {
					put(resolved.Content[j], resolved.Content[j+1])
				}
			} else {
				resolved, e := r.resolve(v, path, depth+1)
				if e != nil {
					return nil, e
				}
				put(k, resolved)
			}
		}
	} else {
		for _, child := range node.Content {
			resolved, e := r.resolve(child, path, depth+1)
			if e != nil {
				return nil, e
			}
			out.Content = append(out.Content, resolved)
		}
	}
	r.resolved[node] = &out
	return &out, nil
}
func resolveCollectionDocument(data []byte, path string) ([]byte, error) {
	if path != "" {
		var e error
		path, e = filepath.Abs(path)
		if e != nil {
			return nil, e
		}
	}
	r := &collectionReferences{documents: map[string]*yaml.Node{}, active: map[*yaml.Node]bool{}, resolved: map[*yaml.Node]*yaml.Node{}}
	root, e := r.parse(data)
	if e != nil {
		return nil, e
	}
	r.documents[path] = root
	resolved, e := r.resolve(root, path, 0)
	if e != nil {
		return nil, e
	}
	return yaml.Marshal(resolved)
}
