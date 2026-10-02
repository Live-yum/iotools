package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	codec "github.com/Live-yum/iotools/internal/crypto"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

const maxBody = 4 << 20

func runHTTP(ctx context.Context, r config.Request, emit Emit) error {
	if value, exists := r.Params["persist"]; exists {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("persist must be a YAML boolean")
		}
	}
	if _, unresolved := r.Params["body_stream"]; unresolved {
		return fmt.Errorf("body_stream必须通过集合模板引擎解析")
	}
	if _, limited := r.Params["max_upload_bytes"]; limited {
		if _, file := r.Params["body_file"]; !file {
			return fmt.Errorf("max_upload_bytes仅用于body_file")
		}
	}
	if e := validateHTTPDownload(r); e != nil {
		return e
	}
	redirectPolicy, e := httpRedirectPolicy(ctx, r)
	if e != nil {
		return e
	}
	u, e := url.Parse(r.Endpoint)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("HTTP endpoint must be an absolute http(s) URL")
	}
	method := strings.ToUpper(r.Action)
	if !strings.Contains(" GET HEAD OPTIONS POST PUT PATCH DELETE TRACE CONNECT ", " "+method+" ") {
		return unsupported(r, "GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE", "TRACE", "CONNECT")
	}
	var streamReader io.Reader
	var streamLength int64
	var streamIdentity os.FileInfo
	if _, ok := r.Params["body_file"]; ok {
		file, reader, length, err := openHTTPBodyFile(ctx, r)
		if err != nil {
			return err
		}
		defer file.Close()
		streamIdentity, err = file.Stat()
		if err != nil {
			return err
		}
		streamReader = reader
		streamLength = length
	}
	body, contentType, e := httpRequestBody(r)
	if e != nil {
		return e
	}
	r, e = httpQueryParameters(r)
	if e != nil {
		return e
	}
	codecs, e := httpCodecs(r)
	if e != nil {
		return e
	}
	endpoint, body, headers, e := transformHTTPRequest(r, body, codecs)
	if e != nil {
		return e
	}
	var transforms []codec.Rule
	if v, ok := r.Params["response_transform"]; ok {
		if e = codec.Parse(v, &transforms); e != nil {
			return e
		}
	}
	if e = codec.ValidateTransformRules(codecs, transforms); e != nil {
		return e
	}
	var reader io.Reader = strings.NewReader(body)
	if streamReader != nil {
		reader = streamReader
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if e != nil {
		return e
	}
	if streamReader != nil {
		req.ContentLength = streamLength
		req.GetBody = func() (io.ReadCloser, error) {
			file, reader, length, err := openHTTPBodyFile(ctx, r)
			if err != nil {
				return nil, err
			}
			identity, err := file.Stat()
			if err != nil || !os.SameFile(streamIdentity, identity) || length != streamLength || !identity.ModTime().Equal(streamIdentity.ModTime()) {
				file.Close()
				return nil, fmt.Errorf("重定向重发前文件身份/大小/修改时间改变，拒绝重发")
			}
			return &httpStreamBody{Reader: reader, Closer: file}, nil
		}
	}
	req.Header = headers
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token := r.String("bearer", ""); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if user := r.String("username", ""); user != "" {
		req.SetBasicAuth(user, r.String("password", ""))
	}
	transport, e := newScopedHTTPTransport(r, emit)
	if e != nil {
		return e
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: redirectPolicy}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if _, ok := r.Params["response_file"]; ok {
		return streamHTTPResponse(ctx, r, resp, emit)
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if e != nil {
		return e
	}
	if len(data) > maxBody {
		return fmt.Errorf("response exceeds 4 MiB limit")
	}
	parsed, parseErr := decodeJSON(data)
	if parseErr != nil {
		parsed = string(data)
	}
	queryBody := data
	send(emit, "response", map[string]any{"status": resp.StatusCode, "headers": resp.Header, "body": parsed, "raw_body_base64": base64.StdEncoding.EncodeToString(data)})
	if len(transforms) > 0 {
		transformed, err := codec.Transform(data, codecs, transforms)
		if err != nil {
			return &HTTPResponseTransformError{Err: fmt.Errorf("响应转换失败: %w", err)}
		}
		var view any
		d := json.NewDecoder(bytes.NewReader(transformed))
		d.UseNumber()
		if err = d.Decode(&view); err != nil {
			return &HTTPResponseTransformError{Err: fmt.Errorf("响应转换 JSON 无效")}
		}
		send(emit, "transformed", map[string]any{"status": resp.StatusCode, "body": view})
		queryBody = transformed
	}
	if filter := r.String("query_filter", ""); filter != "" {
		values, err := FilterJSON(ctx, filter, queryBody)
		if err != nil {
			return fmt.Errorf("response query: %w", err)
		}
		send(emit, "query", values)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}

func httpQueryParameters(r config.Request) (config.Request, error) {
	raw, exists := r.Params["query"]
	if !exists {
		return r, nil
	}
	params, ok := raw.(map[string]any)
	if !ok {
		return r, fmt.Errorf("query must be a mapping")
	}
	u, e := url.Parse(r.Endpoint)
	if e != nil {
		return r, e
	}
	q := u.Query()
	for _, k := range sortedKeys(params) {
		value := params[k]
		values, ok := value.([]any)
		if !ok {
			values = []any{value}
		}
		for _, v := range values {
			s, e := templateString(v)
			if e != nil {
				return r, e
			}
			q.Add(k, s)
		}
	}
	u.RawQuery = q.Encode()
	r.Endpoint = u.String()
	return r, nil
}
func httpRequestBody(r config.Request) (string, string, error) {
	count := 0
	for _, key := range []string{"body", "json", "form_urlencoded", "form_multipart"} {
		if _, ok := r.Params[key]; ok {
			count++
		}
	}
	if count > 1 {
		return "", "", fmt.Errorf("body, json and forms are mutually exclusive")
	}
	if value, ok := r.Params["json"]; ok {
		b, e := json.Marshal(value)
		if len(b) > maxBody {
			return "", "", fmt.Errorf("request body exceeds 4 MiB")
		}
		return string(b), "application/json", e
	}
	if value, ok := r.Params["form_urlencoded"]; ok {
		form, ok := value.(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("URL form must be a mapping")
		}
		q := url.Values{}
		for _, k := range sortedKeys(form) {
			v, e := templateString(form[k])
			if e != nil {
				return "", "", e
			}
			q.Add(k, v)
		}
		body := q.Encode()
		if len(body) > maxBody {
			return "", "", fmt.Errorf("request body exceeds 4 MiB")
		}
		return body, "application/x-www-form-urlencoded", nil
	}
	if value, ok := r.Params["form_multipart"]; ok {
		form, ok := value.(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("multipart form must be a mapping")
		}
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		keys := make([]string, 0, len(form))
		for k := range form {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			part, e := writer.CreateFormField(k)
			if e != nil {
				return "", "", e
			}
			data, e := templateBytes(form[k])
			if e != nil {
				return "", "", e
			}
			if _, e = part.Write(data); e != nil {
				return "", "", e
			}
			if b.Len() > maxBody {
				return "", "", fmt.Errorf("multipart body exceeds 4 MiB")
			}
		}
		if e := writer.Close(); e != nil {
			return "", "", e
		}
		if b.Len() > maxBody {
			return "", "", fmt.Errorf("multipart body exceeds 4 MiB")
		}
		return b.String(), writer.FormDataContentType(), nil
	}
	body := r.String("body", "")
	if len(body) > maxBody {
		return "", "", fmt.Errorf("request body exceeds 4 MiB")
	}
	return body, "", nil
}

type httpStreamBody struct {
	io.Reader
	io.Closer
}
