package webhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func rejectionRequest(t *testing.T, c *testClient, method, path, payload string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, c.s.URL()+path, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", c.s.URL())
	r.Header.Set("X-Iotools-CSRF", c.csrf)
	r.Header.Set("X-Iotools-Session", c.session)
	for _, cookie := range c.http.Jar.Cookies(r.URL) {
		r.AddCookie(cookie)
	}
	return r
}

func fragmentedRejection(t *testing.T, r *http.Request, splitBody int) (*http.Response, []byte) {
	t.Helper()
	r.Close = true
	var wire bytes.Buffer
	if err := r.Write(&wire); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp4", r.URL.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	split := bytes.Index(wire.Bytes(), []byte("\r\n\r\n")) + 4 + splitBody
	if _, err = conn.Write(wire.Bytes()[:split]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err = conn.Write(wire.Bytes()[split:]); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("incomplete rejection body", err)
	}
	return response, body
}

// Exercise each error-producing route over an actual fragmented HTTP/1 close,
// including unread, partially consumed and fully consumed request bodies.
func TestGatewayErrorRouteFragmentationMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, payload, message string
		status                              int
		plain                               bool
		prepare                             func(*testing.T, *testClient, *http.Request)
	}{
		{name: "closed-gateway", method: "POST", path: "/api/platform", status: 503, message: "网关已关闭", prepare: func(t *testing.T, c *testClient, r *http.Request) { c.s.Close() }},
		{name: "bootstrap-method", method: "POST", path: "/api/bootstrap", status: 405, message: "不支持的请求方法"},
		{name: "bootstrap-capacity", method: "GET", path: "/api/bootstrap", status: 429, message: "本机浏览器会话过多，请关闭网关后重新启动", prepare: func(t *testing.T, c *testClient, r *http.Request) {
			r.Header.Del("Cookie")
			c.s.mu.Lock()
			for len(c.s.clients) < maxClients {
				c.s.clients[fmt.Sprint(len(c.s.clients))] = &client{last: time.Now()}
			}
			c.s.mu.Unlock()
		}},
		{name: "unknown-api", method: "POST", path: "/api/missing", status: 404, message: "本机接口不存在"},
		{name: "missing-cookie", method: "POST", path: "/api/platform", status: 403, message: "本机会话已失效，请刷新页面", prepare: func(t *testing.T, c *testClient, r *http.Request) { r.Header.Del("Cookie") }},
		{name: "asset-method", method: "POST", path: "/index.html", status: 405, message: "不支持的请求方法"},
		{name: "asset-missing", method: "GET", path: "/missing.js", status: 404, plain: true, message: "404 page not found\n"},
		{name: "asset-invalid-path", method: "GET", path: "/../secret", status: 404, plain: true, message: "404 page not found\n"},
		{name: "asset-precondition", method: "GET", path: "/index.html", status: 412, plain: true, prepare: func(t *testing.T, c *testClient, r *http.Request) { r.Header.Set("If-Match", `"missing"`) }},
		{name: "asset-range", method: "GET", path: "/index.html", status: 416, plain: true, message: "invalid range: failed to overlap\n", prepare: func(t *testing.T, c *testClient, r *http.Request) { r.Header.Set("Range", "bytes=99999-") }},
		{name: "missing-command-session", method: "POST", path: "/api/command", status: 404, message: "本机会话已关闭", prepare: func(t *testing.T, c *testClient, r *http.Request) { r.Header.Set("X-Iotools-Session", "missing") }},
		{name: "missing-lifecycle-session", method: "POST", path: "/api/lifecycle", status: 404, message: "本机会话已关闭", prepare: func(t *testing.T, c *testClient, r *http.Request) { r.Header.Del("X-Iotools-Session") }},
		{name: "closed-command-session", method: "POST", path: "/api/command", status: 404, message: "本机会话已关闭", prepare: func(t *testing.T, c *testClient, r *http.Request) { c.ok("/api/lifecycle", map[string]any{"action": "close"}) }},
		{name: "foreign-command-session", method: "POST", path: "/api/command", status: 404, message: "本机会话已关闭", prepare: rejectionForeignClient},
		{name: "foreign-lifecycle-session", method: "POST", path: "/api/lifecycle", status: 404, message: "本机会话已关闭", prepare: rejectionForeignClient},
		{name: "partial-lifecycle-overflow", method: "POST", path: "/api/lifecycle", payload: strings.Repeat("x", 4096), status: 400, message: "请求内容超过上限"},
		{name: "fully-read-lifecycle-invalid", method: "POST", path: "/api/lifecycle", payload: `{"action":"invalid"}`, status: 400, message: "未知生命周期操作"},
		{name: "fully-read-open-json", method: "POST", path: "/api/open", payload: `{} {}`, status: 400, message: "请求必须是单个 JSON 对象"},
		{name: "fully-read-platform-method", method: "POST", path: "/api/platform", payload: `{"method":"unknown"}`, status: 400, message: "当前平台不支持此操作"},
		{name: "fully-read-download-choice", method: "POST", path: "/api/files/download", payload: `{}`, status: 400, message: "请选择文件或文本导出"},
		{name: "download-method", method: "POST", path: "/api/download/missing", status: 405, message: "不支持的请求方法"},
		{name: "download-missing", method: "GET", path: "/api/download/missing", status: 404, message: "下载票据已过期或已使用"},
		{name: "upload-limit", method: "POST", path: "/api/files/upload?limit=invalid", status: 400, message: "文件大小上限无效"},
		{name: "upload-oversize", method: "POST", path: "/api/files/upload?limit=1", status: 413, message: "上传文件超过上限"},
		{name: "fully-read-upload-bundle", method: "POST", path: "/api/files/upload?bundle=1&limit=1024", payload: "not a ZIP", status: 400, message: "无法读取 ZIP 配置包"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t)
			c.open(map[string]any{})
			c.ok("/api/platform", map[string]any{"method": "settings.save", "args": map[string]any{"theme": "dark"}})
			payload := tc.payload
			if payload == "" {
				payload = `{"method":"settings.save","args":{"theme":"light"}}`
			}
			r := rejectionRequest(t, c, tc.method, tc.path, payload)
			if tc.prepare != nil {
				tc.prepare(t, c, r)
			}
			before := uploadRejectionTree(t, c.s.root)
			response, body := fragmentedRejection(t, r, len(payload)/2)
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d body=%q", response.StatusCode, body)
			}
			if tc.plain {
				if string(body) != tc.message {
					t.Fatalf("changed standard HTTP error: %q", body)
				}
			} else {
				var envelope struct {
					OK    *bool  `json:"ok"`
					Error string `json:"error"`
				}
				if err := json.Unmarshal(body, &envelope); err != nil || envelope.OK == nil || *envelope.OK || envelope.Error != tc.message {
					t.Fatalf("invalid error envelope: %q (%v)", body, err)
				}
			}
			if after := uploadRejectionTree(t, c.s.root); !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed files or settings")
			}
			if tc.name != "closed-gateway" {
				if settings := c.ok("/api/platform", map[string]any{"method": "settings.get"})["data"].(map[string]any); settings["theme"] != "dark" {
					t.Fatal("rejection changed settings")
				}
			}
		})
	}
}

