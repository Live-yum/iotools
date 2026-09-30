package engine

import (
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"strings"
)

type HTTPOverrides struct {
	Fields, Headers, Query, Form []string
	Body, Bearer, Basic, URL     *string
}

func cloneHTTPValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			out[k] = cloneHTTPValue(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = cloneHTTPValue(v)
		}
		return out
	}
	return v
}
func ApplyHTTPOverrides(c *config.Collection, r config.Request, profile string, o HTTPOverrides) (*config.Collection, config.Request, string, error) {
	if r.Protocol != "http" {
		return nil, r, profile, fmt.Errorf("临时HTTP覆盖仅适用于HTTP请求")
	}
	if o.Basic != nil && o.Bearer != nil {
		return nil, r, profile, fmt.Errorf("basic与bearer不能同时覆盖")
	}
	if o.URL != nil {
		r.Endpoint = *o.URL
	}
	copyCollection := *c
	copyCollection.Profiles = map[string]map[string]string{}
	for name, fields := range c.Profiles {
		m := map[string]string{}
		for k, v := range fields {
			m[k] = v
		}
		copyCollection.Profiles[name] = m
	}
	if profile == "" {
		profile = c.DefaultProfile
	}
	if len(o.Fields) > 0 {
		if profile == "" {
			profile = "@temporary"
			copyCollection.Profiles[profile] = map[string]string{}
		}
		fields, ok := copyCollection.Profiles[profile]
		if !ok {
			return nil, r, profile, fmt.Errorf("unknown profile %q", profile)
		}
		for _, entry := range o.Fields {
			name, value, ok := strings.Cut(entry, "=")
			if !ok || name == "" {
				return nil, r, profile, fmt.Errorf("--set需要 name=value")
			}
			fields[name] = value
		}
	}
	r.Params, _ = cloneHTTPValue(r.Params).(map[string]any)
	if r.Params == nil {
		r.Params = map[string]any{}
	}
	apply := func(param string, entries []string, repeat, fold bool) error {
		if len(entries) == 0 {
			return nil
		}
		values, ok := r.Params[param].(map[string]any)
		if !ok && r.Params[param] != nil {
			return fmt.Errorf("%s必须为映射", param)
		}
		if values == nil {
			values = map[string]any{}
		}
		replaced := map[string]bool{}
		for _, entry := range entries {
			name, value, has := strings.Cut(entry, "=")
			if name == "" {
				return fmt.Errorf("临时覆盖名称不能为空")
			}
			identity := name
			if fold {
				identity = strings.ToLower(name)
			}
			if !replaced[identity] || !repeat || !has {
				for k := range values {
					if k == name || fold && strings.EqualFold(k, name) {
						delete(values, k)
					}
				}
			}
			if has {
				if repeat && replaced[identity] {
					list, _ := values[name].([]any)
					values[name] = append(list, value)
				} else if repeat {
					values[name] = []any{value}
				} else {
					values[name] = value
				}
			}
			replaced[identity] = true
		}
		r.Params[param] = values
		return nil
	}
	if e := apply("headers", o.Headers, false, true); e != nil {
		return nil, r, profile, e
	}
	if e := apply("query", o.Query, true, false); e != nil {
		return nil, r, profile, e
	}
	if len(o.Form) > 0 {
		param := ""
		for _, key := range []string{"form_urlencoded", "form_multipart"} {
			if _, ok := r.Params[key]; ok {
				param = key
			}
		}
		if param == "" {
			return nil, r, profile, fmt.Errorf("--form需要现有表单请求")
		}
		if e := apply(param, o.Form, false, false); e != nil {
			return nil, r, profile, e
		}
	}
	if o.Body != nil {
		delete(r.Params, "body_file")
		delete(r.Params, "body_stream")
		delete(r.Params, "max_upload_bytes")
		if r.Params["form_urlencoded"] != nil || r.Params["form_multipart"] != nil {
			return nil, r, profile, fmt.Errorf("表单正文请用--form逐字段覆盖")
		}
		if _, ok := r.Params["json"]; ok {
			value, e := decodeJSON([]byte(*o.Body))
			if e != nil {
				return nil, r, profile, e
			}
			r.Params["json"] = value
		} else {
			r.Params["body"] = *o.Body
		}
	}
	if o.Bearer != nil {
		r.Params["bearer"] = *o.Bearer
		delete(r.Params, "username")
		delete(r.Params, "password")
	}
	if o.Basic != nil {
		user, password, ok := strings.Cut(*o.Basic, ":")
		if !ok {
			return nil, r, profile, fmt.Errorf("--basic需要username:password；建议使用环境变量模板而非明文命令历史")
		}
		r.Params["username"] = user
		r.Params["password"] = password
		delete(r.Params, "bearer")
	}
	return &copyCollection, r, profile, nil
}
