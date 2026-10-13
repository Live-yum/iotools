package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
)

func historyContextFixture(t *testing.T) (*config.Collection, string, *atomic.Int32) {
	t.Helper()
	hits := new(atomic.Int32)
	body := base64.StdEncoding.EncodeToString([]byte(`{"message":"工业响应 😀"}`))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		data, err := io.ReadAll(r.Body)
		if err != nil || r.Method != "POST" || string(data) != "工业请求 😀" {
			t.Errorf("unexpected request: %s %q %v", r.Method, data, err)
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	c, err := ParseCollection([]byte(fmt.Sprintf(`crypto:
  base64: {algorithm: base64}
requests:
  write:
    method: POST
    url: %s
    body: '工业请求 😀'
    response_transform: [{type: decode, crypto: base64, scope: body, parse: json}]
`, server.URL)))
	if err != nil {
		t.Fatal(err)
	}
	c.SourcePath = filepath.Join(t.TempDir(), "collection.yaml")
	c.Requests[0].Timeout = "1s"
	return c, filepath.Join(t.TempDir(), "history.sqlite"), hits
}

func TestHTTPWorkflowHistoryOutlivesCompletedRecipeDeadline(t *testing.T) {
	c, path, hits := historyContextFixture(t)
	var recipeCtx context.Context
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{
		HistoryPath: path,
		AuthorizeRequestWrite: func(ctx context.Context, _ config.Request) (bool, error) {
			recipeCtx = ctx
			return true, nil
		},
	})
	var events []string
	err := RunCollection(ctx, c, c.Requests[0], "", true, func(event Event) {
		events = append(events, event.Kind)
		if event.Kind == "transformed" {
			// The HTTP body and transform have completed. Deterministically let
			// the recipe deadline expire before attempting local persistence.
			<-recipeCtx.Done()
		}
	})
	if err != nil {
		t.Fatalf("completed response could not be saved: %v", err)
	}
	if !errors.Is(recipeCtx.Err(), context.DeadlineExceeded) || hits.Load() != 1 {
		t.Fatalf("deadline/replay mismatch: %v, HTTP hits=%d", recipeCtx.Err(), hits.Load())
	}
	if strings.Join(events, ",") != "response,transformed" {
		t.Fatalf("unexpected event sequence: %v", events)
	}
	h, err := OpenHTTPHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	entry, err := h.Latest(context.Background(), c.SourcePath, "", "write")
	if err != nil || entry == nil {
		t.Fatalf("missing history response: %+v %v", entry, err)
	}
	wantBody := base64.StdEncoding.EncodeToString([]byte(`{"message":"工业响应 😀"}`))
	if entry.Status != 200 || entry.Method != "POST" || string(entry.Body) != wantBody || string(entry.Transformed) != `{"message":"工业响应 😀"}` {
		t.Fatalf("history bytes changed: %+v", entry)
	}
	rows, err := QueryHTTPHistory(context.Background(), path, "SELECT id FROM http_history")
	if err != nil || len(rows) != 1 {
		t.Fatalf("history must contain exactly one response: %v %v", rows, err)
	}
}

func TestHTTPWorkflowHistoryPreservesCallerCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			c, path, hits := historyContextFixture(t)
			c.Requests[0].Timeout = "30s"
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				want = context.DeadlineExceeded
			}
			defer cancel()
			ctx = WithHTTPWorkflowOptions(ctx, HTTPWorkflowOptions{HistoryPath: path})
			err := RunCollection(ctx, c, c.Requests[0], "", true, func(event Event) {
				if event.Kind == "transformed" {
					if mode == "cancel" {
						cancel()
					}
					<-ctx.Done()
				}
			})
			if !errors.Is(err, want) || !strings.Contains(err.Error(), "history save failed") || hits.Load() != 1 {
				t.Fatalf("caller cancellation lost or HTTP replayed: %v, hits=%d", err, hits.Load())
			}
			rows, err := QueryHTTPHistory(context.Background(), path, "SELECT id FROM http_history")
			if err != nil || len(rows) != 0 {
				t.Fatalf("cancelled caller persisted response: %v %v", rows, err)
			}
		})
	}
}

