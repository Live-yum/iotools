package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
	"io"
	"strings"
)

// importSlumberV3 migrates legacy YAML tags and named chains to v4 expressions.
func importSlumberV3(data []byte) (*config.Collection, error) {
	if len(data) > maxBody {
		return nil, fmt.Errorf("collection exceeds 4 MiB")
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if e := d.Decode(&node); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("expected exactly one YAML document")
	}
	value, e := v3Node(&node, 0)
	if e != nil {
		return nil, e
	}
	root, e := mapValue(value, "v3 collection")
	if e != nil {
		return nil, e
	}
	if e = checkFields(root, "name profiles chains requests"); e != nil {
		return nil, e
	}
	chains, _ := root["chains"].(map[string]any)
	compiled := map[string]string{}
	active := map[string]bool{}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var expr func(string) (string, error)
	var compileChain func(string) (string, error)
	expr = func(s string) (string, error) {
		parts := []string{}
		for len(s) > 0 {
			i := strings.Index(s, "{{")
			if i < 0 {
				parts = append(parts, quote(s))
				break
			}
			if i > 0 {
				parts = append(parts, quote(s[:i]))
			}
			j := strings.Index(s[i+2:], "}}")
			if j < 0 {
				return "", fmt.Errorf("unclosed v3 template")
			}
			key := strings.TrimSpace(s[i+2 : i+2+j])
			var part string
			if strings.HasPrefix(key, "chains.") {
				var e error
				part, e = compileChain(strings.TrimPrefix(key, "chains."))
				if e != nil {
					return "", e
				}
			} else if strings.HasPrefix(key, "env.") {
				part = "env(" + quote(strings.TrimPrefix(key, "env.")) + ")"
			} else {
				if _, e := parseExpression(key); e != nil {
					return "", e
				}
				part = key
			}
			parts = append(parts, part)
			s = s[i+2+j+2:]
		}
		if len(parts) == 0 {
			return `""`, nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "concat([" + strings.Join(parts, ",") + "])", nil
	}
	compileChain = func(id string) (string, error) {
		if s, ok := compiled[id]; ok {
			return s, nil
		}
		if active[id] {
			return "", fmt.Errorf("cyclic v3 chain %q", id)
		}
		active[id] = true
		defer delete(active, id)
		m, e := mapValue(chains[id], "chain "+id)
		if e != nil {
			return "", e
		}
		if e = checkFields(m, "source sensitive selector selector_mode content_type trim"); e != nil {
			return "", e
		}
		source, e := mapValue(m["source"], "chain source")
		if e != nil {
			return "", e
		}
		arg := func(k string) (string, error) {
			v, ok := source[k]
			if !ok {
				return "", fmt.Errorf("chain source missing %s", k)
			}
			s, e := templateString(v)
			if e != nil {
				return "", e
			}
			return expr(s)
		}
		var output string
		switch source["type"] {
		case "env":
			a, e := arg("variable")
			if e != nil {
				return "", e
			}
			output = "env(" + a + ")"
		case "file":
			a, e := arg("path")
			if e != nil {
				return "", e
			}
			output = "file(" + a + ")"
		case "request":
			recipe, ok := source["recipe"].(string)
			if !ok {
				return "", fmt.Errorf("request chain requires recipe")
			}
			trigger := "never"
			if raw, ok := source["trigger"]; ok {
				if m, ok := raw.(map[string]any); ok && m["type"] == "expire" {
					trigger = fmt.Sprint(m["data"])
				} else {
					trigger = fmt.Sprint(raw)
				}
			}
			output = "response(" + quote(recipe) + ",trigger=" + quote(trigger) + ")"
			if section, ok := source["section"].(map[string]any); ok {
				if section["type"] != "header" {
					return "", fmt.Errorf("unsupported chain section")
				}
				header, e := expr(fmt.Sprint(section["data"]))
				if e != nil {
					return "", e
				}
				output = "response_header(" + quote(recipe) + "," + header + ",trigger=" + quote(trigger) + ")"
			}
		case "prompt":
			kwargs := []string{}
			for _, key := range []string{"message", "default"} {
				if _, ok := source[key]; ok {
					a, e := arg(key)
					if e != nil {
						return "", e
					}
					kwargs = append(kwargs, key+"="+a)
				}
			}
			output = "prompt(" + strings.Join(kwargs, ",") + ")"
		case "select":
			options, ok := source["options"]
			if !ok {
				return "", fmt.Errorf("select chain requires options")
			}
			var a string
			if values, ok := options.([]any); ok {
				parts := []string{}
				for _, v := range values {
					s, e := expr(fmt.Sprint(v))
					if e != nil {
						return "", e
					}
					parts = append(parts, s)
				}
				a = "[" + strings.Join(parts, ",") + "]"
			} else {
				s, e := expr(fmt.Sprint(options))
				if e != nil {
					return "", e
				}
				a = "json_parse(" + s + ")"
			}
			output = "select(" + a
			if _, ok := source["message"]; ok {
				a, e := arg("message")
				if e != nil {
					return "", e
				}
				output += ",message=" + a
			}
			output += ")"
		case "command":
			return "", fmt.Errorf("v3 command chains are disabled in the portable runtime")
		default:
			return "", fmt.Errorf("unsupported v3 chain source %v", source["type"])
		}
		if selector, ok := m["selector"].(string); ok {
			mode := "auto"
			if s, ok := m["selector_mode"].(string); ok {
				mode = s
			}
			output += " | jsonpath(" + quote(selector) + ",mode=" + quote(mode) + ")"
		}
		if trim, ok := m["trim"].(string); ok && trim != "none" {
			output += " | trim(mode=" + quote(trim) + ")"
		}
		if m["sensitive"] == true {
			output += " | sensitive()"
		}
		compiled[id] = output
		return output, nil
	}
	delete(root, "chains")
	var convert func(any) (any, error)
	convert = func(v any) (any, error) {
		switch x := v.(type) {
		case string:
			if !strings.Contains(x, "{{") {
				return x, nil
			}
			s, e := expr(x)
			if e != nil {
				return nil, e
			}
			return "{{ " + s + " }}", nil
		case []any:
			out := make([]any, len(x))
			for i, v := range x {
				a, e := convert(v)
				if e != nil {
					return nil, e
				}
				out[i] = a
			}
			return out, nil
		case map[string]any:
			out := map[string]any{}
			for k, v := range x {
				if k == "query" {
					if _, ok := v.([]any); ok {
						out[k] = v
						continue
					}
				}
				a, e := convert(v)
				if e != nil {
					return nil, e
				}
				out[k] = a
			}
			if list, ok := out["query"].([]any); ok {
				q := map[string]any{}
				for _, item := range list {
					s, ok := item.(string)
					if !ok {
						return nil, fmt.Errorf("v3 query entries must be strings")
					}
					key, val, ok := strings.Cut(s, "=")
					if !ok {
						val = ""
					}
					converted, e := convert(val)
					if e != nil {
						return nil, e
					}
					val = converted.(string)
					if old, ok := q[key]; ok {
						if list, ok := old.([]any); ok {
							q[key] = append(list, val)
						} else {
							q[key] = []any{old, val}
						}
					} else {
						q[key] = val
					}
				}
				out["query"] = q
			}
			return out, nil
		}
		return v, nil
	}
	value, e = convert(root)
	if e != nil {
		return nil, e
	}
	return importSlumber(value.(map[string]any))
}
func v3Node(n *yaml.Node, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("v3 YAML nesting exceeds 64")
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) != 1 {
			return nil, fmt.Errorf("invalid v3 YAML")
		}
		return v3Node(n.Content[0], depth+1)
	}
	if n.Kind == yaml.AliasNode {
		return v3Node(n.Alias, depth+1)
	}
	var v any
	switch n.Kind {
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if _, ok := m[k]; ok {
				return nil, fmt.Errorf("duplicate YAML key %s", k)
			}
			a, e := v3Node(n.Content[i+1], depth+1)
			if e != nil {
				return nil, e
			}
			m[k] = a
		}
		v = m
	case yaml.SequenceNode:
		a := make([]any, len(n.Content))
		for i, node := range n.Content {
			r, e := v3Node(node, depth+1)
			if e != nil {
				return nil, e
			}
			a[i] = r
		}
		v = a
	case yaml.ScalarNode:
		copy := *n
		if strings.HasPrefix(copy.Tag, "!") && !strings.HasPrefix(copy.Tag, "!!") {
			copy.Tag = "!!str"
		}
		if e := copy.Decode(&v); e != nil {
			return nil, e
		}
	default:
		return nil, fmt.Errorf("unsupported v3 YAML node")
	}
	tag := strings.TrimPrefix(n.Tag, "!")
	if strings.HasPrefix(n.Tag, "!!") || n.Tag == "" {
		return v, nil
	}
	switch tag {
	case "folder":
		return v, nil
	case "request":
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("request tag requires mapping")
		}
		if _, ok := m["recipe"]; ok {
			m["type"] = "request"
		}
		return m, nil
	case "json", "form_urlencoded", "form_multipart":
		return map[string]any{"type": tag, "data": v}, nil
	case "basic", "env", "file", "prompt", "select", "command":
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s tag requires mapping", tag)
		}
		m["type"] = tag
		return m, nil
	case "bearer":
		return map[string]any{"type": "bearer", "token": v}, nil
	case "expire", "header":
		return map[string]any{"type": tag, "data": v}, nil
	default:
		return nil, fmt.Errorf("unsupported v3 YAML tag %q", n.Tag)
	}
}
