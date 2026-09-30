package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historySQLFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "history.sqlite")
	h, e := OpenHTTPHistory(p)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	for _, collection := range []string{"a.yml", "b.yml"} {
		if e = h.Add(context.Background(), HTTPHistoryEntry{Collection: collection, Recipe: "one", Time: time.Now(), Method: "GET", Status: 200, Body: []byte("original")}); e != nil {
			t.Fatal(e)
		}
	}
	return p
}
func TestHistoryScriptBackupAtomicAndStale(t *testing.T) {
	p := historySQLFixture(t)
	ctx := context.Background()
	script := "BEGIN; UPDATE http_history SET status=201 WHERE collection='a.yml'; SELECT status FROM http_history ORDER BY id; COMMIT;"
	preview, e := PreviewHTTPHistoryScript(ctx, p, script)
	if e != nil {
		t.Fatal(e)
	}
	backup := p + ".backup"
	result, e := ExecuteHTTPHistoryScript(ctx, p, script, preview.Token, backup, true)
	if e != nil {
		t.Fatal(e)
	}
	if result.Backup != backup || len(result.Results) != 2 {
		t.Fatal(result)
	}
	rows, e := QueryHTTPHistory(ctx, backup, "SELECT status FROM http_history ORDER BY id")
	if e != nil || rows[0]["status"] != int64(200) {
		t.Fatal(rows, e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, script, preview.Token, p+".stale", true); e == nil {
		t.Fatal("stale preview accepted")
	}
	if _, e = os.Stat(p + ".stale"); !os.IsNotExist(e) {
		t.Fatal("stale preview created backup")
	}
	fail := "UPDATE http_history SET status=500; INSERT INTO missing_table VALUES(1)"
	pr, e := PreviewHTTPHistoryScript(ctx, p, fail)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, fail, pr.Token, p+".rollback", true); e == nil {
		t.Fatal("invalid script committed")
	}
	rows, e = QueryHTTPHistory(ctx, p, "SELECT status FROM http_history ORDER BY id")
	if e != nil || rows[0]["status"] != int64(201) || rows[1]["status"] != int64(200) {
		t.Fatal(rows, e)
	}
}
func TestHistorySQLLexingAndNoFileEscape(t *testing.T) {
	good := []string{"SELECT 'a;b -- x'; SELECT 2", "-- comment\nSELECT ';'", ".tables", ".schema"}
	for _, s := range good {
		if _, e := historySQLStatements(s); e != nil {
			t.Fatal(s, e)
		}
	}
	bad := []string{"ATTACH 'secret' AS x", "SELECT load_extension('x')", "PRAGMA writable_schema=1", "VACUUM INTO 'x'", "CREATE TRIGGER x AFTER INSERT ON http_history BEGIN SELECT 1; END", "CREATE VIRTUAL TABLE x USING fts5(v)", "ROLLBACK", "SELECT 'bad", "SELECT 1 /*bad"}
	for _, s := range bad {
		if _, e := historySQLStatements(s); e == nil {
			t.Fatal("unsafe accepted", s)
		}
	}
}
func TestHistoryCollectionMigrationAndBackup(t *testing.T) {
	p := historySQLFixture(t)
	ctx := context.Background()
	collections, e := ListHTTPHistoryCollections(ctx, p)
	if e != nil || len(collections) != 2 {
		t.Fatal(collections, e)
	}
	script, e := HTTPHistoryCollectionScript("migrate", "a.yml", "b.yml")
	if e != nil {
		t.Fatal(e)
	}
	pr, e := PreviewHTTPHistoryScript(ctx, p, script)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, script, pr.Token, p+".merge", true); e != nil {
		t.Fatal(e)
	}
	collections, e = ListHTTPHistoryCollections(ctx, p)
	if e != nil || len(collections) != 1 || collections[0]["requests"] != int64(2) {
		t.Fatal(collections, e)
	}
	script, e = HTTPHistoryCollectionScript("delete", "b.yml", "")
	if e != nil {
		t.Fatal(e)
	}
	pr, e = PreviewHTTPHistoryScript(ctx, p, script)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, script, pr.Token, p+".delete", true); e != nil {
		t.Fatal(e)
	}
	rows, e := QueryHTTPHistory(ctx, p+".delete", "SELECT count(*) AS n FROM http_history")
	if e != nil || rows[0]["n"] != int64(2) {
		t.Fatal(rows, e)
	}
	if _, e = HTTPHistoryCollectionScript("migrate", "a", "a"); e == nil {
		t.Fatal("self migration")
	}
	escaped, e := HTTPHistoryCollectionScript("delete", "x'; ATTACH other", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = historySQLStatements(escaped); e != nil {
		t.Fatal("escaped literal rejected", e)
	}
	if !strings.Contains(escaped, "x'';") {
		t.Fatal("unescaped source")
	}
}
func TestHistoryScriptWALAndReadOnly(t *testing.T) {
	p := historySQLFixture(t)
	ctx := context.Background()
	db, e := historyManagementDB(p, true)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("PRAGMA journal_mode=WAL"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO http_history SELECT NULL,collection,profile,recipe,method,created_at,status,headers,body,transformed FROM http_history LIMIT 1"); e != nil {
		t.Fatal(e)
	}
	script := "UPDATE http_history SET status=202"
	pr, e := PreviewHTTPHistoryScript(ctx, p, script)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, script, pr.Token, p+".wal-backup", true); e != nil {
		t.Fatal(e)
	}
	rows, e := QueryHTTPHistory(ctx, p+".wal-backup", "SELECT count(*) AS n FROM http_history WHERE status=200")
	if e != nil || rows[0]["n"] != int64(3) {
		t.Fatal("WAL backup not complete", rows, e)
	}
	if _, e = QueryHTTPHistoryScript(ctx, p, "UPDATE http_history SET status=500"); e == nil {
		t.Fatal("read-only mutation")
	}
	result, e := QueryHTTPHistoryScript(ctx, p, "SELECT 1; SELECT 'semi;colon'")
	if e != nil || len(result) != 2 {
		t.Fatal(result, e)
	}
	result, e = QueryHTTPHistoryScript(ctx, p, ".schema")
	if e != nil || len(result[0].Rows) == 0 {
		t.Fatal(result, e)
	}
}
func TestHistoryScriptBackupNeverOverwritesAndLimitsRollback(t *testing.T) {
	p := historySQLFixture(t)
	ctx := context.Background()
	script := "DELETE FROM http_history"
	pr, e := PreviewHTTPHistoryScript(ctx, p, script)
	if e != nil {
		t.Fatal(e)
	}
	backup := p + ".existing"
	if e = os.WriteFile(backup, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, script, pr.Token, backup, true); e == nil {
		t.Fatal("backup overwritten")
	}
	b, _ := os.ReadFile(backup)
	if string(b) != "keep" {
		t.Fatal("backup changed")
	}
	script = "UPDATE http_history SET status=500; WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1001) SELECT x FROM n"
	pr, e = PreviewHTTPHistoryScript(ctx, p, script)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ExecuteHTTPHistoryScript(ctx, p, script, pr.Token, p+".limit", true); e == nil {
		t.Fatal("row limit ignored")
	}
	rows, e := QueryHTTPHistory(ctx, p, "SELECT status FROM http_history")
	if e != nil || rows[0]["status"] != int64(200) {
		t.Fatal("limit failed rollback", rows, e)
	}
}
