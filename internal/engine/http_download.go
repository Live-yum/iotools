package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"io"
	"net/http"
	"os"
)

func validateHTTPDownload(r config.Request) error {
	if raw, exists := r.Params["max_response_bytes"]; exists {
		if _, file := r.Params["response_file"]; !file {
			return fmt.Errorf("max_response_bytes仅用于response_file")
		}
		n, e := exactInt(raw)
		if e != nil || n < 1 || n > 8<<30 {
			return fmt.Errorf("max_response_bytes必须为1..8GiB")
		}
	}
	value, ok := r.Params["response_file"]
	if !ok {
		return nil
	}
	name, ok := value.(string)
	if !ok || name == "" {
		return fmt.Errorf("response_file必须是明确的新文件路径")
	}
	for _, key := range []string{"response_transform", "query_filter"} {
		if _, ok := r.Params[key]; ok {
			return fmt.Errorf("流式response_file不能与%s同用；派生/查询视图需要有界内存响应", key)
		}
	}
	if _, e := os.Lstat(name); e == nil {
		return fmt.Errorf("输出文件已存在，拒绝覆盖：%s", name)
	} else if !os.IsNotExist(e) {
		return e
	}
	return nil
}
func streamHTTPResponse(ctx context.Context, r config.Request, resp *http.Response, emit Emit) error {
	limit := int64(1 << 30)
	if raw, ok := r.Params["max_response_bytes"]; ok {
		n, e := exactInt(raw)
		if e != nil || n < 1 || n > 8<<30 {
			return fmt.Errorf("max_response_bytes必须为1..8GiB")
		}
		limit = n
	}
	if resp.ContentLength > limit {
		return fmt.Errorf("响应超过max_response_bytes")
	}
	name := r.String("response_file", "")
	f, e := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			os.Remove(name)
		}
	}()
	digest := sha256.New()
	count, e := io.CopyBuffer(io.MultiWriter(f, digest), contextFileReader{ctx, io.LimitReader(resp.Body, limit+1)}, make([]byte, 32<<10))
	if e != nil {
		return e
	}
	if count > limit {
		return fmt.Errorf("响应超过max_response_bytes")
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	complete = true
	send(emit, "response-file", map[string]any{"status": resp.StatusCode, "headers": resp.Header, "path": name, "bytes": count, "sha256": hex.EncodeToString(digest.Sum(nil))})
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d（错误响应已保存到指定新文件）", resp.StatusCode)
	}
	return nil
}