func rejectionForeignClient(t *testing.T, c *testClient, r *http.Request) {
	t.Helper()
	c.s.mu.Lock()
	c.s.clients["foreign-client"] = &client{csrf: "foreign-csrf", last: time.Now()}
	c.s.mu.Unlock()
	r.Header.Del("Cookie")
	r.AddCookie(&http.Cookie{Name: "iotools_client", Value: "foreign-client"})
	r.Header.Set("X-Iotools-CSRF", "foreign-csrf")
}

type terminalErrorReader struct {
	data []byte
	err  error
}

func (r *terminalErrorReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestRejectedBodyStateAndOriginalLengthMatrix(t *testing.T) {
	for _, tc := range []struct {
		name           string
		length, before int64
		readToEOF      bool
		terminal       error
		wantDrain      int64
		wantClose      bool
	}{
		{name: "unread", length: 6, wantDrain: 6},
		{name: "partial", length: 6, before: 2, wantDrain: 4},
		{name: "boundary", length: maxRejectedBodyDrain, wantDrain: maxRejectedBodyDrain},
		{name: "over-boundary", length: maxRejectedBodyDrain + 1, wantClose: true},
		{name: "original-large-short-tail", length: maxRejectedBodyDrain + 1, before: maxRejectedBodyDrain, wantClose: true},
		{name: "fully-read", length: 6, before: 6},
		{name: "fully-read-large", length: maxRejectedBodyDrain + 1, before: maxRejectedBodyDrain + 1},
		{name: "clean-eof", length: 6, readToEOF: true},
		{name: "bytes-and-eof", length: 6, terminal: io.EOF, wantDrain: 6},
		{name: "consumed-bytes-and-eof", length: 6, terminal: io.EOF, readToEOF: true},
		{name: "unknown-clean-eof", length: -1, readToEOF: true},
		{name: "read-error", length: 6, terminal: errors.New("read failed"), readToEOF: true, wantClose: true},
		{name: "unexpected-eof-then-eof", length: 6, terminal: io.ErrUnexpectedEOF, readToEOF: true, wantClose: true},
		{name: "drain-error-at-full-length", length: 6, terminal: io.ErrUnexpectedEOF, wantDrain: 6, wantClose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			length := tc.length
			if length < 0 {
				length = 6
			}
			data := bytes.Repeat([]byte{'x'}, int(length))
			var input io.Reader = bytes.NewReader(data)
			if tc.terminal != nil {
				input = &terminalErrorReader{data: data, err: tc.terminal}
			}
			counted := &rejectionBody{reader: input}
			r := httptest.NewRequest("POST", "http://127.0.0.1/api/missing", nil)
			r.Body, r.ContentLength = counted, tc.length
			tracked := trackRequestBody(r)
			if tc.readToEOF {
				_, _ = io.Copy(io.Discard, r.Body)
				// Model the native body's next EOF after an UnexpectedEOF.
				_, _ = r.Body.Read(make([]byte, 1))
			} else if tc.before > 0 {
				if _, err := io.CopyN(io.Discard, r.Body, tc.before); err != nil {
					t.Fatal(err)
				}
			}
			consumed, reads := tracked.consumed, counted.reads
			w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
			disposeRejectedBody(w, r)
			if got := tracked.consumed - consumed; got != tc.wantDrain {
				t.Fatalf("drained %d, want %d", got, tc.wantDrain)
			}
			if tc.wantDrain == 0 && counted.reads != reads {
				t.Fatal("already consumed or unsafe body was read again")
			}
			if got := w.Header().Get("Connection") == "close"; got != tc.wantClose {
				t.Fatalf("close=%v, want %v", got, tc.wantClose)
			}
			if tc.wantDrain == 0 && !tc.wantClose && len(w.deadlines) != 0 {
				t.Fatal("healthy consumed body changed connection deadlines")
			}
			reads = counted.reads
			disposeRejectedBody(w, r)
			if counted.reads != reads {
				t.Fatal("disposal ran twice")
			}
		})
	}
}