func TestHTTPWorkflowHistoryPreservesEnclosingRecipeDeadline(t *testing.T) {
	c, path, hits := historyContextFixture(t)
	c.Requests[0].Timeout = "30s"
	h, err := OpenHTTPHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Reproduce the state when response() recursively enters w.run: w.ctx is
	// the enclosing recipe's deadline, not the original collection context.
	w := &httpWorkflow{
		ctx: parent, collection: c, collectionKey: c.SourcePath, rootDir: filepath.Dir(c.SourcePath),
		vars: map[string]string{}, varsCache: map[string]any{}, varsActive: map[string]bool{},
		active: map[string]bool{}, executed: map[string]bool{}, responses: map[string]*HTTPHistoryEntry{},
		allowWrites: true, options: HTTPWorkflowOptions{HistoryPath: path, AllowChainWrites: true}, history: h,
	}
	err = w.run(c.Requests[0], true, func(event Event) {
		if event.Kind == "chain" && event.Data.(map[string]any)["event"].(Event).Kind == "transformed" {
			<-parent.Done()
		}
	})
	if !errors.Is(err, context.DeadlineExceeded) || hits.Load() != 1 {
		t.Fatalf("enclosing deadline lost or HTTP replayed: %v, hits=%d", err, hits.Load())
	}
	rows, err := QueryHTTPHistory(context.Background(), path, "SELECT id FROM http_history")
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired enclosing recipe persisted response: %v %v", rows, err)
	}
}

func TestHTTPWorkflowHistoryFailureDoesNotReplayRequest(t *testing.T) {
	c, path, hits := historyContextFixture(t)
	ctx := WithHTTPWorkflowOptions(context.Background(), HTTPWorkflowOptions{HistoryPath: path})
	err := RunCollection(ctx, c, c.Requests[0], "", true, func(event Event) {
		if event.Kind == "transformed" {
			h, err := OpenHTTPHistory(path)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			if _, err := h.db.Exec("DROP TABLE http_history"); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err == nil || !strings.Contains(err.Error(), "history save failed") || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || hits.Load() != 1 {
		t.Fatalf("save failure hidden or request replayed: %v, hits=%d", err, hits.Load())
	}
}

func TestHTTPWorkflowHistoryPendingSaveIsCancellableAndBounded(t *testing.T) {
	for _, mode := range []string{"cancel", "save_budget"} {
		t.Run(mode, func(t *testing.T) {
			c, path, hits := historyContextFixture(t)
			c.Requests[0].Timeout = "30s"
			h, err := OpenHTTPHistory(path)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			// Reserve the only connection so history Add must wait inside BeginTx.
			conn, err := h.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &httpWorkflow{
				ctx: ctx, collection: c, collectionKey: c.SourcePath, rootDir: filepath.Dir(c.SourcePath),
				vars: map[string]string{}, varsCache: map[string]any{}, varsActive: map[string]bool{},
				active: map[string]bool{}, executed: map[string]bool{}, responses: map[string]*HTTPHistoryEntry{},
				allowWrites: true, options: HTTPWorkflowOptions{HistoryPath: path}, history: h,
			}
			result := make(chan error, 1)
			go func() { result <- w.run(c.Requests[0], false, nil) }()
			watchdog := time.NewTimer(10 * time.Second)
			defer watchdog.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for h.db.Stats().WaitCount == 0 {
				select {
				case <-poll.C:
				case err := <-result:
					t.Fatalf("workflow ended before waiting for history: %v", err)
				case <-watchdog.C:
					cancel()
					conn.Close()
					<-result
					t.Fatal("history Add did not begin waiting")
				}
			}
			want := context.DeadlineExceeded
			if mode == "cancel" {
				cancel()
				want = context.Canceled
			}
			select {
			case err = <-result:
			case <-watchdog.C:
				cancel()
				conn.Close()
				<-result
				t.Fatal("pending history save exceeded its independent budget")
			}
			if !errors.Is(err, want) || hits.Load() != 1 {
				t.Fatalf("pending save ignored its context or replayed HTTP: %v, hits=%d", err, hits.Load())
			}
			if mode == "save_budget" && ctx.Err() != nil {
				t.Fatal("save deadline cancelled the caller")
			}
			if err = conn.Close(); err != nil {
				t.Fatal(err)
			}
			rows, err := QueryHTTPHistory(context.Background(), path, "SELECT id FROM http_history")
			if err != nil || len(rows) != 0 {
				t.Fatalf("cancelled pending save wrote history: %v %v", rows, err)
			}
		})
	}
}
