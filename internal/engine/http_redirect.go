package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
)

func httpOrigin(u *url.URL) string { return strings.ToLower(u.Scheme + "://" + u.Host) }
func httpRedirectPolicy(ctx context.Context, r config.Request) (func(*http.Request, []*http.Request) error, error) {
	follow := false
	if raw, ok := r.Params["follow_redirects"]; ok {
		var valid bool
		follow, valid = raw.(bool)
		if !valid {
			return nil, fmt.Errorf("follow_redirects必须为布尔值")
		}
	}
	limit := 10
	if raw, ok := r.Params["max_redirects"]; ok {
		n, err := exactInt(raw)
		if err != nil || n < 1 || n > 50 {
			return nil, fmt.Errorf("max_redirects必须为1..50")
		}
		limit = int(n)
	}
	origins := map[string]bool{}
	if raw, ok := r.Params["redirect_origins"]; ok {
		list, ok := raw.([]any)
		if !ok || len(list) > 50 {
			return nil, fmt.Errorf("redirect_origins必须为最多50个明确origin的数组")
		}
		for _, value := range list {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("redirect_origins元素必须为字符串")
			}
			u, err := url.Parse(text)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
				return nil, fmt.Errorf("redirect_origins只接受scheme://host[:port]，不含凭据/路径/查询")
			}
			origins[httpOrigin(u)] = true
		}
	}
	return func(next *http.Request, via []*http.Request) error {
		if !follow {
			return http.ErrUseLastResponse
		}
		if len(via) > limit {
			return fmt.Errorf("重定向超过max_redirects=%d", limit)
		}
		if next.URL.User != nil || next.URL.Host == "" || (next.URL.Scheme != "http" && next.URL.Scheme != "https") {
			return fmt.Errorf("重定向目标必须为无凭据的HTTP(S)URL")
		}
		previous := via[len(via)-1].URL
		if previous.Scheme == "https" && next.URL.Scheme == "http" {
			return fmt.Errorf("拒绝HTTPS降级到HTTP的重定向")
		}
		if httpOrigin(next.URL) != httpOrigin(via[0].URL) {
			if !origins[httpOrigin(next.URL)] {
				return fmt.Errorf("跨origin重定向未在redirect_origins中明确允许")
			}
			// Custom headers can carry credentials under arbitrary names. Only forward
			// representation negotiation fields to an explicitly allowed new origin.
			headers := http.Header{}
			for _, key := range []string{"Accept", "Accept-Encoding", "Content-Type", "User-Agent"} {
				if value := next.Header.Values(key); len(value) > 0 {
					headers[key] = append([]string(nil), value...)
				}
			}
			next.Header = headers
			next.Host = ""
		}
		redirected := r
		redirected.Endpoint = next.URL.String()
		redirected.Action = next.Method
		options, _ := ctx.Value(httpWorkflowKey{}).(HTTPWorkflowOptions)
		if redirected.Mutates() && options.AuthorizeRequestWrite != nil {
			allowed, err := options.AuthorizeRequestWrite(ctx, redirected)
			if err != nil {
				return err
			}
			if !allowed {
				return fmt.Errorf("已取消重定向修改步骤")
			}
		}
		return nil
	}, nil
}