func TestRejectedBodyDeadlineContextAndFraming(t *testing.T) {
	t.Run("short-context-deadline", func(t *testing.T) {
		deadline := time.Now().Add(100 * time.Millisecond)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		r := httptest.NewRequest("POST", "http://127.0.0.1/", strings.NewReader("body")).WithContext(ctx)
		w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
		disposeRejectedBody(w, r)
		if len(w.deadlines) != 2 || !w.deadlines[0].Equal(deadline) || !w.deadlines[1].IsZero() {
			t.Fatal("request deadline was not respected", w.deadlines)
		}
	})
	t.Run("cancelled-no-read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b := &rejectionBody{reader: strings.NewReader("body")}
		r := httptest.NewRequest("POST", "http://127.0.0.1/", nil).WithContext(ctx)
		r.Body, r.ContentLength = b, 4
		w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
		disposeRejectedBody(w, r)
		if b.reads != 0 || w.Header().Get("Connection") != "close" {
			t.Fatal("cancelled body was drained")
		}
	})
	t.Run("clean-eof-shorter-than-declared", func(t *testing.T) {
		r := httptest.NewRequest("POST", "http://127.0.0.1/", strings.NewReader("short"))
		r.ContentLength = 20
		trackRequestBody(r)
		_, _ = io.Copy(io.Discard, r.Body)
		w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
		disposeRejectedBody(w, r)
		if w.Header().Get("Connection") != "close" {
			t.Fatal("inconsistent framing reused the connection")
		}
	})
	t.Run("original-length-cannot-be-expanded", func(t *testing.T) {
		input := strings.NewReader("bodyNEXT")
		r := httptest.NewRequest("POST", "http://127.0.0.1/", nil)
		r.Body, r.ContentLength = io.NopCloser(input), 4
		trackRequestBody(r)
		r.ContentLength = 8
		w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
		disposeRejectedBody(w, r)
		rest, _ := io.ReadAll(input)
		if string(rest) != "NEXT" {
			t.Fatal("drain crossed the original request boundary")
		}
	})
}

