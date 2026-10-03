package webhost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func uploadRejectionTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var contents []byte
		if !entry.IsDir() {
			contents, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		tree[rel] = entry.Type().String() + "\x00" + string(contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestUploadEarlyRejectionFragmentedConnectionCloseReturnsCompleteError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     string
		status    int
		message   string
		blockRoot bool
	}{
		{name: "invalid-limit", limit: "invalid", status: http.StatusBadRequest, message: "文件大小上限无效"},
		{name: "oversized", limit: "1", status: http.StatusRequestEntityTooLarge, message: "上传文件超过上限"},
		{name: "mkdir-failure", limit: "1024", status: http.StatusInternalServerError, message: "无法创建导入目录", blockRoot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t)
			if tc.blockRoot {
				// A regular file blocks MkdirTemp on every platform without
				// relying on permission bits or changing the server's root.
				if err := os.Remove(c.s.root); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(c.s.root, []byte("unchanged root blocker"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				directory := filepath.Join(c.s.root, "existing")
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "keep.bin"), []byte{0, 1, 255}, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := uploadRejectionTree(t, c.s.root)
			origin, err := url.Parse(c.s.URL())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				payload := bytes.Repeat([]byte{0, 1, 127, 255}, 256)
				request, err := http.NewRequest(http.MethodPost, c.s.URL()+"/api/files/upload?name=oversize.bin&limit="+tc.limit, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				request.Close = true
				request.Header.Set("Content-Type", "application/octet-stream")
				request.Header.Set("Origin", c.s.URL())
				request.Header.Set("X-Iotools-CSRF", c.csrf)
				for _, cookie := range c.http.Jar.Cookies(request.URL) {
					request.AddCookie(cookie)
				}
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
				if readErr != nil || response.StatusCode != tc.status {
					t.Fatalf("incomplete rejection: status=%d err=%v", response.StatusCode, readErr)
				}
				var envelope struct {
					OK    *bool  `json:"ok"`
					Error string `json:"error"`
				}
				if err := json.Unmarshal(body, &envelope); err != nil || envelope.OK == nil || *envelope.OK || envelope.Error != tc.message {
					t.Fatalf("invalid rejection response: body=%q err=%v", body, err)
				}
				if after := uploadRejectionTree(t, c.s.root); !reflect.DeepEqual(before, after) {
					t.Fatal("rejected upload changed the file tree")
				}
			}
		})
	}
}
