package webhost

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func limit(value any, maximum int64) (int64, error) {
	if value == nil {
		return min(int64(1)<<30, maximum), nil
	}
	n, e := strconv.ParseInt(fmt.Sprint(value), 10, 64)
	if e != nil || n < 1 || n > maximum {
		return 0, errors.New("文件大小上限无效")
	}
	return n, nil
}
func fileInfo(root, p string) (map[string]any, error) {
	st, e := os.Stat(p)
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(root, p)
	if e != nil {
		return nil, e
	}
	return map[string]any{"path": filepath.ToSlash(rel), "name": filepath.Base(p), "size": st.Size()}, nil
}
func (s *Server) platform(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method string         `json:"method"`
		Args   map[string]any `json:"args"`
	}
	if e := decode(r, maxCommand, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	var data any
	var err error
	switch in.Method {
	case "settings.get":
		data, err = s.settings()
	case "settings.save":
		err = s.saveSettings(in.Args)
	case "files.list":
		data, err = s.listFiles()
	case "files.read":
		var maximum int64
		maximum, err = limit(in.Args["limit"], 8<<20)
		if err == nil {
			var p string
			p, err = s.private(fmt.Sprint(in.Args["path"]), true)
			if err == nil {
				var b []byte
				b, err = readFile(p, maximum)
				if err == nil && !utf8.Valid(b) {
					err = errors.New("该文件不是有效 UTF-8 文本")
				}
				data = string(b)
			}
		}
	case "help.read":
		data = "iotools Web 版通过同源本机 Go 网关运行 HTTP、Kafka、MQTT、Modbus TCP 和 OPC UA。浏览器页面不能独立连接原生 TCP；关闭或隐藏页面会取消活动请求。30 秒失去会话心跳也会暂停，闲置 10 分钟后需刷新页面重新打开会话。文件保存在本机网关专属目录，导出由浏览器下载完成。Android USB 与原生串口不在 Web 能力范围。写入仍需逐次确认；不要在公共电脑保存正式凭据。"
	case "licenses.read":
		b, e := fs.ReadFile(s.assets, "assets/lib/core/platform/native/assets/LICENSE")
		data = string(b)
		err = e
	case "usb.list":
		data = "[]"
	case "usb.status":
		data = "Web 网关不提供 Android USB 接口"
	case "files.cancel":
		data = nil
	default:
		err = errors.New("当前平台不支持此操作")
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	success(w, data)
}
func (s *Server) settings() (map[string]any, error) {
	p, e := s.private(".iotools-settings.json", false)
	if e != nil {
		return nil, e
	}
	v := map[string]any{}
	if b, e := readFile(p, 16384); e == nil {
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, errors.New("本机设置损坏")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	out := map[string]any{"root": "", "platform": "web", "version": s.version, "sha": s.version, "theme": "dark", "readOnly": false, "history": true, "collection": "iotools.yaml", "usb": false, "capabilities": capabilities()}
	for _, k := range []string{"theme", "readOnly", "history", "collection"} {
		if value, ok := v[k]; ok {
			out[k] = value
		}
	}
	return out, nil
}
func (s *Server) saveSettings(args map[string]any) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	old, e := s.settings()
	if e != nil {
		return e
	}
	for k, v := range args {
		switch k {
		case "theme":
			if v != "dark" && v != "light" && v != "system" {
				return errors.New("外观设置无效")
			}
		case "history", "readOnly":
			if _, ok := v.(bool); !ok {
				return errors.New("开关设置必须是布尔值")
			}
		case "collection":
			p, ok := v.(string)
			if !ok {
				return errors.New("集合路径无效")
			}
			if _, e = s.private(p, false); e != nil {
				return e
			}
		default:
			return errors.New("不支持的本机设置")
		}
		old[k] = v
	}
	saved := map[string]any{}
	for _, k := range []string{"theme", "readOnly", "history", "collection"} {
		saved[k] = old[k]
	}
	b, _ := json.Marshal(saved)
	f, e := os.CreateTemp(s.root, ".iotools-settings-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	dest, e := s.private(".iotools-settings.json", false)
	if e != nil {
		return e
	}
	return os.Rename(name, dest)
}
func readFile(p string, max int64) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return nil, errors.New("文件类型或大小超出限制")
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, errors.New("文件超过上限")
	}
	return b, e
}
func (s *Server) listFiles() ([]map[string]any, error) {
	out := []map[string]any{}
	e := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == s.root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".iotools-") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= 10000 {
			return errors.New("文件数量超过 10000")
		}
		v, e := fileInfo(s.root, p)
		if e == nil {
			out = append(out, v)
		}
		return e
	})
	sort.Slice(out, func(i, j int) bool { return out[i]["path"].(string) < out[j]["path"].(string) })
	return out, e
}