type blockedRejectionBody struct {
	started, release chan struct{}
}

func (b *blockedRejectionBody) Read(p []byte) (int, error) {
	close(b.started)
	<-b.release
	return copy(p, "body"), io.EOF
}
func (b *blockedRejectionBody) Close() error { return nil }

func TestBootstrapRejectionDoesNotHoldGatewayMutexDuringDrain(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprint("closed=", closed), func(t *testing.T) {
			c := fixture(t)
			if closed {
				c.s.Close()
			} else {
				c.s.mu.Lock()
				for len(c.s.clients) < maxClients {
					c.s.clients[fmt.Sprint(len(c.s.clients))] = &client{last: time.Now()}
				}
				c.s.mu.Unlock()
			}
			b := &blockedRejectionBody{started: make(chan struct{}), release: make(chan struct{})}
			r := httptest.NewRequest("GET", c.s.URL()+"/api/bootstrap", nil)
			r.Body, r.ContentLength = b, 4
			w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
			done := make(chan struct{})
			go func() {
				defer close(done)
				// Direct entry also covers a Close racing the dispatch check.
				c.s.bootstrap(w, r)
			}()
			defer func() { close(b.release); <-done }()
			select {
			case <-b.started:
			case <-time.After(time.Second):
				t.Fatal("bootstrap error did not attempt its bounded drain")
			}
			unlocked := make(chan struct{})
			go func() { c.s.mu.Lock(); c.s.mu.Unlock(); close(unlocked) }()
			select {
			case <-unlocked:
			case <-time.After(time.Second):
				t.Fatal("bootstrap held the gateway mutex during network I/O")
			}
		})
	}
}

func TestRejectionDoesNotCrossPipelinedRequestBoundary(t *testing.T) {
	c := fixture(t)
	first := rejectionRequest(t, c, "POST", "/api/command", `{"op":"state"}`)
	first.Header.Set("X-Iotools-Session", "missing")
	second := rejectionRequest(t, c, "POST", "/api/platform", `{"method":"settings.get"}`)
	second.Close = true
	var wire bytes.Buffer
	if err := first.Write(&wire); err != nil {
		t.Fatal(err)
	}
	if err := second.Write(&wire); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp4", first.URL.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = conn.Write(wire.Bytes()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	for i, request := range []*http.Request{first, second} {
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		want := http.StatusNotFound
		if i == 1 {
			want = http.StatusOK
		}
		if err != nil || response.StatusCode != want || !json.Valid(body) {
			t.Fatalf("pipeline response %d: status=%d body=%q err=%v", i, response.StatusCode, body, err)
		}
	}
}

