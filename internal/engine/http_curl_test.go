package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCurlGenerationEscapingTransformsAndNoNetwork(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); fmt.Fprint(w, `"session"`) }))
	defer s.Close()
	r := config.Request{ID: "x", Protocol: "http", Action: "POST", Endpoint: "{{host}}/data", Params: map[string]any{"body": "a'b", "headers": map[string]any{"X-Test": "$(touch /tmp/NOPE)"}, "query": map[string]any{"x": "a b"}}}
	c := &config.Collection{Version: 1, Profiles: map[string]map[string]string{"local": {"host": s.URL}}, Requests: []config.Request{r}}
	output, e := GenerateCurl(context.Background(), c, r, "local", false, false)
	if e != nil {
		t.Fatal(e)
	}
	if hits.Load() != 0 || !strings.Contains(output, `'a'"'"'b'`) || !strings.Contains(output, "?x=a+b") || !strings.Contains(output, "--data-binary") {
		t.Fatal(output)
	}
	login := config.Request{ID: "login", Protocol: "http", Action: "GET", Endpoint: s.URL}
	c.Requests = append(c.Requests, login)
	r.Params["body"] = "{{response('login',trigger='always')}}"
	if _, e := GenerateCurl(context.Background(), c, r, "local", false, false); e == nil || hits.Load() != 0 {
		t.Fatal("curl silently triggered dependency")
	}
	if _, e := GenerateCurl(context.Background(), c, r, "local", false, true); e != nil || hits.Load() != 1 {
		t.Fatalf("explicit trigger: %v hits%d", e, hits.Load())
	}
	if _, e := shellQuote("a\x00b"); e == nil {
		t.Fatal("NUL command argument accepted")
	}
}
