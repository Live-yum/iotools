package webhost

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type rejectionBody struct {
	reader io.Reader
	reads  int
}

func (b *rejectionBody) Read(p []byte) (int, error) {
	b.reads++
	return b.reader.Read(p)
}
func (b *rejectionBody) Close() error { return nil }

type rejectionWriter struct {
	*httptest.ResponseRecorder
	deadlines   []time.Time
	unsupported bool
}

func (w *rejectionWriter) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	if w.unsupported {
		return http.ErrNotSupported
	}
	return nil
}

func TestRejectionBodyConsumptionIsBoundedAndNeverParses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		length      int64
		expect      string
		chunked     bool
		unsupported bool
		wantRead    bool
	}{
		{name: "small", length: 6, wantRead: true},
		{name: "empty", length: 0},
		{name: "unknown", length: -1},
		{name: "oversized", length: maxRejectedBodyDrain + 1},
		{name: "continue", length: 6, expect: "100-continue"},
		{name: "chunked", length: 6, chunked: true},
		{name: "unsupported-writer", length: 6, unsupported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &rejectionBody{reader: strings.NewReader("secret")}
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/platform", nil)
			request.Body = body
			request.ContentLength = tc.length
			request.Header.Set("Expect", tc.expect)
			if tc.chunked {
				request.TransferEncoding = []string{"chunked"}
			}
			writer := &rejectionWriter{ResponseRecorder: httptest.NewRecorder(), unsupported: tc.unsupported}
			rejectRequest(writer, request, "denied")
			if writer.Code != http.StatusForbidden || !strings.Contains(writer.Body.String(), `"ok":false`) {
				t.Fatal("rejection no longer returns explicit 403")
			}
			if (body.reads > 0) != tc.wantRead {
				t.Fatalf("unexpected body consumption: %d", body.reads)
			}
			if tc.wantRead && (len(writer.deadlines) != 2 || writer.deadlines[0].IsZero() || !writer.deadlines[1].IsZero()) {
				t.Fatal("bounded drain must install and then clear its deadline")
			}
		})
	}
}

func TestRejectionFragmentedConnectionCloseReturnsComplete403(t *testing.T) {
	c := fixture(t)
	c.ok("/api/platform", map[string]any{"method": "settings.save", "args": map[string]any{"theme": "dark"}})
	before := c.ok("/api/platform", map[string]any{"method": "settings.get"})["data"]
	origin, err := url.Parse(c.s.URL())
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*http.Request){
		"origin":     func(r *http.Request) { r.Header.Set("Origin", "https://evil.invalid") },
		"host":       func(r *http.Request) { r.Host = "evil.invalid" },
		"csrf":       func(r *http.Request) { r.Header.Set("X-Iotools-CSRF", "wrong") },
		"cross-site": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 5; i++ {
				payload := `{"method":"settings.save","args":{"theme":"light"}}`
				request, err := http.NewRequest(http.MethodPost, c.s.URL()+"/api/platform", strings.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				request.Close = true
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", c.s.URL())
				request.Header.Set("X-Iotools-CSRF", c.csrf)
				for _, cookie := range c.http.Jar.Cookies(origin) {
					request.AddCookie(cookie)
				}
				change(request)
				var wire bytes.Buffer
				if err = request.Write(&wire); err != nil {
					t.Fatal(err)
				}
				conn, err := net.DialTimeout("tcp4", origin.Host, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				split := bytes.Index(wire.Bytes(), []byte("\r\n\r\n")) + 4 + len(payload)/2
				_, err = conn.Write(wire.Bytes()[:split])
				if err == nil {
					time.Sleep(30 * time.Millisecond) // Deliberately fragmented client body.
					_, err = conn.Write(wire.Bytes()[split:])
				}
				if err != nil {
					conn.Close()
					t.Fatal(err)
				}
				response, err := http.ReadResponse(bufio.NewReader(conn), request)
				if err != nil {
					conn.Close()
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				conn.Close()
				if readErr != nil || response.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte(`"ok":false`)) {
					t.Fatalf("incomplete denial: status=%d err=%v", response.StatusCode, readErr)
				}
				if after := c.ok("/api/platform", map[string]any{"method": "settings.get"})["data"]; !reflect.DeepEqual(before, after) {
					t.Fatal("denied request changed settings")
				}
			}
		})
	}
}

func TestRejectionMissingSmallBodyCannotHoldConnection(t *testing.T) {
	c := fixture(t)
	address := strings.TrimPrefix(c.s.URL(), "http://")
	conn, err := net.DialTimeout("tcp4", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	_, err = io.WriteString(conn, "POST /api/platform HTTP/1.1\r\nHost: evil.invalid\r\nContent-Length: 20\r\nConnection: close\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	_, err = http.ReadResponse(bufio.NewReader(conn), nil)
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() || time.Since(start) > time.Second {
		t.Fatal("rejected missing body exceeded its bounded drain deadline", err)
	}
}
