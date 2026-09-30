package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryManagementScopeAndAtomicDelete(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	h, e := OpenHTTPHistory(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, collection := range []string{"one", "two"} {
		if e = h.Add(ctx, HTTPHistoryEntry{Collection: collection, Recipe: "get", Time: time.Now(), Status: 200, Body: []byte("hello")}); e != nil {
			t.Fatal(e)
		}
	}
	h.Close()
	rows, e := ListHTTPHistory(ctx, path, "one", "")
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	id := rows[0]["id"].(int64)
	entry, e := GetHTTPHistory(ctx, path, "one", id)
	if e != nil || entry["body"] != "hello" {
		t.Fatal(entry, e)
	}
	if _, e = GetHTTPHistory(ctx, path, "two", id); e == nil {
		t.Fatal("cross-collection history exposed")
	}
	if _, e = DeleteHTTPHistory(ctx, path, "one", []int64{id}, false); e == nil {
		t.Fatal("delete without confirmation")
	}
	if _, e = DeleteHTTPHistory(ctx, path, "one", []int64{id, id + 1}, true); e == nil {
		t.Fatal("cross-collection partial deletion")
	}
	rows, _ = ListHTTPHistory(ctx, path, "one", "")
	if len(rows) != 1 {
		t.Fatal("failed transaction deleted row")
	}
	if n, e := DeleteHTTPHistory(ctx, path, "one", []int64{id}, true); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	rows, _ = ListHTTPHistory(ctx, path, "two", "")
	if len(rows) != 1 {
		t.Fatal("unrelated history changed")
	}
}
