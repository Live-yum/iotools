package engine

import (
	"bytes"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"go.yaml.in/yaml/v3"
	"net/url"
	"regexp"
	"strings"
)

var restVariable = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

func nativeCollection(c *config.Collection) (*config.Collection, error) {
	b, e := yaml.Marshal(c)
	if e != nil {
		return nil, e
	}
	return config.Parse(b)
}
func importREST(data []byte) (*config.Collection, error) {
	if len(data) > maxBody {
		return nil, fmt.Errorf("import exceeds 4 MiB")
	}
	c := &config.Collection{Version: 1, Profiles: map[string]map[string]string{"imported": {}}, DefaultProfile: "imported"}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	name := ""
	ids := map[string]bool{}
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		i++
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "###") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "###"))
			continue
		}
		if strings.HasPrefix(line, "# @name ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "# @name "))
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			k, v, ok := strings.Cut(line[1:], "=")
			if !ok {
				return nil, fmt.Errorf("invalid REST variable")
			}
			c.Profiles["imported"][strings.TrimSpace(k)] = strings.TrimSpace(v)
			continue
		}
		if strings.HasPrefix(line, "<") || strings.HasPrefix(line, ">") {
			return nil, fmt.Errorf("REST script/file directives must be converted explicitly")
		}
		f := strings.Fields(line)
		method := "GET"
		endpoint := ""
		if len(f) == 1 {
			endpoint = f[0]
		} else if len(f) >= 2 {
			method = strings.ToUpper(f[0])
			endpoint = f[1]
		} else {
			return nil, fmt.Errorf("invalid REST request")
		}
		if !strings.Contains(" GET HEAD POST PUT PATCH DELETE OPTIONS TRACE CONNECT ", " "+method+" ") {
			return nil, fmt.Errorf("unsupported REST request method %q", method)
		}
		id := safeImportID(name)
		if id == "" {
			id = fmt.Sprintf("request_%d", len(c.Requests)+1)
		}
		base := id
		for n := 2; ids[id]; n++ {
			id = fmt.Sprintf("%s_%d", base, n)
		}
		ids[id] = true
		r := config.Request{ID: id, Name: name, Protocol: "http", Action: method, Endpoint: endpoint, Params: map[string]any{}}
		name = ""
		headers := map[string]any{}
		for i < len(lines) {
			line = strings.TrimSpace(lines[i])
			if line == "" {
				i++
				break
			}
			if strings.HasPrefix(line, "###") {
				break
			}
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				return nil, fmt.Errorf("invalid REST header at line %d", i+1)
			}
			headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			i++
		}
		if len(headers) > 0 {
			r.Params["headers"] = headers
		}
		body := []string{}
		for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "###") {
			line = lines[i]
			if strings.HasPrefix(strings.TrimSpace(line), "<") || strings.HasPrefix(strings.TrimSpace(line), ">") {
				return nil, fmt.Errorf("REST script/file directives must be converted explicitly")
			}
			body = append(body, line)
			i++
		}
		if s := strings.TrimRight(strings.Join(body, "\n"), "\n"); s != "" {
			r.Params["body"] = s
		}
		c.Requests = append(c.Requests, r)
	}
	if len(c.Requests) == 0 {
		return nil, fmt.Errorf("no REST requests found")
	}
	return nativeCollection(c)
}
func safeImportID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func importInsomnia(data []byte) (*config.Collection, error) {
	root, e := decodeCollectionMap(data)
	if e != nil {
		return nil, e
	}
	resources, ok := root["resources"].([]any)
	if !ok {
		return nil, fmt.Errorf("Insomnia resources export required")
	}
	c := &config.Collection{Version: 1, Profiles: map[string]map[string]string{}}
	convert := func(v any) any {
		if s, ok := v.(string); ok {
			return strings.ReplaceAll(s, "{{ _.", "{{ ")
		}
		return v
	}
	for _, item := range resources {
		m, e := mapValue(item, "resource")
		if e != nil {
			return nil, e
		}
		switch m["_type"] {
		case "environment":
			d, ok := m["data"].(map[string]any)
			if !ok {
				continue
			}
			id := safeImportID(fmt.Sprint(m["name"]))
			if id == "" {
				id = fmt.Sprint(m["_id"])
			}
			if _, exists := c.Profiles[id]; exists {
				return nil, fmt.Errorf("duplicate environment name %q", id)
			}
			vars := map[string]string{}
			for k, v := range d {
				s, e := templateString(convert(v))
				if e != nil {
					return nil, e
				}
				vars[k] = s
			}
			c.Profiles[id] = vars
		case "request":
			for _, k := range []string{"preRequestScript", "afterResponseScript"} {
				if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
					return nil, fmt.Errorf("Insomnia scripts are not executed; convert %s explicitly", k)
				}
			}
			endpoint, ok := m["url"].(string)
			if !ok {
				return nil, fmt.Errorf("Insomnia request URL required")
			}
			r := config.Request{ID: fmt.Sprint(m["_id"]), Name: fmt.Sprint(m["name"]), Protocol: "http", Action: strings.ToUpper(fmt.Sprint(m["method"])), Endpoint: convert(endpoint).(string), Params: map[string]any{}}
			headers := map[string]any{}
			if list, ok := m["headers"].([]any); ok {
				for _, v := range list {
					h, ok := v.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("invalid Insomnia header")
					}
					if h["disabled"] == true {
						continue
					}
					headers[fmt.Sprint(h["name"])] = convert(h["value"])
				}
			}
			r.Params["headers"] = headers
			query := map[string]any{}
			if list, ok := m["parameters"].([]any); ok {
				for _, v := range list {
					p, ok := v.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("invalid Insomnia parameter")
					}
					if p["disabled"] == true {
						continue
					}
					k := fmt.Sprint(p["name"])
					val := convert(p["value"])
					if existing, ok := query[k]; ok {
						if a, ok := existing.([]any); ok {
							query[k] = append(a, val)
						} else {
							query[k] = []any{existing, val}
						}
					} else {
						query[k] = val
					}
				}
			}
			if len(query) > 0 {
				r.Params["query"] = query
			}
			if a, ok := m["authentication"].(map[string]any); ok && a["disabled"] != true {
				switch a["type"] {
				case nil, "":
				case "basic":
					r.Params["username"] = convert(a["username"])
					r.Params["password"] = convert(a["password"])
				case "bearer":
					r.Params["bearer"] = convert(a["token"])
				default:
					return nil, fmt.Errorf("unsupported Insomnia auth %v", a["type"])
				}
			}
			if b, ok := m["body"].(map[string]any); ok {
				mime, _ := b["mimeType"].(string)
				if mime != "" {
					headers["Content-Type"] = mime
				}
				if p, ok := b["params"].([]any); ok {
					form := map[string]any{}
					for _, v := range p {
						entry, ok := v.(map[string]any)
						if !ok {
							return nil, fmt.Errorf("invalid form parameter")
						}
						if entry["disabled"] == true {
							continue
						}
						if entry["type"] == "file" {
							return nil, fmt.Errorf("Insomnia file uploads require explicit file() template")
						}
						form[fmt.Sprint(entry["name"])] = convert(entry["value"])
					}
					if strings.HasPrefix(mime, "multipart/") {
						delete(headers, "Content-Type")
						r.Params["form_multipart"] = form
					} else {
						r.Params["form_urlencoded"] = form
					}
				} else if text, ok := b["text"]; ok {
					r.Params["body"] = convert(text)
				}
			}
			c.Requests = append(c.Requests, r)
		case "workspace", "request_group", "cookie_jar":
		default:
			return nil, fmt.Errorf("unsupported Insomnia resource type %v", m["_type"])
		}
	}
	if len(c.Requests) == 0 {
		return nil, fmt.Errorf("no Insomnia requests found")
	}
	return nativeCollection(c)
}

