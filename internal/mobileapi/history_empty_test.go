package mobileapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingHistoryNeverCreatesDatabase(t *testing.T) {
	s := testSession(t)
	status, err := s.history(command{Op: "history.status"})
	if err != nil || status.(map[string]any)["exists"] != false {
		t.Fatalf("status %v %v", status, err)
	}
	for _, op := range []string{"history.list", "history.collections"} {
		rows, err := s.history(command{Op: op})
		if err != nil || len(rows.([]any)) != 0 {
			t.Fatalf("%s %v %v", op, rows, err)
		}
	}
	for _, op := range []string{"history.query", "history.get", "history.preview", "history.execute", "history.delete", "history.collection.preview"} {
		_, err := s.history(command{Op: op, SQL: "SELECT 1"})
		if err == nil || !strings.Contains(err.Error(), "暂无历史数据库") || strings.Contains(err.Error(), s.root) {
			t.Fatalf("%s error %v", op, err)
		}
	}
	if _, err = os.Stat(filepath.Join(s.root, "history.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("history unexpectedly created: %v", err)
	}
}