func TestRejectedProtocolCommandCannotConsumeConfirmation(t *testing.T) {
	c := fixture(t)
	var writes atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	source := `{"version":1,"requests":[{"id":"write","protocol":"http","action":"POST","endpoint":"` + target.URL + `","params":{"json":{"hello":"test"}}}]}`
	if err := os.WriteFile(filepath.Join(c.s.root, "write.json"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	c.open(map[string]any{"path": "write.json"})
	token := c.preview("write")
	payload, err := json.Marshal(map[string]any{"op": "run", "token": token, "confirmed": true})
	if err != nil {
		t.Fatal(err)
	}
	before := uploadRejectionTree(t, c.s.root)
	for _, foreign := range []bool{false, true} {
		r := rejectionRequest(t, c, "POST", "/api/command", string(payload))
		if foreign {
			rejectionForeignClient(t, c, r)
		} else {
			r.Header.Set("X-Iotools-Session", "missing")
		}
		response, _ := fragmentedRejection(t, r, len(payload)/2)
		if response.StatusCode != http.StatusNotFound || writes.Load() != 0 {
			t.Fatal("rejected command performed protocol work")
		}
	}
	if after := uploadRejectionTree(t, c.s.root); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected protocol command changed files")
	}
	c.ok("/api/command", map[string]any{"op": "run", "token": token, "confirmed": true})
	if result := c.waitDone(); result["status"] != "completed" || writes.Load() != 1 {
		t.Fatal("rejection consumed or replayed the legitimate confirmation", result, writes.Load())
	}
}

func TestDownloadRejectionPreservesTicketOwnershipAndCleanup(t *testing.T) {
	c := fixture(t)
	for _, mode := range []string{"foreign", "expired", "range", "precondition", "changed-file"} {
		t.Run(mode, func(t *testing.T) {
			result := c.ok("/api/files/download", map[string]any{"text": "secret", "name": "secret.txt", "limit": 6})
			path := result["data"].(map[string]any)["url"].(string)
			id := strings.TrimPrefix(path, "/api/download/")
			c.s.mu.Lock()
			ticket := c.s.tickets[id]
			if mode == "expired" {
				ticket.expiry = time.Now().Add(-time.Second)
				c.s.tickets[id] = ticket
			}
			c.s.mu.Unlock()
			r := rejectionRequest(t, c, "GET", path, "unread body")
			status := http.StatusNotFound
			switch mode {
			case "foreign":
				rejectionForeignClient(t, c, r)
			case "range":
				r.Header.Set("Range", "bytes=9999-")
				status = http.StatusRequestedRangeNotSatisfiable
			case "precondition":
				r.Header.Set("If-Match", `"missing"`)
				status = http.StatusPreconditionFailed
			case "changed-file":
				if err := os.WriteFile(ticket.path, []byte("too large"), 0600); err != nil {
					t.Fatal(err)
				}
				status = http.StatusBadRequest
			}
			response, _ := fragmentedRejection(t, r, 3)
			if response.StatusCode != status {
				t.Fatal("download rejection status changed", response.StatusCode)
			}
			c.s.mu.Lock()
			_, remains := c.s.tickets[id]
			c.s.mu.Unlock()
			if remains != (mode == "foreign") {
				t.Fatal("ticket ownership/consumption changed")
			}
			if mode == "foreign" {
				response := c.request("GET", path, nil)
				data, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 || string(data) != "secret" {
					t.Fatal("foreign rejection consumed the owner's download")
				}
			}
			if _, err := os.Stat(ticket.path); !os.IsNotExist(err) {
				t.Fatal("consumed temporary download was not removed", err)
			}
		})
	}
}

type headerObservedWriter struct {
	*rejectionWriter
	beforeHeader func(int)
}

func (w *headerObservedWriter) WriteHeader(status int) {
	w.beforeHeader(status)
	w.rejectionWriter.WriteHeader(status)
}

type failedContentSeeker struct{ *strings.Reader }

func (s failedContentSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekEnd {
		return 0, errors.New("content size unavailable")
	}
	return s.Reader.Seek(offset, whence)
}

func TestServeContentErrorDisposalPreservesSuccessAndStandardResponses(t *testing.T) {
	for _, tc := range []struct {
		name, method, header, value string
		status                     int
		failedSeek                 bool
	}{
		{name: "get", method: "GET", status: 200},
		{name: "head", method: "HEAD", status: 200},
		{name: "range", method: "GET", header: "Range", value: "bytes=0-2", status: 206},
		{name: "not-modified", method: "GET", header: "If-Modified-Since", value: "Wed, 01 Jan 2025 00:00:00 GMT", status: 304},
		{name: "precondition", method: "GET", header: "If-Match", value: `"missing"`, status: 412},
		{name: "invalid-range", method: "GET", header: "Range", value: "bytes=999-", status: 416},
		{name: "failed-seek", method: "GET", status: 500, failedSeek: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1/file.txt", strings.NewReader("request body"))
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			tracked := trackRequestBody(r)
			underlying := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
			underlying.Header().Set("Content-Type", "text/plain")
			observed := &headerObservedWriter{rejectionWriter: underlying, beforeHeader: func(status int) {
				if status >= 400 && tracked.consumed != tracked.length {
					t.Error("error headers committed before the body was drained")
				}
			}}
			content := func() io.ReadSeeker {
				if tc.failedSeek {
					return failedContentSeeker{strings.NewReader("content")}
				}
				return strings.NewReader("content")
			}
			modified := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			http.ServeContent(&contentResponseWriter{ResponseWriter: observed, request: r}, r, "file.txt", modified, content())
			baseline := httptest.NewRecorder()
			baseline.Header().Set("Content-Type", "text/plain")
			http.ServeContent(baseline, r, "file.txt", modified, content())
			if underlying.Code != tc.status || underlying.Code != baseline.Code || underlying.Body.String() != baseline.Body.String() || !reflect.DeepEqual(underlying.Header(), baseline.Header()) {
				t.Fatalf("ServeContent response changed: status=%d body=%q", underlying.Code, underlying.Body.String())
			}
			if tc.status < 400 && (tracked.consumed != 0 || len(underlying.deadlines) != 0) {
				t.Fatal("successful content response pre-read the request body")
			}
		})
	}
}

