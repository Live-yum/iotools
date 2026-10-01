package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (w *httpWorkflow) streamFileTemplate(value any) (string, bool, error) {
	text, ok := value.(string)
	if !ok {
		return "", false, nil
	}
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{{") || !strings.HasSuffix(text, "}}") {
		return "", false, nil
	}
	expression, e := parseExpression(strings.TrimSpace(text[2 : len(text)-2]))
	if e != nil {
		return "", false, e
	}
	if expression.kind != 'c' || expression.name != "file" || len(expression.args) != 1 || len(expression.kwargs) != 0 {
		return "", false, nil
	}
	v, e := w.eval(expression.args[0])
	if e != nil {
		return "", false, e
	}
	name, e := templateString(v)
	if e != nil {
		return "", false, e
	}
	path, e := w.absoluteBodyFile(name)
	return path, true, e
}
func (w *httpWorkflow) absoluteBodyFile(name string) (string, error) {
	if strings.HasPrefix(name, "~/") {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		name = filepath.Join(home, name[2:])
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(w.rootDir, name)
	}
	if w.options.ValidateFilePath != nil {
		if e := w.options.ValidateFilePath(name); e != nil {
			return "", e
		}
	}
	return name, nil
}

type contextFileReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextFileReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(b)
}
func openHTTPBodyFile(ctx context.Context, r config.Request) (*os.File, io.Reader, int64, error) {
	for _, key := range []string{"body", "json", "form_urlencoded", "form_multipart", "request_crypto", "request_transforms"} {
		if _, ok := r.Params[key]; ok {
			return nil, nil, 0, fmt.Errorf("body_file不能同时使用%s；流式正文不隐式缓冲或跳过加密", key)
		}
	}
	path, ok := r.Params["body_file"].(string)
	if !ok || path == "" {
		return nil, nil, 0, fmt.Errorf("body_file必须是文件路径")
	}
	limit := int64(1 << 30)
	if raw, ok := r.Params["max_upload_bytes"]; ok {
		n, e := exactInt(raw)
		if e != nil || n < 1 || n > 8<<30 {
			return nil, nil, 0, fmt.Errorf("max_upload_bytes必须为1..8GiB")
		}
		limit = n
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, 0, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, 0, fmt.Errorf("流式正文必须是普通文件")
	}
	if info.Size() > limit {
		f.Close()
		return nil, nil, 0, fmt.Errorf("文件超过max_upload_bytes限制")
	}
	return f, contextFileReader{ctx, io.LimitReader(f, info.Size())}, info.Size(), nil
}
