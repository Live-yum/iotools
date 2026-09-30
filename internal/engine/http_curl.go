package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// GenerateCurl never executes the generated command. Triggered dependencies are
// disabled unless explicitly selected, and retain their separate write gates.
func GenerateCurl(ctx context.Context, c *config.Collection, r config.Request, profile string, allowWrites, executeTriggers bool) (string, error) {
	duration, err := r.Duration()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if c == nil || r.Protocol != "http" {
		return "", fmt.Errorf("curl生成仅支持HTTP请求")
	}
	if profile == "" {
		profile = c.DefaultProfile
	}
	vars := map[string]string{}
	if profile != "" {
		var ok bool
		vars, ok = c.Profiles[profile]
		if !ok {
			return "", fmt.Errorf("unknown profile %q", profile)
		}
	}
	options, _ := ctx.Value(httpWorkflowKey{}).(HTTPWorkflowOptions)
	options.NoNetwork = !executeTriggers
	root, key := ".", c.SourcePath
	if key != "" {
		root = filepath.Dir(key)
	} else {
		b, _ := json.Marshal(c)
		key = fmt.Sprintf("memory:%x", sha256.Sum256(b))
	}
	w := &httpWorkflow{ctx: ctx, collection: c, profile: profile, rootDir: root, collectionKey: key, vars: vars, varsCache: map[string]any{}, varsActive: map[string]bool{}, active: map[string]bool{}, executed: map[string]bool{}, responses: map[string]*HTTPHistoryEntry{}, allowWrites: allowWrites, options: options, current: r}
	if options.HistoryPath != "" {
		h, e := OpenHTTPHistory(options.HistoryPath)
		if e != nil {
			return "", e
		}
		w.history = h
		defer h.Close()
	}
	resolved, e := w.renderRequest(r)
	if e != nil {
		return "", e
	}
	return curlForRequest(resolved)
}
func shellQuote(value string) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("curl命令参数必须是无NUL的UTF-8文本，请导出二进制正文到文件")
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'", nil
}
func curlForRequest(r config.Request) (string, error) {
	if e := validateParams(r); e != nil {
		return "", e
	}
	endpoint, e := url.Parse(r.Endpoint)
	if e != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return "", fmt.Errorf("无效HTTP端点")
	}
	body, contentType, e := httpRequestBody(r)
	if e != nil {
		return "", e
	}
	r, e = httpQueryParameters(r)
	if e != nil {
		return "", e
	}
	codecs, e := httpCodecs(r)
	if e != nil {
		return "", e
	}
	address, body, headers, e := transformHTTPRequest(r, body, codecs)
	if e != nil {
		return "", e
	}
	req, e := http.NewRequest(strings.ToUpper(r.Action), address, nil)
	if e != nil {
		return "", e
	}
	req.Header = headers
	if contentType != "" && headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", contentType)
	}
	if bearer := r.String("bearer", ""); bearer != "" {
		headers.Set("Authorization", "Bearer "+bearer)
	}
	if user := r.String("username", ""); user != "" {
		req.SetBasicAuth(user, r.String("password", ""))
	}
	args := []string{"curl", "--request"}
	add := func(value string) error {
		quoted, e := shellQuote(value)
		if e != nil {
			return e
		}
		args = append(args, quoted)
		return nil
	}
	if e = add(strings.ToUpper(r.Action)); e != nil {
		return "", e
	}
	args = append(args, "--url")
	if e = add(address); e != nil {
		return "", e
	}
	keys := []string{}
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers[key] {
			args = append(args, "--header")
			if e = add(key + ": " + value); e != nil {
				return "", e
			}
		}
	}
	for _, pair := range [][2]string{{"ca_file", "--cacert"}, {"cert_file", "--cert"}, {"key_file", "--key"}} {
		if value := r.String(pair[0], ""); value != "" {
			args = append(args, pair[1])
			if e = add(value); e != nil {
				return "", e
			}
		}
	}
	if file := r.String("body_file", ""); file != "" {
		args = append(args, "--data-binary")
		if e = add("@" + file); e != nil {
			return "", e
		}
	} else if body != "" {
		args = append(args, "--data-binary")
		if e = add(body); e != nil {
			return "", e
		}
	}
	if output := r.String("response_file", ""); output != "" {
		args = append(args, "--output")
		if e = add(output); e != nil {
			return "", e
		}
	}
	return strings.Join(args, " "), nil
}
