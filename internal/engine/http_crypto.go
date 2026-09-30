package engine

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Live-yum/iotools/internal/config"
	codec "github.com/Live-yum/iotools/internal/crypto"
)

type requestTransform struct {
	Target string `json:"target"`
	Name   string `json:"name,omitempty"`
	Crypto string `json:"crypto"`
	Type   string `json:"type"`
}

func httpCodecs(r config.Request) (map[string]codec.Config, error) {
	out := map[string]codec.Config{}
	if v, ok := r.Params["crypto"]; ok {
		if e := codec.Parse(v, &out); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func transformHTTPRequest(r config.Request, body string, codecs map[string]codec.Config) (string, string, http.Header, error) {
	headers := http.Header{}
	if h, ok := r.Params["headers"].(map[string]any); ok {
		for k, v := range h {
			headers.Set(k, fmt.Sprint(v))
		}
	}
	u, e := url.Parse(r.Endpoint)
	if e != nil {
		return "", "", nil, e
	}
	q := u.Query()
	var rules []requestTransform
	if v, ok := r.Params["request_transforms"]; ok {
		if e = codec.Parse(v, &rules); e != nil {
			return "", "", nil, e
		}
	}
	for _, rule := range rules {
		c, ok := codecs[rule.Crypto]
		if !ok {
			return "", "", nil, fmt.Errorf("请求转换引用未知 crypto")
		}
		if rule.Type != "encode" && rule.Type != "encrypt" && rule.Type != "decode" && rule.Type != "decrypt" {
			return "", "", nil, fmt.Errorf("请求转换 type 无效")
		}
		if (rule.Type == "encrypt" || rule.Type == "decrypt") && !c.IsAES() {
			return "", "", nil, fmt.Errorf("encrypt/decrypt 仅适用于 AES")
		}
		convert := func(s string) (string, error) {
			var b []byte
			var e error
			if rule.Type == "encode" || rule.Type == "encrypt" {
				b, e = c.Encode([]byte(s))
			} else {
				b, e = c.Decode([]byte(s))
			}
			if e != nil {
				return "", e
			}
			if !utf8.Valid(b) {
				return "", fmt.Errorf("请求转换不是有效 UTF-8")
			}
			return string(b), nil
		}
		switch rule.Target {
		case "body":
			if rule.Name != "" {
				return "", "", nil, fmt.Errorf("body 转换不能设置 name")
			}
			body, e = convert(body)
		case "header":
			if rule.Name == "" || len(headers.Values(rule.Name)) == 0 {
				return "", "", nil, fmt.Errorf("请求转换 header 不存在")
			}
			values := headers.Values(rule.Name)
			headers.Del(rule.Name)
			for _, v := range values {
				var s string
				s, e = convert(v)
				if e != nil {
					break
				}
				if strings.ContainsAny(s, "\r\n") {
					return "", "", nil, fmt.Errorf("转换后 header 含换行")
				}
				headers.Add(rule.Name, s)
			}
		case "query":
			values, ok := q[rule.Name]
			if !ok {
				return "", "", nil, fmt.Errorf("请求转换 query 不存在")
			}
			for i, v := range values {
				values[i], e = convert(v)
				if e != nil {
					break
				}
			}
			q[rule.Name] = values
		case "json":
			var b []byte
			b, e = codec.Transform([]byte(body), codecs, []codec.Rule{{Type: rule.Type, Crypto: rule.Crypto, Paths: []string{rule.Name}}})
			body = string(b)
		default:
			return "", "", nil, fmt.Errorf("请求转换 target 要求 body/header/query/json")
		}
		if e != nil {
			return "", "", nil, e
		}
	}
	if id := r.String("request_crypto", ""); id != "" {
		c, ok := codecs[id]
		if !ok {
			return "", "", nil, fmt.Errorf("request_crypto 不存在")
		}
		b, e := c.Encode([]byte(body))
		if e != nil {
			return "", "", nil, e
		}
		body = string(b)
	}
	u.RawQuery = q.Encode()
	return u.String(), body, headers, nil
}
