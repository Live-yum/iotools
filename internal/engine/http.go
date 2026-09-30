package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
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
	req, e := http.NewRequestWithContext(ctx, method, r.Endpoint, strings.NewReader(body))
	if e != nil {
		return e
	}
	if h, ok := r.Params["headers"].(map[string]any); ok {
		for k, v := range h {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}
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
	if json.Unmarshal(data, &parsed) != nil {
		parsed = string(data)
	}
	send(emit, "response", map[string]any{"status": resp.StatusCode, "headers": resp.Header, "body": parsed})
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}