// Portable paths also reject Windows device aliases and names that Windows
// normalizes to a different file. A bundle must have the same meaning on every
// gateway host, including Linux-created bundles later copied to Windows.
func portablePath(name string) bool {
	if !fs.ValidPath(name) || name == "." || len(strings.Split(name, "/")) > 32 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part != strings.TrimRight(part, ". ") || strings.ContainsAny(part, `\:<>"|?*`) {
			return false
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return false
			}
		}
		if reservedName(part) {
			return false
		}
	}
	return true
}
func reservedName(name string) bool {
	upper := strings.ToUpper(strings.TrimRight(strings.Split(name, ".")[0], " "))
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || upper == "CONIN$" || upper == "CONOUT$" {
		return true
	}
	if strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT") {
		suffix := upper[3:]
		return len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9' || suffix == "¹" || suffix == "²" || suffix == "³"
	}
	return false
}
func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune(`/\:<>"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if name == "" || name == "." || name == ".." {
		name = "data.bin"
	}
	for len(name) > 120 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = "data.bin"
	}
	if reservedName(name) {
		name = "_" + name
	}
	return name
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(b []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	default:
		return c.r.Read(b)
	}
}
func copyBounded(ctx context.Context, to string, from io.Reader, maximum int64) (int64, error) {
	f, e := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return 0, e
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(to)
		}
	}()
	n, e := io.Copy(f, io.LimitReader(contextReader{ctx, from}, maximum+1))
	if e == nil && n > maximum {
		e = errors.New("文件超过允许大小")
	}
	if e == nil {
		e = f.Sync()
	}
	if e == nil {
		e = ctx.Err()
	}
	if e == nil {
		e = f.Close()
	}
	ok = e == nil
	return n, e
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	maximum, e := limit(r.URL.Query().Get("limit"), maxFile)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if r.ContentLength > maximum {
		fail(w, 413, "上传文件超过上限")
		return
	}
	bundle := r.URL.Query().Get("bundle") == "1"
	prefix := "attachment-"
	if bundle {
		prefix = "bundle-"
	}
	directory, e := os.MkdirTemp(s.root, prefix)
	if e != nil {
		fail(w, 500, "无法创建导入目录")
		return
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(directory)
		}
	}()
	name := safeName(r.URL.Query().Get("name"))
	if bundle {
		name = ".source.zip"
	}
	p := filepath.Join(directory, name)
	_, e = copyBounded(r.Context(), p, r.Body, maximum)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	var data any
	if bundle {
		data, e = s.extract(r.Context(), p, directory, maximum)
		if e == nil {
			e = os.Remove(p)
		}
	} else {
		data, e = fileInfo(s.root, p)
	}
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e = r.Context().Err(); e != nil {
		fail(w, 400, "导入已取消")
		return
	}
	done = true
	success(w, data)
}
func (s *Server) extract(ctx context.Context, source, dir string, maximum int64) (any, error) {
	z, e := zip.OpenReader(source)
	if e != nil {
		return nil, errors.New("无法读取 ZIP 配置包")
	}
	defer z.Close()
	if len(z.File) < 1 || len(z.File) > 256 {
		return nil, errors.New("ZIP 必须包含 1–256 个项目")
	}
	seen := map[string]bool{}
	var declared uint64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !portablePath(name) {
			return nil, errors.New("ZIP 包含非法路径")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, errors.New("ZIP 包含重复或大小写冲突路径")
		}
		seen[key] = true
		if f.Mode()&os.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) || f.Flags&1 != 0 {
			return nil, errors.New("ZIP 不支持链接、特殊文件或加密内容")
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, errors.New("ZIP 仅支持 store 和 deflate")
		}
		if f.UncompressedSize64 > uint64(maximum) || declared > uint64(maximum)-f.UncompressedSize64 {
			return nil, errors.New("ZIP 解压总大小超过上限")
		}
		declared += f.UncompressedSize64
	}
	var total int64
	files := []map[string]any{}
	for _, f := range z.File {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		p := filepath.Join(dir, filepath.FromSlash(f.Name))
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(p, 0700); e != nil {
				return nil, e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return nil, e
		}
		input, e := f.Open()
		if e != nil {
			return nil, e
		}
		n, e := copyBounded(ctx, p, input, maximum-total)
		input.Close()
		if e != nil {
			return nil, e
		}
		if uint64(n) != f.UncompressedSize64 {
			return nil, errors.New("ZIP 声明大小与内容不一致")
		}
		total += n
		v, e := fileInfo(s.root, p)
		if e != nil {
			return nil, e
		}
		files = append(files, v)
	}
	rel, _ := filepath.Rel(s.root, dir)
	return map[string]any{"directory": filepath.ToSlash(rel), "files": files, "bytes": total}, nil
}
func (s *Server) prepareDownload(w http.ResponseWriter, r *http.Request, owner string) {
	var args struct {
		Path  *string `json:"path"`
		Text  *string `json:"text"`
		Name  string  `json:"name"`
		Limit any     `json:"limit"`
	}
	if e := decode(r, 64<<20, &args); e != nil {
		fail(w, 400, e.Error())
		return
	}
	maximum, e := limit(args.Limit, maxFile)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if (args.Path == nil) == (args.Text == nil) {
		fail(w, 400, "请选择文件或文本导出")
		return
	}
	t := ticket{owner: owner, name: safeName(args.Name), expiry: time.Now().Add(time.Minute), maximum: maximum}
	if args.Path != nil {
		t.path, e = s.private(*args.Path, true)
		if e == nil {
			st, se := os.Stat(t.path)
			e = se
			if e == nil && st.Size() > maximum {
				e = errors.New("文件超过导出上限")
			}
		}
	} else {
		if int64(len(*args.Text)) > maximum {
			e = errors.New("文本超过导出上限")
		} else {
			f, fe := os.CreateTemp(s.root, ".iotools-download-")
			e = fe
			if e == nil {
				t.path = f.Name()
				t.temporary = true
				_, e = io.WriteString(f, *args.Text)
				if ce := f.Close(); e == nil {
					e = ce
				}
			}
		}
	}
	if e != nil {
		if t.temporary {
			os.Remove(t.path)
		}
		fail(w, 400, e.Error())
		return
	}
	s.mu.Lock()
	if s.closed || r.Context().Err() != nil || len(s.tickets) >= 64 {
		closed := s.closed || r.Context().Err() != nil
		s.mu.Unlock()
		if t.temporary {
			os.Remove(t.path)
		}
		if closed {
			fail(w, 503, "导出已取消或网关已关闭")
			return
		}
		fail(w, 429, "待下载文件过多")
		return
	}
	id := randomToken()
	s.tickets[id] = t
	s.mu.Unlock()
	success(w, map[string]any{"url": "/api/download/" + id})
}
func (s *Server) download(w http.ResponseWriter, r *http.Request, owner string) {
	id := strings.TrimPrefix(r.URL.Path, "/api/download/")
	s.mu.Lock()
	t, ok := s.tickets[id]
	if ok && t.owner == owner {
		delete(s.tickets, id)
	}
	s.mu.Unlock()
	if ok && t.owner == owner && t.temporary {
		defer os.Remove(t.path)
	}
	if !ok || t.owner != owner || time.Now().After(t.expiry) {
		fail(w, 404, "下载票据已过期或已使用")
		return
	}
	rel, e := filepath.Rel(s.root, t.path)
	if e != nil {
		fail(w, 400, "无效下载文件")
		return
	}
	p, e := s.private(filepath.ToSlash(rel), true)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	f, e := os.Open(p)
	if e != nil {
		fail(w, 404, "下载文件不存在")
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > t.maximum {
		fail(w, 400, "无效下载文件或内容已超过导出上限")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": t.name}))
	http.ServeContent(w, r, t.name, st.ModTime(), f)
}