type contentFastWriter struct {
	*rejectionWriter
	readFromCalls int
}

func (w *contentFastWriter) ReadFrom(r io.Reader) (int64, error) {
	w.readFromCalls++
	return io.Copy(w.ResponseRecorder, r)
}

func TestContentResponseWriterDelegatesAndCommitsOnlyOnce(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(fmt.Sprint("fast=", fast), func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1/", strings.NewReader("unread"))
			tracked := trackRequestBody(r)
			base := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
			fastWriter := &contentFastWriter{rejectionWriter: base}
			var underlying http.ResponseWriter = base
			if fast {
				underlying = fastWriter
			}
			w := &contentResponseWriter{ResponseWriter: underlying, request: r}
			if err := http.NewResponseController(w).SetReadDeadline(time.Time{}); err != nil {
				t.Fatal("Unwrap did not preserve response controller support", err)
			}
			if n, err := w.ReadFrom(strings.NewReader("content")); err != nil || n != 7 {
				t.Fatal(n, err)
			}
			w.WriteHeader(http.StatusBadRequest)
			if base.Code != 200 || base.Body.String() != "content" || tracked.consumed != 0 || (fastWriter.readFromCalls == 1) != fast {
				t.Fatal("stream delegation or implicit status changed")
			}
		})
	}
}

func TestRejectedFramingReturnsFinalErrorWithoutWaitingForBody(t *testing.T) {
	c := fixture(t)
	for _, tc := range []struct {
		name, framing string
	}{
		{name: "expect-continue", framing: "Content-Length: 4\r\nExpect: 100-continue\r\n"},
		{name: "chunked", framing: "Transfer-Encoding: chunked\r\n"},
		{name: "oversized", framing: fmt.Sprintf("Content-Length: %d\r\n", maxRejectedBodyDrain+1)},
		{name: "stalled-small", framing: "Content-Length: 4\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rejectionRequest(t, c, "POST", "/api/missing", "")
			conn, err := net.DialTimeout("tcp4", r.URL.Host, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			headers := "POST /api/missing HTTP/1.1\r\nHost: " + r.Host + "\r\nOrigin: " + c.s.URL() + "\r\nX-Iotools-CSRF: " + c.csrf + "\r\nCookie: " + r.Header.Get("Cookie") + "\r\n" + tc.framing + "\r\n"
			start := time.Now()
			if _, err = io.WriteString(conn, headers); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 404 || !json.Valid(body) || !response.Close || time.Since(start) > time.Second {
				t.Fatalf("unsafe framing did not return a bounded final rejection: status=%d close=%v body=%q err=%v", response.StatusCode, response.Close, body, err)
			}
		})
	}
}

