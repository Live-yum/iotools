package crypto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Rule struct {
	Type         string   `json:"type"`
	Crypto       string   `json:"crypto,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	SkipMissing  bool     `json:"skip_missing,omitempty"`
	SkipNull     bool     `json:"skip_null,omitempty"`
	SkipBlank    bool     `json:"skip_blank,omitempty"`
	Parse        string   `json:"parse,omitempty"`
	TextEncoding string   `json:"text_encoding,omitempty"`
}
type token struct {
	field string
	index int
	kind  byte
}

func path(s string) ([]token, error) {
	fail := errors.New("JSONPath 无效：仅支持 $、.字段、[索引]、[*]、[\"字段\"]")
	if len(s) == 0 || s[0] != '$' {
		return nil, fail
	}
	var out []token
	for i := 1; i < len(s); {
		switch s[i] {
		case '.':
			i++
			start := i
			for i < len(s) && s[i] != '.' && s[i] != '[' {
				i++
			}
			if start == i {
				return nil, fail
			}
			f := s[start:i]
			if strings.ContainsAny(f, " ]*$?") {
				return nil, fail
			}
			out = append(out, token{field: f, kind: 'f'})
		case '[':
			i++
			if i >= len(s) {
				return nil, fail
			}
			if s[i] == '"' {
				start := i
				i++
				escaped := false
				for i < len(s) {
					if s[i] == '"' && !escaped {
						break
					}
					if s[i] == '\\' && !escaped {
						escaped = true
					} else {
						escaped = false
					}
					i++
				}
				if i >= len(s) {
					return nil, fail
				}
				var f string
				if json.Unmarshal([]byte(s[start:i+1]), &f) != nil {
					return nil, fail
				}
				i++
				if i >= len(s) || s[i] != ']' {
					return nil, fail
				}
				i++
				out = append(out, token{field: f, kind: 'f'})
			} else {
				start := i
				for i < len(s) && s[i] != ']' {
					i++
				}
				if i >= len(s) {
					return nil, fail
				}
				v := s[start:i]
				i++
				if v == "*" {
					out = append(out, token{kind: '*'})
				} else {
					n, e := strconv.Atoi(v)
					if e != nil || n < 0 || strconv.Itoa(n) != v {
						return nil, fail
					}
					out = append(out, token{kind: 'i', index: n})
				}
			}
		default:
			return nil, fail
		}
	}
	return out, nil
}
func parseJSON(b []byte) (any, error) {
	if !utf8.Valid(b) {
		return nil, errors.New("JSON 不是有效 UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, errors.New("JSON 解析失败（内容已隐藏）")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("JSON 含多余内容")
	}
	return v, nil
}

// Transform derives a transactional view. Errors return no partial output and raw is unchanged.
func Transform(raw []byte, codecs map[string]Config, rules []Rule) ([]byte, error) {
	if len(rules) == 0 {
		return bytes.Clone(raw), nil
	}
	var root any
	parsed := false
	visited := map[string]Rule{}
	for index, r := range rules {
		if r.Scope == "" {
			r.Scope = "fields"
		}
		if r.Scope != "body" && r.Scope != "fields" {
			return nil, errors.New("转换 scope 无效")
		}
		if r.Type != "decode" && r.Type != "decrypt" && r.Type != "parse_json" && r.Type != "encode" && r.Type != "encrypt" {
			return nil, errors.New("转换 type 无效")
		}
		var c Config
		if r.Type != "parse_json" {
			var ok bool
			c, ok = codecs[r.Crypto]
			if !ok {
				return nil, errors.New("转换引用不存在的 crypto")
			}
			if e := c.Validate(); e != nil {
				return nil, e
			}
			if (r.Type == "decrypt" || r.Type == "encrypt") && !c.IsAES() {
				return nil, errors.New("encrypt/decrypt 仅适用于 AES")
			}
		} else if r.Crypto != "" || r.Scope != "fields" {
			return nil, errors.New("parse_json 要求 fields 且不能设置 crypto")
		}
		convert := func(b []byte) ([]byte, error) {
			if r.Type == "encode" || r.Type == "encrypt" {
				return c.Encode(b)
			}
			return c.Decode(b)
		}
		if r.Scope == "body" {
			if index != 0 || r.Parse != "json" || len(r.Paths) > 0 || r.SkipMissing || r.SkipBlank || r.SkipNull || (r.TextEncoding != "" && r.TextEncoding != "utf8" && r.TextEncoding != "utf8-sig") {
				return nil, errors.New("body 转换必须为首步并设置 parse: json，不能设置 paths/skip")
			}
			b, e := convert(raw)
			if e != nil {
				return nil, e
			}
			if r.TextEncoding == "utf8-sig" {
				b = bytes.TrimPrefix(b, []byte{239, 187, 191})
			}
			root, e = parseJSON(b)
			if e != nil {
				return nil, e
			}
			parsed = true
			continue
		}
		if len(r.Paths) == 0 || r.Parse != "" || r.TextEncoding != "" {
			return nil, errors.New("fields 转换要求 paths，不能设置 parse/text_encoding")
		}
		if !parsed {
			var e error
			root, e = parseJSON(raw)
			if e != nil {
				return nil, e
			}
			parsed = true
		}
		for _, selector := range r.Paths {
			tokens, e := path(selector)
			if e != nil {
				return nil, e
			}
			var walk func(any, []token, string) (any, error)
			walk = func(v any, t []token, p string) (any, error) {
				if len(t) == 0 {
					if prev, ok := visited[p]; ok {
						prev.Paths = nil
						cur := r
						cur.Paths = nil
						if reflect.DeepEqual(prev, cur) {
							return v, nil
						}
						return nil, errors.New("多个转换对同一字段操作冲突")
					}
					visited[p] = r
					if v == nil && r.SkipNull {
						return v, nil
					}
					s, ok := v.(string)
					if !ok {
						return nil, errors.New("转换目标必须为字符串")
					}
					if r.SkipBlank && strings.TrimSpace(s) == "" {
						return v, nil
					}
					if r.Type == "parse_json" {
						return parseJSON([]byte(s))
					}
					b, e := convert([]byte(s))
					if e != nil {
						return nil, e
					}
					if !utf8.Valid(b) {
						return nil, errors.New("转换结果不是有效 UTF-8")
					}
					return string(b), nil
				}
				tok := t[0]
				missing := func() (any, error) {
					if r.SkipMissing {
						return v, nil
					}
					return nil, errors.New("JSONPath 未找到字段或索引")
				}
				switch tok.kind {
				case 'f':
					m, ok := v.(map[string]any)
					if !ok {
						return nil, errors.New("JSONPath 字段要求对象")
					}
					x, ok := m[tok.field]
					if !ok {
						return missing()
					}
					key := strings.ReplaceAll(strings.ReplaceAll(tok.field, "~", "~0"), "/", "~1")
					x, e := walk(x, t[1:], p+"/"+key)
					if e != nil {
						return nil, e
					}
					m[tok.field] = x
				case 'i', '*':
					a, ok := v.([]any)
					if !ok {
						return nil, errors.New("JSONPath 索引要求数组")
					}
					start, end := tok.index, tok.index+1
					if tok.kind == '*' {
						start = 0
						end = len(a)
					}
					if start >= len(a) && tok.kind != '*' {
						return missing()
					}
					for n := start; n < end; n++ {
						x, e := walk(a[n], t[1:], p+"/"+strconv.Itoa(n))
						if e != nil {
							return nil, e
						}
						a[n] = x
					}
				}
				return v, nil
			}
			root, e = walk(root, tokens, "")
			if e != nil {
				return nil, e
			}
		}
	}
	return json.MarshalIndent(root, "", "  ")
}
