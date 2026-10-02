package engine

import (
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"net"
	"net/http"
	"strings"
	"unicode"
)

type scopedHTTPTransport struct {
	strict, insecure *http.Transport
	request          config.Request
	hosts            map[string]bool
	emit             Emit
}

func httpInsecureHosts(r config.Request) (map[string]bool, error) {
	hosts := map[string]bool{}
	raw, exists := r.Params["ignore_certificate_hosts"]
	if !exists {
		return hosts, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) > 50 {
		return nil, fmt.Errorf("ignore_certificate_hosts必须为最多50个精确主机名/IP的数组")
	}
	for _, item := range list {
		host, ok := item.(string)
		if !ok || host == "" || len(host) > 253 || strings.IndexFunc(host, unicode.IsControl) >= 0 || strings.ContainsAny(host, "/*?@\\\r\n\t ") || strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return nil, fmt.Errorf("证书例外必须是精确主机名/IP，不含端口、通配符或URL")
		}
		hosts[strings.ToLower(host)] = true
	}
	return hosts, nil
}
func newScopedHTTPTransport(r config.Request, emit Emit) (*scopedHTTPTransport, error) {
	hosts, err := httpInsecureHosts(r)
	if err != nil {
		return nil, err
	}
	tls, err := tlsConfig(r)
	if err != nil {
		return nil, err
	}
	strict := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tls}
	insecureTLS := tls.Clone()
	// Only selected by RoundTrip after exact-host matching and explicit runtime approval.
	insecureTLS.InsecureSkipVerify = true
	insecure := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: insecureTLS}
	return &scopedHTTPTransport{strict: strict, insecure: insecure, request: r, hosts: hosts, emit: emit}, nil
}
func (t *scopedHTTPTransport) CloseIdleConnections() {
	t.strict.CloseIdleConnections()
	t.insecure.CloseIdleConnections()
}
func (t *scopedHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || !t.hosts[strings.ToLower(req.URL.Hostname())] {
		return t.strict.RoundTrip(req)
	}
	options, _ := req.Context().Value(httpWorkflowKey{}).(HTTPWorkflowOptions)
	allowed := options.AllowInsecureTLS
	selected := t.request
	selected.Endpoint = req.URL.String()
	selected.Action = req.Method
	if options.AuthorizeInsecureTLS != nil {
		var err error
		allowed, err = options.AuthorizeInsecureTLS(req.Context(), selected)
		if err != nil {
			return nil, err
		}
	}
	if !allowed {
		return nil, fmt.Errorf("主机TLS证书例外需要本次明确确认或--allow-insecure-tls；没有发送请求")
	}
	send(t.emit, "security-warning", map[string]any{"host": req.URL.Hostname(), "message": "此请求已明确关闭该主机的TLS证书校验，存在中间人风险；其他主机仍强校验"})
	return t.insecure.RoundTrip(req)
}