type cancellationDuringRead struct {
	cancel context.CancelFunc
	reads  int
}

func (b *cancellationDuringRead) Read(p []byte) (int, error) {
	b.reads++
	b.cancel()
	return copy(p, "x"), nil
}
func (b *cancellationDuringRead) Close() error { return nil }

func TestRejectedBodyStopsOnCancellationBetweenReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &cancellationDuringRead{cancel: cancel}
	r := httptest.NewRequest("POST", "http://127.0.0.1/", nil).WithContext(ctx)
	r.Body, r.ContentLength = b, 4
	w := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
	disposeRejectedBody(w, r)
	if b.reads != 1 || w.Header().Get("Connection") != "close" || len(w.deadlines) != 2 || w.deadlines[1].IsZero() {
		t.Fatal("drain ignored mid-read cancellation")
	}
}

type informationalRejectionWriter struct {
	*rejectionWriter
	statuses []int
}

func (w *informationalRejectionWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
	if status >= 200 {
		w.rejectionWriter.WriteHeader(status)
	}
}

func TestContentResponseWriterInformationalStatusesDoNotCommit(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1/", strings.NewReader("body"))
	tracked := trackRequestBody(r)
	underlying := &informationalRejectionWriter{rejectionWriter: &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}}
	w := &contentResponseWriter{ResponseWriter: underlying, request: r}
	w.WriteHeader(http.StatusEarlyHints)
	if tracked.consumed != 0 || w.wroteHeader {
		t.Fatal("informational status consumed or finalized the request")
	}
	w.WriteHeader(http.StatusNotFound)
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte("missing")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(underlying.statuses, []int{103, 404}) || tracked.consumed != 4 || len(underlying.deadlines) != 2 || underlying.Body.String() != "missing" {
		t.Fatal("final status was not disposed/committed exactly once")
	}
}

func TestGatewayBodyTrackingKeepsServerRequestBodyIntact(t *testing.T) {
	c := fixture(t)
	for _, path := range []string{"/index.html", "/api/missing"} {
		method := "POST"
		if path == "/index.html" {
			method = "GET"
		}
		r := rejectionRequest(t, c, method, path, "body")
		original := r.Body
		c.s.ServeHTTP(&rejectionWriter{ResponseRecorder: httptest.NewRecorder()}, r)
		if r.Body != original {
			t.Fatal("handler replaced the HTTP server's original request body")
		}
	}
	for _, original := range []io.ReadCloser{nil, http.NoBody} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/", nil)
		r.Body = original
		if tracked := trackRequestBody(r); tracked != nil || r.Body != original {
			t.Fatal("tracking replaced an absent body")
		}
	}
}

func TestContentResponseWriterImplicitWriteDoesNotDrainLaterError(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1/", strings.NewReader("body"))
	tracked := trackRequestBody(r)
	underlying := &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}
	w := &contentResponseWriter{ResponseWriter: underlying, request: r}
	if _, err := w.Write([]byte("success")); err != nil {
		t.Fatal(err)
	}
	w.WriteHeader(http.StatusBadRequest)
	w.WriteHeader(http.StatusInternalServerError)
	if underlying.Code != 200 || underlying.Body.String() != "success" || tracked.consumed != 0 || len(underlying.deadlines) != 0 {
		t.Fatal("late error changed an already committed successful response")
	}
}

func TestContentResponseWriterSwitchingProtocolsIsFinal(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1/", strings.NewReader("body"))
	tracked := trackRequestBody(r)
	underlying := &informationalRejectionWriter{rejectionWriter: &rejectionWriter{ResponseRecorder: httptest.NewRecorder()}}
	w := &contentResponseWriter{ResponseWriter: underlying, request: r}
	w.WriteHeader(http.StatusSwitchingProtocols)
	w.WriteHeader(http.StatusBadRequest)
	if !w.wroteHeader || !reflect.DeepEqual(underlying.statuses, []int{101}) || tracked.consumed != 0 || len(underlying.deadlines) != 0 {
		t.Fatal("protocol switching was treated as a provisional response")
	}
}
