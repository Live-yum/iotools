package main

import (
	"context"
	"github.com/Live-yum/iotools/internal/engine"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryAdminCLIExplicitPreviewAndReadonly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "history.sqlite")
	h, e := engine.OpenHTTPHistory(p)
	if e != nil {
		t.Fatal(e)
	}
	e = h.Add(context.Background(), engine.HTTPHistoryEntry{Collection: "first", Recipe: "a", Method: "GET", Time: time.Now(), Status: 200})
	h.Close()
	if e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--history-db", p, "--history-collections"}, {"--history-db", p, "--history-script", ".tables"}, {"--history-db", p, "--history-collection-action", "migrate", "--history-source", "first", "--history-target", "second"}} {
		if e = run(args); e != nil {
			t.Fatal(e)
		}
	}
	script := "UPDATE http_history SET status=201"
	pr, e := engine.PreviewHTTPHistoryScript(context.Background(), p, script)
	if e != nil {
		t.Fatal(e)
	}
	base := []string{"--history-db", p, "--history-script", script, "--history-apply", pr.Token, "--history-backup", p + ".backup"}
	if e = run(base); e == nil {
		t.Fatal("missing write authorization")
	}
	if e = run(append(append([]string{}, base...), "--allow-writes", "--read-only")); e == nil {
		t.Fatal("readonly bypass")
	}
	if e = run(append(base, "--allow-writes")); e != nil {
		t.Fatal(e)
	}
}
