package mobileapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
)

// Java's JSON number implementations disagree beyond 53 bits. Decimal strings
// keep exact integers intact throughout nested structs, variants and maps.
func wireMarshal(value any) ([]byte, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	var tree any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&tree); e != nil {
		return nil, e
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			if !strings.ContainsAny(string(x), ".eE") {
				n, ok := new(big.Int).SetString(string(x), 10)
				if ok && n.BitLen() > 53 {
					return string(x)
				}
			}
		case []any:
			for i, v := range x {
				x[i] = walk(v)
			}
		case map[string]any:
			// Editable HTTP JSON is a payload, not a display metric. Converting
			// its integer tokens into strings would change the next wire request.
			// Flutter's exact reply decoder preserves this specific subtree.
			httpRequest := x["protocol"] == "http"
			for k, v := range x {
				if httpRequest && k == "params" {
					if params, ok := v.(map[string]any); ok {
						for key, value := range params {
							if key != "json" {
								params[key] = walk(value)
							}
						}
						continue
					}
				}
				x[k] = walk(v)
			}
		}
		return v
	}
	return json.Marshal(walk(tree))
}
func sensitiveName(name string) bool {
	name = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
	switch name {
	case "password", "passwd", "token", "accesstoken", "refreshtoken", "bearer", "authorization", "proxyauthorization", "cookie", "setcookie", "secret", "clientsecret", "apikey", "key", "iv":
		return true
	}
	return strings.HasSuffix(name, "password") || strings.HasSuffix(name, "token") || strings.HasSuffix(name, "secret") || strings.HasSuffix(name, "bearer")
}
func redactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		copy := map[string]any{}
		for k, x := range v {
			if sensitiveName(k) {
				copy[k] = "••••••"
			} else {
				copy[k] = redactValue(x)
			}
		}
		return copy
	case []any:
		copy := make([]any, len(v))
		for i, x := range v {
			copy[i] = redactValue(x)
		}
		return copy
	}
	return value
}
func displayRequest(r config.Request) config.Request {
	out := r
	out.Params = map[string]any{}
	for key, value := range r.Params {
		if key == "crypto" {
			out.Params[key] = "已隐藏密钥定义"
		} else if sensitiveName(key) && !(r.Protocol == "kafka" && key == "key") {
			out.Params[key] = "••••••"
		} else {
			out.Params[key] = redactValue(value)
		}
	}
	if parsed, e := url.Parse(r.Endpoint); e == nil {
		if parsed.User != nil {
			parsed.User = url.User("已隐藏")
		}
		query := parsed.Query()
		for key := range query {
			if sensitiveName(key) {
				query.Set(key, "已隐藏")
			}
		}
		parsed.RawQuery = query.Encode()
		out.Endpoint = parsed.String()
	}
	return out
}

// Reject duplicate keys and excessive nesting before unmarshalling DTOs. A
// duplicated action/token/endpoint must never acquire last-value-wins meaning.
func validateJSONStructure(input string) error {
	d := json.NewDecoder(strings.NewReader(input))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds 64 levels")
		}
		token, e := d.Token()
		if e != nil {
			return e
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				if !ok {
					return fmt.Errorf("JSON object key must be text")
				}
				if seen[key] {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				seen[key] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, e = d.Token()
		return e
	}
	if e := value(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("expected exactly one JSON command")
	}
	return nil
}
