package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type templateExpr struct {
	kind   byte
	value  any
	name   string
	args   []*templateExpr
	kwargs map[string]*templateExpr
	keys   []string
}
type expressionParser struct {
	s          string
	pos, depth int
}

func parseExpression(s string) (*templateExpr, error) {
	p := &expressionParser{s: s}
	x, e := p.expression()
	p.space()
	if e == nil && p.pos != len(s) {
		e = fmt.Errorf("unexpected token at template offset %d", p.pos)
	}
	return x, e
}
func (p *expressionParser) space() {
	for p.pos < len(p.s) && unicode.IsSpace(rune(p.s[p.pos])) {
		p.pos++
	}
}
func (p *expressionParser) take(c byte) bool {
	p.space()
	if p.pos < len(p.s) && p.s[p.pos] == c {
		p.pos++
		return true
	}
	return false
}
func (p *expressionParser) ident() string {
	p.space()
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || p.pos > start && c >= '0' && c <= '9' {
			p.pos++
		} else {
			break
		}
	}
	return p.s[start:p.pos]
}
func (p *expressionParser) expression() (*templateExpr, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 64 {
		return nil, fmt.Errorf("template expression nesting exceeds 64")
	}
	x, e := p.atom()
	if e != nil {
		return nil, e
	}
	for p.take('|') {
		next, e := p.atom()
		if e != nil {
			return nil, e
		}
		if next.kind != 'c' {
			return nil, fmt.Errorf("pipe right side must be a function call")
		}
		next.args = append(next.args, x)
		x = next
	}
	return x, nil
}
func (p *expressionParser) atom() (*templateExpr, error) {
	p.space()
	if p.pos >= len(p.s) {
		return nil, fmt.Errorf("incomplete template expression")
	}
	c := p.s[p.pos]
	binary := c == 'b' && p.pos+1 < len(p.s) && (p.s[p.pos+1] == '\'' || p.s[p.pos+1] == '"')
	if c == '\'' || c == '"' || binary {
		if binary {
			p.pos++
			c = p.s[p.pos]
		}
		p.pos++
		var b strings.Builder
		for p.pos < len(p.s) {
			if p.s[p.pos] == c {
				p.pos++
				if binary {
					return &templateExpr{kind: 'l', value: []byte(b.String())}, nil
				}
				return &templateExpr{kind: 'l', value: b.String()}, nil
			}
			if p.s[p.pos] == '\\' {
				if p.pos+1 >= len(p.s) {
					return nil, fmt.Errorf("unterminated string escape")
				}
				r, multibyte, tail, e := strconv.UnquoteChar(p.s[p.pos:], c)
				if e != nil {
					return nil, fmt.Errorf("invalid template string escape")
				}
				p.pos = len(p.s) - len(tail)
				if binary && !multibyte {
					b.WriteByte(byte(r))
				} else {
					b.WriteRune(r)
				}
			} else {
				r, n := utf8.DecodeRuneInString(p.s[p.pos:])
				b.WriteRune(r)
				p.pos += n
			}
		}
		return nil, fmt.Errorf("unterminated template string")
	}
	if p.take('[') {
		x := &templateExpr{kind: 'a'}
		if p.take(']') {
			return x, nil
		}
		for {
			v, e := p.expression()
			if e != nil {
				return nil, e
			}
			x.args = append(x.args, v)
			if p.take(']') {
				return x, nil
			}
			if !p.take(',') {
				return nil, fmt.Errorf("array requires comma")
			}
			if p.take(']') {
				return x, nil
			}
		}
	}
	if p.take('{') {
		x := &templateExpr{kind: 'o'}
		if p.take('}') {
			return x, nil
		}
		for {
			k, e := p.expression()
			if e != nil {
				return nil, e
			}
			if !p.take(':') {
				return nil, fmt.Errorf("object requires colon")
			}
			v, e := p.expression()
			if e != nil {
				return nil, e
			}
			x.args = append(x.args, k, v)
			if p.take('}') {
				return x, nil
			}
			if !p.take(',') {
				return nil, fmt.Errorf("object requires comma")
			}
			if p.take('}') {
				return x, nil
			}
		}
	}
	if p.take('(') {
		x, e := p.expression()
		if e != nil {
			return nil, e
		}
		if !p.take(')') {
			return nil, fmt.Errorf("unclosed parentheses")
		}
		return x, nil
	}
	if c == '-' || c >= '0' && c <= '9' {
		start := p.pos
		p.pos++
		for p.pos < len(p.s) && strings.ContainsRune("0123456789.eE+-", rune(p.s[p.pos])) {
			p.pos++
		}
		s := p.s[start:p.pos]
		if !json.Valid([]byte(s)) {
			return nil, fmt.Errorf("invalid numeric literal")
		}
		return &templateExpr{kind: 'l', value: json.Number(s)}, nil
	}
	name := p.ident()
	if name == "" {
		return nil, fmt.Errorf("unexpected template token at %d", p.pos)
	}
	switch name {
	case "null":
		return &templateExpr{kind: 'l'}, nil
	case "true":
		return &templateExpr{kind: 'l', value: true}, nil
	case "false":
		return &templateExpr{kind: 'l', value: false}, nil
	}
	if !p.take('(') {
		return &templateExpr{kind: 'v', name: name}, nil
	}
	x := &templateExpr{kind: 'c', name: name, kwargs: map[string]*templateExpr{}}
	if p.take(')') {
		return x, nil
	}
	keywordSeen := false
	for {
		start := p.pos
		key := p.ident()
		if key != "" && p.take('=') {
			keywordSeen = true
			if _, ok := x.kwargs[key]; ok {
				return nil, fmt.Errorf("duplicate keyword %s", key)
			}
			v, e := p.expression()
			if e != nil {
				return nil, e
			}
			x.kwargs[key] = v
			x.keys = append(x.keys, key)
		} else {
			p.pos = start
			if keywordSeen {
				return nil, fmt.Errorf("positional argument follows keyword")
			}
			v, e := p.expression()
			if e != nil {
				return nil, e
			}
			x.args = append(x.args, v)
		}
		if p.take(')') {
			return x, nil
		}
		if !p.take(',') {
			return nil, fmt.Errorf("function arguments require comma")
		}
		if p.take(')') {
			return x, nil
		}
	}
}

