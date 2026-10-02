// Package webhost serves the Flutter UI and real protocol engine on loopback only.
package webhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Live-yum/iotools/internal/mobileapi"
)

const maxClients = 16
const idleRetention = 10 * time.Minute
const maxCommand = 8 << 20
const maxFile = int64(8) << 30

type Options struct {
	Root, Version string
	Assets        fs.FS
	Lease         time.Duration
}
type client struct {
	csrf string
	last time.Time
}
type session struct {
	owner  string
	engine *mobileapi.Session
	last   time.Time
}
type ticket struct {
	owner, path, name string
	temporary         bool
	expiry            time.Time
	maximum           int64
}
type Server struct {
	root, version, host, origin string
	assets                      fs.FS
	lease                       time.Duration
	mu                          sync.Mutex
	clients                     map[string]*client
	sessions                    map[string]*session
	tickets                     map[string]ticket
	stop                        chan struct{}
	ctx                         context.Context
	cancel                      context.CancelFunc
	once                        sync.Once
	filesMu                     sync.Mutex
	closed                      bool
}

func New(listener net.Listener, options Options) (*Server, error) {
	tcp, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		return nil, errors.New("Web gateway must bind an explicit loopback address")
	}
	if options.Assets == nil {
		return nil, errors.New("compiled Flutter web assets are required")
	}
	for _, name := range []string{"index.html", "main.dart.js", "flutter_bootstrap.js"} {
		f, e := options.Assets.Open(name)
		if e != nil {
			return nil, fmt.Errorf("missing compiled Flutter asset %s", name)
		}
		st, e := f.Stat()
		f.Close()
		if e != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("compiled Flutter asset %s must be a regular file", name)
		}
	}
	if !filepath.IsAbs(options.Root) || filepath.Dir(filepath.Clean(options.Root)) == filepath.Clean(options.Root) {
		return nil, errors.New("private root must be an absolute non-root directory")
	}
	if e := os.MkdirAll(options.Root, 0700); e != nil {
		return nil, e
	}
	root, e := filepath.EvalSymlinks(options.Root)
	if e != nil {
		return nil, e
	}
	if filepath.Dir(root) == root {
		return nil, errors.New("private root must not resolve to a filesystem root")
	}
	lease := options.Lease
	if lease <= 0 {
		lease = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ctx: ctx, cancel: cancel, root: root, version: options.Version, host: listener.Addr().String(), origin: "http://" + listener.Addr().String(), assets: options.Assets, lease: lease, clients: map[string]*client{}, sessions: map[string]*session{}, tickets: map[string]ticket{}, stop: make(chan struct{})}
	go s.reap()
	return s, nil
}
func (s *Server) URL() string { return s.origin }
func randomToken() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func success(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}
func fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": message})
}
func (s *Server) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.stop)
		s.cancel()
		sessions := s.sessions
		s.sessions = map[string]*session{}
		tickets := s.tickets
		s.tickets = map[string]ticket{}
		s.mu.Unlock()
		for _, v := range sessions {
			if v.engine != nil {
				v.engine.Close()
			}
		}
		for _, t := range tickets {
			if t.temporary {
				os.Remove(t.path)
			}
		}
	})
}
func (s *Server) reap() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.mu.Lock()
			s.reapLocked(now)
			for key, v := range s.tickets {
				if now.After(v.expiry) {
					delete(s.tickets, key)
					if v.temporary {
						os.Remove(v.path)
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

// The gateway mutex orders lease expiry with explicit resume, so a stale
// reaper decision can never pause a newly resumed tab. Engine lifecycle methods
// only cancel local work and do not wait on protocol I/O.
func (s *Server) reapLocked(now time.Time) {
	for id, v := range s.sessions {
		if v.engine == nil {
			continue
		}
		if now.Sub(v.last) > max(idleRetention, s.lease) {
			v.engine.Close()
			delete(s.sessions, id)
		} else if now.Sub(v.last) > s.lease {
			v.engine.Pause()
		}
	}
	for id, c := range s.clients {
		if now.Sub(c.last) > max(idleRetention, s.lease) {
			delete(s.clients, id)
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		fail(w, http.StatusServiceUnavailable, "网关已关闭")
		return
	}
	// Also cancel in-flight file copies when Close is called by an embedding
	// host that does not use Serve's HTTP server.
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	r = r.WithContext(ctx)
	if r.Host != s.host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "请从本机网关的原始地址打开应用")
		return
	}
	if r.URL.Path == "/api/bootstrap" {
		if r.Method != http.MethodGet {
			fail(w, 405, "不支持的请求方法")
			return
		}
		s.bootstrap(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		s.asset(w, r)
		return
	}
	owner, c := s.authenticate(r)
	if c == nil {
		fail(w, 403, "本机会话已失效，请刷新页面")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/download/") {
		if r.Method != http.MethodGet {
			fail(w, 405, "不支持的请求方法")
			return
		}
		s.download(w, r, owner)
		return
	}
	if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Iotools-CSRF")), []byte(c.csrf)) != 1 {
		fail(w, 403, "本机请求校验失败")
		return
	}
	switch r.URL.Path {
	case "/api/open":
		s.open(w, r, owner)
	case "/api/command", "/api/lifecycle":
		s.command(w, r, owner)
	case "/api/platform":
		s.platform(w, r)
	case "/api/files/upload":
		s.upload(w, r)
	case "/api/files/download":
		s.prepareDownload(w, r, owner)
	default:
		fail(w, 404, "本机接口不存在")
	}
}
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cookie, _ := r.Cookie("iotools_client")
	var id string
	var c *client
	if cookie != nil {
		id = cookie.Value
		c = s.clients[id]
	}
	if s.closed {
		fail(w, 503, "网关已关闭")
		return
	}
	if c == nil {
		s.reapLocked(time.Now())
		if len(s.clients) >= maxClients {
			fail(w, 429, "本机浏览器会话过多，请关闭网关后重新启动")
			return
		}
		id = randomToken()
		c = &client{csrf: randomToken()}
		s.clients[id] = c
	}
	c.last = time.Now()
	http.SetCookie(w, &http.Cookie{Name: "iotools_client", Value: id, Path: "/api/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	success(w, map[string]any{"csrf": c.csrf, "capabilities": capabilities(), "version": s.version})
}
func (s *Server) authenticate(r *http.Request) (string, *client) {
	cookie, e := r.Cookie("iotools_client")
	if e != nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.clients[cookie.Value]
	if c != nil {
		c.last = time.Now()
		copy := *c
		return cookie.Value, &copy
	}
	return "", nil
}
func body(r *http.Request, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("请求内容超过上限")
	}
	return b, nil
}
func decode(r *http.Request, limit int64, out any) error {
	b, e := body(r, limit)
	if e != nil {
		return e
	}
	if trimmed := bytes.TrimSpace(b); len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("请求必须是单个 JSON 对象")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	if e = d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("请求必须是单个 JSON 对象")
	}
	return nil
}
func (s *Server) open(w http.ResponseWriter, r *http.Request, owner string) {
	var input struct {
		Path     string `json:"path"`
		ReadOnly bool   `json:"readOnly"`
		History  bool   `json:"history"`
	}
	if e := decode(r, maxCommand, &input); e != nil {
		fail(w, 400, e.Error())
		return
	}
	require := input.Path != ""
	if input.Path == "" {
		input.Path = "iotools.yaml"
	}
	file, e := s.private(input.Path, false)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	s.mu.Lock()
	s.reapLocked(time.Now())
	if !s.closed && len(s.sessions) >= maxClients {
		// Reloaded/abandoned tabs may not deliver pagehide. Reclaim only an
		// expired lease; never evict a live browser session to admit another.
		var oldestID string
		for key, v := range s.sessions {
			if v.engine != nil && time.Since(v.last) > s.lease && (oldestID == "" || v.last.Before(s.sessions[oldestID].last)) {
				oldestID = key
			}
		}
		if oldestID != "" {
			s.sessions[oldestID].engine.Close()
			delete(s.sessions, oldestID)
		}
	}
	if s.closed || len(s.sessions) >= maxClients {
		s.mu.Unlock()
		fail(w, 429, "本机会话过多")
		return
	}
	id := randomToken()
	s.sessions[id] = &session{owner: owner, last: time.Now()}
	s.mu.Unlock()
	engine, e := mobileapi.Open(file, s.version, mobileapi.Options{ReadOnly: input.ReadOnly, History: input.History, PrivateRoot: s.root, RequireExisting: require})
	if e != nil {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		fail(w, 400, e.Error())
		return
	}
	s.mu.Lock()
	if s.closed {
		delete(s.sessions, id)
		s.mu.Unlock()
		engine.Close()
		fail(w, 503, "网关已关闭")
		return
	}
	s.sessions[id] = &session{owner: owner, engine: engine, last: time.Now()}
	s.mu.Unlock()
	raw := s.relativeReply(engine.Command(`{"op":"state"}`))
	var envelope map[string]json.RawMessage
	json.Unmarshal([]byte(raw), &envelope)
	encoded, _ := json.Marshal(id)
	envelope["session"] = encoded
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(envelope)
}
func (s *Server) command(w http.ResponseWriter, r *http.Request, owner string) {
	id := r.Header.Get("X-Iotools-Session")
	s.mu.Lock()
	v := s.sessions[id]
	if v == nil || v.owner != owner || v.engine == nil {
		s.mu.Unlock()
		fail(w, 404, "本机会话已关闭")
		return
	}
	v.last = time.Now()
	s.mu.Unlock()
	if r.URL.Path == "/api/lifecycle" {
		var input struct {
			Action string `json:"action"`
		}
		if e := decode(r, 1024, &input); e != nil {
			fail(w, 400, e.Error())
			return
		}
		switch input.Action {
		case "pause", "resume":
			s.mu.Lock()
			if input.Action == "pause" {
				v.engine.Pause()
			} else {
				v.last = time.Now()
				v.engine.Resume()
			}
			s.mu.Unlock()
		case "close":
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
			if v.engine != nil {
				v.engine.Close()
			}
		default:
			fail(w, 400, "未知生命周期操作")
			return
		}
		success(w, nil)
		return
	}
	b, e := body(r, maxCommand)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, s.relativeReply(v.engine.Command(string(b))))
}
func (s *Server) relativeReply(raw string) string {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &top) != nil {
		return raw
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(top["data"], &data) != nil {
		return raw
	}
	var p string
	if json.Unmarshal(data["path"], &p) != nil || !filepath.IsAbs(p) {
		return raw
	}
	rel, e := filepath.Rel(s.root, p)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return raw
	}
	data["path"], _ = json.Marshal(filepath.ToSlash(rel))
	top["data"], _ = json.Marshal(data)
	b, _ := json.Marshal(top)
	return string(b)
}
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		fail(w, 405, "不支持的请求方法")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	if !fs.ValidPath(name) || strings.HasPrefix(name, "api/") {
		http.NotFound(w, r)
		return
	}
	f, e := s.assets.Open(path.Clean(name))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	seeker, ok := f.(io.ReadSeeker)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), seeker)
}
func (s *Server) private(handle string, existing bool) (string, error) {
	if !portablePath(handle) {
		return "", errors.New("文件路径必须是应用内相对路径")
	}
	p := s.root
	for _, part := range strings.Split(handle, "/") {
		p = filepath.Join(p, part)
		st, e := os.Lstat(p)
		if e == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("不支持符号链接")
		}
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
	}
	if existing {
		st, e := os.Stat(p)
		if e != nil || !st.Mode().IsRegular() {
			return "", errors.New("请选择已存在的应用文件")
		}
	}
	return p, nil
}
func capabilities() map[string]any {
	return map[string]any{"native_protocols": true, "serial": false, "usb": false, "file_import": true, "file_export": true, "large_file_streaming": true, "max_transfer_bytes": maxFile, "background_policy": "cancel_when_hidden_or_lease_expires", "gateway_required": true}
}

// Serve serves on the already-bound listener; Close cancels every engine session.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	if addr, ok := l.Addr().(*net.TCPAddr); !ok || !addr.IP.IsLoopback() || addr.String() != s.host {
		return errors.New("Web gateway must serve on its original loopback listener")
	}
	h := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 40 * time.Second, MaxHeaderBytes: 16 << 10}
	defer h.Close()
	stopped, returned := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
		case <-s.stop:
		case <-returned:
			return
		}
		s.Close()
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if h.Shutdown(deadline) != nil {
			// Shutdown alone does not terminate stalled request bodies.
			h.Close()
		}
	}()
	e := h.Serve(l)
	close(returned)
	s.Close()
	<-stopped
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
