package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type HTTPHistoryEntry struct {
	ID          int64       `json:"id"`
	Collection  string      `json:"collection"`
	Profile     string      `json:"profile"`
	Recipe      string      `json:"recipe"`
	Method      string      `json:"method"`
	Time        time.Time   `json:"time"`
	Status      int         `json:"status"`
	Headers     http.Header `json:"headers"`
	Body        []byte      `json:"body"`
	Transformed []byte      `json:"transformed,omitempty"`
}
type HTTPHistory struct {
	db   *sql.DB
	path string
}

func OpenHTTPHistory(path string) (*HTTPHistory, error) {
	if path == "" {
		return nil, fmt.Errorf("history path required")
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	info, e := os.Lstat(abs)
	if e == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("history must be a regular file, not a symlink")
		}
	} else if os.IsNotExist(e) {
		f, e := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
	} else {
		return nil, e
	}
	// Do not persist secrets in a world/group-readable file on POSIX.
	if info, e := os.Stat(abs); e == nil && info.Mode().Perm()&0077 != 0 {
		if e = os.Chmod(abs, 0600); e != nil {
			return nil, fmt.Errorf("history permissions: %w", e)
		}
	}
	dsn := (&url.URL{Scheme: "file", Path: sqliteURIPath(abs)}).String() + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS http_history (id INTEGER PRIMARY KEY AUTOINCREMENT, collection TEXT NOT NULL, profile TEXT NOT NULL, recipe TEXT NOT NULL, method TEXT NOT NULL, created_at TEXT NOT NULL, status INTEGER NOT NULL, headers TEXT NOT NULL, body BLOB NOT NULL, transformed BLOB); CREATE INDEX IF NOT EXISTS http_history_latest ON http_history(collection,profile,recipe,id DESC)`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &HTTPHistory{db: db, path: abs}, nil
}
func (h *HTTPHistory) Close() error { return h.db.Close() }
func (h *HTTPHistory) Add(ctx context.Context, e HTTPHistoryEntry) error {
	if len(e.Body) > maxBody || len(e.Transformed) > maxBody {
		return fmt.Errorf("history response exceeds limit")
	}
	headers, err := json.Marshal(e.Headers)
	if err != nil {
		return err
	}
	body := e.Body
	if body == nil {
		body = []byte{}
	}
	_, err = h.db.ExecContext(ctx, `INSERT INTO http_history(collection,profile,recipe,method,created_at,status,headers,body,transformed) VALUES(?,?,?,?,?,?,?,?,?)`, e.Collection, e.Profile, e.Recipe, e.Method, e.Time.Format(time.RFC3339Nano), e.Status, string(headers), body, e.Transformed)
	return err
}
func (h *HTTPHistory) Latest(ctx context.Context, collection, profile, recipe string) (*HTTPHistoryEntry, error) {
	e := &HTTPHistoryEntry{}
	var created, headers string
	err := h.db.QueryRowContext(ctx, `SELECT id,collection,profile,recipe,method,created_at,status,headers,body,transformed FROM http_history WHERE collection=? AND profile=? AND recipe=? ORDER BY id DESC LIMIT 1`, collection, profile, recipe).Scan(&e.ID, &e.Collection, &e.Profile, &e.Recipe, &e.Method, &created, &e.Status, &headers, &e.Body, &e.Transformed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.Time, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(headers), &e.Headers); err != nil {
		return nil, err
	}
	return e, nil
}

// QueryHTTPHistory is a read-only SQLite console entry point. SQLite enforces
// query_only on a read-only connection; SQL mutation cannot be hidden in a CTE.
func QueryHTTPHistory(ctx context.Context, path, statement string) ([]map[string]any, error) {
	if len(statement) > 65536 {
		return nil, fmt.Errorf("SQL exceeds 64 KiB")
	}
	trim := strings.TrimSpace(statement)
	upper := strings.ToUpper(trim)
	if !(strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "SELECT\n") || strings.HasPrefix(upper, "WITH ") || strings.HasPrefix(upper, "EXPLAIN ")) {
		return nil, fmt.Errorf("history console permits SELECT, WITH or EXPLAIN queries only")
	}
	if strings.Contains(trim, ";") {
		return nil, fmt.Errorf("history console accepts one statement without semicolons")
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	info, e := os.Lstat(abs)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("history must be a regular file")
	}
	dsn := (&url.URL{Scheme: "file", Path: sqliteURIPath(abs)}).String() + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, e := db.QueryContext(ctx, statement)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	columns, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	total := 0
	for rows.Next() {
		if len(out) >= 1000 {
			return nil, fmt.Errorf("history query exceeds 1000 rows; add LIMIT")
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if e = rows.Scan(pointers...); e != nil {
			return nil, e
		}
		row := map[string]any{}
		for i, col := range columns {
			row[col] = values[i]
		}
		b, e := json.Marshal(row)
		if e != nil {
			return nil, e
		}
		total += len(b)
		if total > maxBody {
			return nil, fmt.Errorf("history query exceeds 4 MiB")
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func sqliteURIPath(path string) string {
	s := filepath.ToSlash(path)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}