func importOpenAPI(data []byte) (*config.Collection, error) {
	root, e := decodeCollectionMap(data)
	if e != nil {
		return nil, e
	}
	version, _ := root["openapi"].(string)
	if !strings.HasPrefix(version, "3.") {
		return nil, fmt.Errorf("OpenAPI 3.x required")
	}
	c := &config.Collection{Version: 1, DefaultProfile: "imported", Profiles: map[string]map[string]string{"imported": {}}}
	vars := c.Profiles["imported"]
	server := "http://127.0.0.1:8080"
	if servers, ok := root["servers"].([]any); ok && len(servers) > 0 {
		if m, ok := servers[0].(map[string]any); ok {
			if s, ok := m["url"].(string); ok {
				server = s
			}
			if parameters, ok := m["variables"].(map[string]any); ok {
				for key, value := range parameters {
					p, ok := value.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("invalid server variable")
					}
					server = strings.ReplaceAll(server, "{"+key+"}", fmt.Sprint(p["default"]))
				}
			}
		}
	}
	vars["host"] = strings.TrimRight(server, "/")
	paths, e := mapValue(root["paths"], "OpenAPI paths")
	if e != nil {
		return nil, e
	}
	ids := map[string]bool{}
	for _, path := range sortedKeys(paths) {
		resolved, e := resolveCollectionRefs(paths[path], root, map[string]bool{}, 0)
		if e != nil {
			return nil, e
		}
		p, e := mapValue(resolved, "path")
		if e != nil {
			return nil, e
		}
		for _, method := range []string{"get", "head", "post", "put", "patch", "delete", "options", "trace"} {
			raw, ok := p[method]
			if !ok {
				continue
			}
			op, e := mapValue(raw, "operation")
			if e != nil {
				return nil, e
			}
			id, _ := op["operationId"].(string)
			if id == "" {
				id = safeImportID(method + "_" + path)
			}
			base := id
			for n := 2; ids[id]; n++ {
				id = fmt.Sprintf("%s_%d", base, n)
			}
			ids[id] = true
			name, _ := op["summary"].(string)
			r := config.Request{ID: id, Name: name, Protocol: "http", Action: strings.ToUpper(method), Endpoint: "{{ host }}" + path, Params: map[string]any{}}
			headers := map[string]any{}
			query := map[string]any{}
			params := []any{}
			if list, ok := p["parameters"].([]any); ok {
				params = append(params, list...)
			}
			if list, ok := op["parameters"].([]any); ok {
				params = append(params, list...)
			}
			for _, raw := range params {
				param, e := mapValue(raw, "parameter")
				if e != nil {
					return nil, e
				}
				key, _ := param["name"].(string)
				v, ok := param["example"]
				if !ok {
					if schema, schemaOK := param["schema"].(map[string]any); schemaOK {
						v, ok = schema["default"]
					}
				}
				if !ok {
					v = "CHANGE_ME"
				}
				value, e := templateString(v)
				if e != nil {
					return nil, e
				}
				switch param["in"] {
				case "path":
					vars[key] = value
					r.Endpoint = strings.ReplaceAll(r.Endpoint, "{"+key+"}", "{{ "+key+" }}")
				case "query":
					query[key] = value
				case "header":
					headers[key] = value
				case "cookie":
					return nil, fmt.Errorf("OpenAPI cookie parameters require explicit Cookie header")
				}
			}
			if b, ok := op["requestBody"].(map[string]any); ok {
				content, ok := b["content"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid OpenAPI request body")
				}
				keys := sortedKeys(content)
				if _, ok := content["application/json"]; ok {
					keys = []string{"application/json"}
				}
				if len(keys) > 0 {
					mime := keys[0]
					desc, ok := content[mime].(map[string]any)
					if !ok {
						return nil, fmt.Errorf("invalid media type")
					}
					example, ok := desc["example"]
					if !ok {
						if schema, ok := desc["schema"].(map[string]any); ok {
							example = schema["example"]
						}
					}
					if example == nil {
						example = map[string]any{}
					}
					headers["Content-Type"] = mime
					if mime == "application/json" {
						r.Params["json"] = example
					} else if mime == "application/x-www-form-urlencoded" {
						r.Params["form_urlencoded"] = example
					} else {
						s, e := templateString(example)
						if e != nil {
							return nil, e
						}
						r.Params["body"] = s
					}
				}
			}
			security := root["security"]
			if own, ok := op["security"]; ok {
				security = own
			}
			if list, ok := security.([]any); ok && len(list) > 0 {
				first, ok := list[0].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid security requirement")
				}
				components, _ := root["components"].(map[string]any)
				schemes, _ := components["securitySchemes"].(map[string]any)
				for key := range first {
					scheme, ok := schemes[key].(map[string]any)
					if !ok {
						return nil, fmt.Errorf("missing security scheme %s", key)
					}
					switch scheme["type"] {
					case "http":
						switch scheme["scheme"] {
						case "bearer":
							r.Params["bearer"] = "{{ env('IOTOOLS_TOKEN') }}"
						case "basic":
							r.Params["username"] = "{{ env('IOTOOLS_USERNAME') }}"
							r.Params["password"] = "{{ env('IOTOOLS_PASSWORD') }}"
						default:
							return nil, fmt.Errorf("unsupported HTTP security scheme")
						}
					case "apiKey":
						name := fmt.Sprint(scheme["name"])
						switch scheme["in"] {
						case "header":
							headers[name] = "{{ env('IOTOOLS_API_KEY') }}"
						case "query":
							query[name] = "{{ env('IOTOOLS_API_KEY') }}"
						default:
							return nil, fmt.Errorf("unsupported API key destination")
						}
					default:
						return nil, fmt.Errorf("OAuth/OpenID must be configured as an explicit login chain")
					}
				}
			}
			if len(headers) > 0 {
				r.Params["headers"] = headers
			}
			if len(query) > 0 {
				r.Params["query"] = query
			}
			c.Requests = append(c.Requests, r)
		}
	}
	if len(c.Requests) == 0 {
		return nil, fmt.Errorf("no OpenAPI operations found")
	}
	return nativeCollection(c)
}

// ExportCurl emits a POSIX-shell command without executing it. It is explicitly
// sensitive output: callers should only expose it on user request.
func ExportCurl(r config.Request) (string, error) {
	if r.Protocol != "http" {
		return "", fmt.Errorf("curl export requires HTTP")
	}
	if _, e := url.ParseRequestURI(r.Endpoint); e != nil {
		return "", e
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	var b bytes.Buffer
	fmt.Fprintf(&b, "curl --request %s --url %s", quote(r.Action), quote(r.Endpoint))
	if headers, ok := r.Params["headers"].(map[string]any); ok {
		for _, k := range sortedKeys(headers) {
			fmt.Fprintf(&b, " --header %s", quote(k+": "+fmt.Sprint(headers[k])))
		}
	}
	if body := r.String("body", ""); body != "" {
		fmt.Fprintf(&b, " --data-binary %s", quote(body))
	}
	return b.String(), nil
}
