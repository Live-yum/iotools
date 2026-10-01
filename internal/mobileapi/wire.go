package mobileapi

import (
	"bytes"
	"encoding/json"
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
			for k, v := range x {
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
