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
	"net/http"
	"net/url"
	"strings"
)

const maxBody = 4 << 20

func runHTTP(ctx context.Context, r config.Request, emit Emit) error {
	u, e := url.Parse(r.Endpoint)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("HTTP endpoint must be an absolute http(s) URL")
	}
	method := strings.ToUpper(r.Action)
	if !strings.Contains(" GET HEAD OPTIONS POST PUT PATCH DELETE ", " "+method+" ") {
		return unsupported(r, "GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE")
	}
	body := r.String("body", "")
	if v, ok := r.Params["json"]; ok {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		body = string(b)
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
	req, e := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if e != nil {
		return e
	}
	req.Header = headers
	if _, ok := r.Params["json"]; ok && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := r.String("bearer", ""); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if user := r.String("username", ""); user != "" {
		req.SetBasicAuth(user, r.String("password", ""))
	}
	tls, e := tlsConfig(r)
	if e != nil {
		return e
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tls}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if e != nil {
		return e
	}
	if len(data) > maxBody {
		return fmt.Errorf("response exceeds 4 MiB limit")
	}
	var parsed any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&parsed) != nil {
		parsed = string(data)
	}
	send(emit, "response", map[string]any{"status": resp.StatusCode, "headers": resp.Header, "body": parsed, "raw_body_base64": base64.StdEncoding.EncodeToString(data)})
	if len(transforms) > 0 {
		transformed, err := codec.Transform(data, codecs, transforms)
		if err != nil {
			return fmt.Errorf("响应转换失败: %w", err)
		}
		var view any
		d := json.NewDecoder(bytes.NewReader(transformed))
		d.UseNumber()
		if err = d.Decode(&view); err != nil {
			return fmt.Errorf("响应转换 JSON 无效")
		}
		send(emit, "transformed", map[string]any{"status": resp.StatusCode, "body": view})
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}