type templateChunk struct {
	text string
	expr *templateExpr
}

func parseTemplate(s string) ([]templateChunk, error) {
	chunks := []templateChunk{}
	var text strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "{_") {
			j := i + 1
			for j < len(s) && s[j] == '_' {
				j++
			}
			if j < len(s) && s[j] == '{' {
				if j+1 < len(s) && s[j+1] == '{' {
					text.WriteString(s[i : j-1])
					i = j
					continue
				}
				text.WriteString("{" + s[i+2:j] + "{")
				i = j + 1
				continue
			}
		}
		if !strings.HasPrefix(s[i:], "{{") {
			text.WriteByte(s[i])
			i++
			continue
		}
		if text.Len() > 0 {
			chunks = append(chunks, templateChunk{text: text.String()})
			text.Reset()
		}
		start := i + 2
		j := start
		depth := 0
		quote := byte(0)
		escaped := false
		for ; j < len(s); j++ {
			c := s[j]
			if quote != 0 {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				continue
			}
			if depth == 0 && strings.HasPrefix(s[j:], "}}") {
				break
			}
			switch c {
			case '{', '[', '(':
				depth++
			case '}', ']', ')':
				depth--
			}
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced template delimiters")
			}
		}
		if j >= len(s) {
			return nil, fmt.Errorf("unclosed {{ template")
		}
		x, e := parseExpression(s[start:j])
		if e != nil {
			return nil, e
		}
		chunks = append(chunks, templateChunk{expr: x})
		i = j + 2
	}
	if text.Len() > 0 || len(chunks) == 0 {
		chunks = append(chunks, templateChunk{text: text.String()})
	}
	return chunks, nil
}
func templateString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []byte:
		if !utf8.Valid(x) {
			return "", fmt.Errorf("binary template value cannot be used as UTF-8 text")
		}
		return string(x), nil
	case nil:
		return "null", nil
	default:
		b, e := json.Marshal(v)
		return string(b), e
	}
}
func templateBytes(v any) ([]byte, error) {
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	s, e := templateString(v)
	return []byte(s), e
}
func (w *httpWorkflow) render(s string, typed bool) (any, error) {
	if len(s) > maxBody {
		return nil, fmt.Errorf("template exceeds 4 MiB")
	}
	chunks, e := parseTemplate(s)
	if e != nil {
		return nil, e
	}
	if typed && len(chunks) == 1 && chunks[0].expr != nil {
		return w.eval(chunks[0].expr)
	}
	var b strings.Builder
	for _, chunk := range chunks {
		if chunk.expr == nil {
			b.WriteString(chunk.text)
		} else {
			v, e := w.eval(chunk.expr)
			if e != nil {
				return nil, e
			}
			s, e := templateString(v)
			if e != nil {
				return nil, e
			}
			b.WriteString(s)
		}
		if b.Len() > maxBody {
			return nil, fmt.Errorf("rendered template exceeds 4 MiB")
		}
	}
	return b.String(), nil
}
func (w *httpWorkflow) walk(value any, typed bool, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("template value nesting exceeds 64")
	}
	switch v := value.(type) {
	case string:
		return w.render(v, typed)
	case map[string]any:
		out := map[string]any{}
		for _, k := range sortedKeys(v) {
			r, e := w.walk(v[k], typed, depth+1)
			if e != nil {
				return nil, e
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			r, e := w.walk(x, typed, depth+1)
			if e != nil {
				return nil, e
			}
			out[i] = r
		}
		return out, nil
	default:
		return value, nil
	}
}
func (w *httpWorkflow) eval(x *templateExpr) (any, error) {
	w.steps++
	if w.steps > 10000 {
		return nil, fmt.Errorf("template evaluation exceeds 10000 steps")
	}
	if e := w.ctx.Err(); e != nil {
		return nil, e
	}
	switch x.kind {
	case 'l':
		return x.value, nil
	case 'v':
		if v, ok := w.varsCache[x.name]; ok {
			return v, nil
		}
		s, ok := w.vars[x.name]
		if !ok {
			return nil, fmt.Errorf("unknown template field %q", x.name)
		}
		if w.varsActive[x.name] {
			return nil, fmt.Errorf("cyclic profile field %q", x.name)
		}
		w.varsActive[x.name] = true
		defer delete(w.varsActive, x.name)
		// Retain ${...} profile/environment references in legacy collections.
		r, e := w.legacyString(s)
		if e != nil {
			return nil, e
		}
		v, e := w.render(r, true)
		if e == nil {
			w.varsCache[x.name] = v
		}
		return v, e
	case 'a':
		a := make([]any, len(x.args))
		for i, arg := range x.args {
			v, e := w.eval(arg)
			if e != nil {
				return nil, e
			}
			a[i] = v
		}
		return a, nil
	case 'o':
		m := map[string]any{}
		for i := 0; i < len(x.args); i += 2 {
			k, e := w.eval(x.args[i])
			if e != nil {
				return nil, e
			}
			s, e := templateString(k)
			if e != nil {
				return nil, e
			}
			v, e := w.eval(x.args[i+1])
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		return m, nil
	case 'c':
		args := make([]any, len(x.args))
		for i, arg := range x.args {
			v, e := w.eval(arg)
			if e != nil {
				return nil, e
			}
			args[i] = v
		}
		kw := map[string]any{}
		for _, k := range x.keys {
			v, e := w.eval(x.kwargs[k])
			if e != nil {
				return nil, e
			}
			kw[k] = v
		}
		return w.call(x.name, args, kw)
	}
	return nil, fmt.Errorf("invalid template expression")
}
func (w *httpWorkflow) call(name string, a []any, kw map[string]any) (any, error) {
	signatures := map[string]struct {
		n  int
		kw string
	}{"base64": {1, "decode"}, "boolean": {1, ""}, "concat": {1, ""}, "debug": {1, ""}, "env": {1, "default"}, "file": {1, ""}, "float": {1, ""}, "index": {2, ""}, "integer": {1, ""}, "join": {2, ""}, "jq": {2, "mode"}, "json_parse": {1, ""}, "jsonpath": {2, "mode"}, "lower": {1, ""}, "upper": {1, ""}, "prompt": {0, "message default sensitive"}, "replace": {3, "regex n"}, "response": {1, "trigger view"}, "response_header": {2, "trigger"}, "select": {1, "message"}, "sensitive": {1, ""}, "slice": {3, ""}, "split": {2, "n"}, "string": {1, ""}, "trim": {1, "mode"}, "encode": {2, ""}, "decode": {2, ""}, "encrypt": {2, ""}, "decrypt": {2, ""}, "command": {1, "cwd stdin"}}
	sig, ok := signatures[name]
	if !ok {
		return nil, fmt.Errorf("unknown template function %q", name)
	}
	if len(a) != sig.n {
		return nil, fmt.Errorf("%s expects %d positional arguments", name, sig.n)
	}
	for k := range kw {
		if !strings.Contains(" "+sig.kw+" ", " "+k+" ") {
			return nil, fmt.Errorf("unknown %s keyword %q", name, k)
		}
	}
	str := func(i int) (string, error) { return templateString(a[i]) }
	kwstr := func(k, def string) (string, error) {
		if v, ok := kw[k]; ok {
			return templateString(v)
		}
		return def, nil
	}
	kwbool := func(k string) (bool, error) {
		v, ok := kw[k]
		if !ok {
			return false, nil
		}
		b, ok := v.(bool)
		if !ok {
			return false, fmt.Errorf("%s must be boolean", k)
		}
		return b, nil
	}
	switch name {
	case "command":
		return nil, fmt.Errorf("command() is disabled in the portable standalone runtime; use native template functions")
	case "debug", "sensitive":
		return a[0], nil
	case "string":
		return str(0)
	case "env":
		key, e := str(0)
		if e != nil {
			return nil, e
		}
		if value, ok := os.LookupEnv(key); ok {
			return value, nil
		}
		if v, ok := kw["default"]; ok {
			return templateString(v)
		}
		return nil, fmt.Errorf("environment variable %q is not set", key)
	case "file":
		p, e := str(0)
		if e != nil {
			return nil, e
		}
		if strings.HasPrefix(p, "~/") {
			home, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			p = filepath.Join(home, p[2:])
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(w.rootDir, p)
		}
		if w.options.ValidateFilePath != nil {
			if e := w.options.ValidateFilePath(p); e != nil {
				return nil, e
			}
		}
		f, e := os.Open(p)
		if e != nil {
			return nil, fmt.Errorf("file template: %w", e)
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("file() requires a regular file")
		}
		b, e := io.ReadAll(io.LimitReader(f, maxBody+1))
		if len(b) > maxBody {
			return nil, fmt.Errorf("file exceeds 4 MiB")
		}
		return b, e
	case "base64":
		b, e := templateBytes(a[0])
		if e != nil {
			return nil, e
		}
		decode, e := kwbool("decode")
		if e != nil {
			return nil, e
		}
		if decode {
			return base64.StdEncoding.Strict().DecodeString(string(b))
		}
		return base64.StdEncoding.EncodeToString(b), nil
	case "json_parse":
		b, e := templateBytes(a[0])
		if e != nil {
			return nil, e
		}
		return decodeJSON(b)
	case "jq", "jsonpath":
		q, e := str(0)
		if e != nil {
			return nil, e
		}
		mode, e := kwstr("mode", "auto")
		if e != nil {
			return nil, e
		}
		values, e := queryValues(w.ctx, name, q, a[1])
		if e != nil {
			return nil, e
		}
		return queryMode(values, mode)
	case "lower", "upper", "trim":
		s, e := str(0)
		if e != nil {
			return nil, e
		}
		switch name {
		case "lower":
			return strings.ToLower(s), nil
		case "upper":
			return strings.ToUpper(s), nil
		}
		mode, e := kwstr("mode", "both")
		if e != nil {
			return nil, e
		}
		switch mode {
		case "both":
			return strings.TrimSpace(s), nil
		case "start":
			return strings.TrimLeftFunc(s, unicode.IsSpace), nil
		case "end":
			return strings.TrimRightFunc(s, unicode.IsSpace), nil
		}
		return nil, fmt.Errorf("trim mode requires start, end, both")
	case "integer":
		if b, ok := a[0].(bool); ok {
			if b {
				return json.Number("1"), nil
			}
			return json.Number("0"), nil
		}
		s, e := str(0)
		if e != nil {
			return nil, e
		}
		if i, e := strconv.ParseInt(s, 10, 64); e == nil {
			return json.Number(strconv.FormatInt(i, 10)), nil
		}
		f, e := strconv.ParseFloat(s, 64)
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < math.MinInt64 || f >= math.MaxInt64 {
			return nil, fmt.Errorf("integer conversion outside i64 range")
		}
		return json.Number(strconv.FormatInt(int64(f), 10)), nil
	case "float":
		if b, ok := a[0].(bool); ok {
			if b {
				return float64(1), nil
			}
			return float64(0), nil
		}
		s, e := str(0)
		if e != nil {
			return nil, e
		}
		f, e := strconv.ParseFloat(s, 64)
		if e != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("invalid finite float")
		}
		return f, nil
	case "boolean":
		v := a[0]
		if v == nil {
			return false, nil
		}
		switch x := v.(type) {
		case bool:
			return x, nil
		case json.Number:
			f, _ := x.Float64()
			return f != 0, nil
		case float64:
			return x != 0, nil
		case int:
			return x != 0, nil
		case int64:
			return x != 0, nil
		case string:
			return len(x) > 0, nil
		case []byte:
			return len(x) > 0, nil
		case []any:
			return len(x) > 0, nil
		case map[string]any:
			return len(x) > 0, nil
		}
		return true, nil
	case "concat", "join":
		sep := ""
		var list any = a[0]
		if name == "join" {
			var e error
			sep, e = str(0)
			if e != nil {
				return nil, e
			}
			list = a[1]
		}
		values, ok := list.([]any)
		if !ok {
			return nil, fmt.Errorf("%s requires array", name)
		}
		s := make([]string, len(values))
		for i, v := range values {
			x, e := templateString(v)
			if e != nil {
				return nil, e
			}
			s[i] = x
		}
		return strings.Join(s, sep), nil
	case "index", "slice":
		return templateSequence(name, a)
	case "split":
		sep, e := str(0)
		if e != nil {
			return nil, e
		}
		s, e := str(1)
		if e != nil {
			return nil, e
		}
		n := -1
		if v, ok := kw["n"]; ok && v != nil {
			i, e := exactInt(v)
			if e != nil {
				ss, _ := templateString(v)
				i, e = strconv.ParseInt(ss, 10, 32)
			}
			if e != nil || i < 0 || i > 100000 {
				return nil, fmt.Errorf("split n requires nonnegative bounded integer")
			}
			n = int(i) + 1
		}
		list := strings.SplitN(s, sep, n)
		if sep == "" && n < 0 {
			list = append([]string{""}, append(list, "")...)
		}
		out := make([]any, len(list))
		for i, v := range list {
			out[i] = v
		}
		return out, nil
	case "replace":
		from, e := str(0)
		if e != nil {
			return nil, e
		}
		to, e := str(1)
		if e != nil {
			return nil, e
		}
		s, e := str(2)
		if e != nil {
			return nil, e
		}
		n := -1
		if v, ok := kw["n"]; ok && v != nil {
			ss, _ := templateString(v)
			i, e := strconv.ParseInt(ss, 10, 32)
			if e != nil || i < 0 {
				return nil, fmt.Errorf("replace n requires nonnegative integer")
			}
			n = int(i)
		}
		useRegex, e := kwbool("regex")
		if e != nil {
			return nil, e
		}
		if !useRegex {
			return strings.Replace(s, from, to, n), nil
		}
		re, e := regexp.Compile(from)
		if e != nil {
			return nil, e
		}
		if n < 0 {
			return re.ReplaceAllString(s, to), nil
		}
		indices := re.FindAllStringSubmatchIndex(s, n)
		var out strings.Builder
		last := 0
		for _, idx := range indices {
			out.WriteString(s[last:idx[0]])
			out.Write(re.ExpandString(nil, to, s, idx))
			last = idx[1]
		}
		out.WriteString(s[last:])
		return out.String(), nil
	case "response", "response_header":
		id, e := str(0)
		if e != nil {
			return nil, e
		}
		trigger, e := kwstr("trigger", "never")
		if e != nil {
			return nil, e
		}
		entry, e := w.response(id, trigger)
		if e != nil {
			return nil, e
		}
		if name == "response_header" {
			header, e := str(1)
			if e != nil {
				return nil, e
			}
			v := entry.Headers.Get(header)
			if len(entry.Headers.Values(header)) == 0 {
				return nil, fmt.Errorf("response header %q missing", header)
			}
			return v, nil
		}
		view, e := kwstr("view", "raw")
		if e != nil {
			return nil, e
		}
		switch view {
		case "raw":
			return entry.Body, nil
		case "transformed":
			return w.transformResponse(id, entry.Body)
		default:
			return nil, fmt.Errorf("response view requires raw or transformed")
		}
	case "prompt":
		if w.options.Prompt == nil {
			return nil, fmt.Errorf("prompt() requires an interactive prompt provider")
		}
		message, e := kwstr("message", "")
		if e != nil {
			return nil, e
		}
		def, e := kwstr("default", "")
		if e != nil {
			return nil, e
		}
		sensitive, e := kwbool("sensitive")
		if e != nil {
			return nil, e
		}
		return w.options.Prompt(w.ctx, message, def, sensitive)
	case "select":
		options, ok := a[0].([]any)
		if !ok || len(options) == 0 {
			return nil, fmt.Errorf("select() requires a nonempty array")
		}
		if w.options.Select == nil {
			return nil, fmt.Errorf("select() requires an interactive selection provider")
		}
		message, e := kwstr("message", "")
		if e != nil {
			return nil, e
		}
		return w.options.Select(w.ctx, message, options)
	case "encode", "decode", "encrypt", "decrypt":
		id, e := str(0)
		if e != nil {
			return nil, e
		}
		c, e := w.codec(id)
		if e != nil {
			return nil, e
		}
		if (name == "encrypt" || name == "decrypt") && !c.IsAES() {
			return nil, fmt.Errorf("encrypt/decrypt requires an AES codec")
		}
		b, e := templateBytes(a[1])
		if e != nil {
			return nil, e
		}
		if name == "encode" || name == "encrypt" {
			return c.Encode(b)
		}
		return c.Decode(b)
	}
	return nil, fmt.Errorf("unsupported template function")
}
func templateSequence(name string, a []any) (any, error) {
	parseIndex := func(v any) (int64, error) {
		s, e := templateString(v)
		if e != nil {
			return 0, e
		}
		return strconv.ParseInt(s, 10, 64)
	}
	start, e := parseIndex(a[0])
	if e != nil {
		return nil, e
	}
	value := a[len(a)-1]
	var chars []rune
	var seq reflect.Value
	switch x := value.(type) {
	case string:
		chars = []rune(x)
		seq = reflect.ValueOf(chars)
	case []byte:
		seq = reflect.ValueOf(x)
	case []any:
		seq = reflect.ValueOf(x)
	default:
		return nil, fmt.Errorf("index/slice requires string, bytes or array")
	}
	length := int64(seq.Len())
	if start < 0 {
		start += length
	}
	if name == "index" {
		if start < 0 || start >= length {
			return nil, nil
		}
		if chars != nil {
			return string(chars[start]), nil
		}
		if b, ok := value.([]byte); ok {
			return []byte{b[start]}, nil
		}
		return seq.Index(int(start)).Interface(), nil
	}
	stop := length
	if a[1] != nil {
		stop, e = parseIndex(a[1])
		if e != nil {
			return nil, e
		}
		if stop < 0 {
			stop += length
		}
	}
	start = max(int64(0), min(start, length))
	stop = max(start, min(max(int64(0), stop), length))
	if chars != nil {
		return string(chars[start:stop]), nil
	}
	return seq.Slice(int(start), int(stop)).Interface(), nil
}
