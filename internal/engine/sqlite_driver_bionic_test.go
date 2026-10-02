//go:build android || sqlite_android_test

package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bionicTestDSN(path, query string) string {
	return (&url.URL{Scheme: "file", Path: sqliteURIPath(path)}).String() + "?" + query
}
func bionicTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err = db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestSQLiteBionicDSNPreservesURIAndPragmas(t *testing.T) {
	base := "file:///tmp/history%20%E4%B8%AD%E6%96%87%25%3F%23.sqlite"
	out, err := sqliteBionicDSN(base + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	prefix, query, _ := strings.Cut(out, "?")
	if prefix != base {
		t.Fatalf("URI filename changed: %q", prefix)
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"mode": "ro", "_query_only": "1", "_busy_timeout": "1000", "_foreign_keys": "1", "_synchronous": "FULL"} {
		if q.Get(key) != want {
			t.Fatalf("%s=%q, want %q", key, q.Get(key), want)
		}
	}
	if q.Has("_pragma") {
		t.Fatal("modernc pragmas leaked to bundled driver")
	}
	for _, query := range []string{
		"_pragma=unknown(1)", "_pragma=query_only(2)", "_pragma=busy_timeout(-1)", "_pragma=busy_timeout(2147483648)",
		"_pragma=query_only(1);ATTACH", "_pragma=query_only(1)&_pragma=query_only(0)", "_pragma=foreign_keys(1)&_fk=0",
		"_query_only=0", "_pragma=query_only(1)&mode=ro&mode=rw", "_pragma=query_only%ZZ", "_pragma=",
	} {
		if _, err := sqliteBionicDSN(base + "?" + query); err == nil {
			t.Errorf("ambiguous or unsupported DSN accepted: %s", query)
		}
	}
}
func TestSQLiteBionicReadOnlyForeignKeysAndExactFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history 中文 %?#.sqlite")
	db := bionicTestDB(t, bionicTestDSN(path, "_pragma=foreign_keys(1)&_pragma=busy_timeout(123)"))
	for key, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 123, "synchronous": 2} {
		var got int
		if err := db.QueryRow("PRAGMA " + key).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want%d: %v", key, got, want, err)
		}
	}
	if _, err := db.Exec("CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); INSERT INTO parent VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO child VALUES(8)"); err == nil {
		t.Fatal("foreign key enforcement lost")
	}
	if _, err := db.Exec("INSERT INTO child VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("URI did not address exact Unicode/punctuation filename", err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 || files[0].Name() != filepath.Base(path) {
		t.Fatalf("unexpected filenames %v: %v", files, err)
	}
	ro := bionicTestDB(t, bionicTestDSN(path, "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"))
	var flag int
	if err := ro.QueryRow("PRAGMA query_only").Scan(&flag); err != nil || flag != 1 {
		t.Fatalf("query_only=%d: %v", flag, err)
	}
	if _, err := ro.Exec("INSERT INTO parent VALUES(9)"); err == nil {
		t.Fatal("read-only connection wrote")
	}
	// Even disabling the independent query-only flag cannot turn a mode=ro open
	// into a writeable connection.
	if _, err := ro.Exec("PRAGMA query_only=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Exec("INSERT INTO parent VALUES(9)"); err == nil {
		t.Fatal("mode=ro was not a real SQLite read-only open")
	}
	queryOnly := bionicTestDB(t, bionicTestDSN(path, "mode=rw&_pragma=query_only(1)"))
	if _, err := queryOnly.Exec("INSERT INTO parent VALUES(10)"); err == nil {
		t.Fatal("query_only was ignored independently of mode=ro")
	}
	for _, mode := range []string{"ro", "rw"} {
		missing := filepath.Join(t.TempDir(), "missing.sqlite")
		missingDB, err := sql.Open("sqlite", bionicTestDSN(missing, "mode="+mode))
		if err != nil {
			t.Fatal(err)
		}
		err = missingDB.Ping()
		missingDB.Close()
		if err == nil {
			t.Fatalf("mode=%s created a missing database", mode)
		}
		if _, err = os.Stat(missing); !os.IsNotExist(err) {
			t.Fatalf("mode=%s created a file: %v", mode, err)
		}
	}
}
func TestSQLiteBionicTransactionsBusyTimeoutAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	db := bionicTestDB(t, bionicTestDSN(path, "_pragma=busy_timeout(100)"))
	if _, err := db.Exec("CREATE TABLE values_table(v INTEGER)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO values_table VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM values_table").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback lost: %d %v", count, err)
	}
	locked := bionicTestDB(t, bionicTestDSN(path, "mode=rw&_pragma=busy_timeout(100)"))
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, writeErr := locked.Exec("INSERT INTO values_table VALUES(2)")
	elapsed := time.Since(started)
	_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
	conn.Close()
	if rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if writeErr == nil || elapsed < 50*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("busy timeout/lock not retained: duration=%s err=%v", elapsed, writeErr)
	}
	if _, err = locked.Exec("INSERT INTO values_table VALUES(3)"); err != nil {
		t.Fatal("lock not released", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var sum int64
	err = db.QueryRowContext(ctx, "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n").Scan(&sum)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query cancellation not forwarded: %v", err)
	}
}
func TestSQLiteBionicSnapshotRetainsBlobNullIntegerAndTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	db := bionicTestDB(t, bionicTestDSN(path, "_pragma=busy_timeout(1000)"))
	if _, err := db.Exec("CREATE TABLE exact(i INTEGER,b BLOB,n TEXT); INSERT INTO exact VALUES(9223372036854775807,x'ff00fe',NULL)"); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	data, err := historySnapshot(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		t.Fatal("snapshot isn't a SQLite database")
	}
	backup := filepath.Join(t.TempDir(), "original.sqlite")
	if err = os.WriteFile(backup, data, 0600); err != nil {
		t.Fatal(err)
	}
	copy := bionicTestDB(t, bionicTestDSN(backup, "mode=ro&_pragma=query_only(1)"))
	var integer int64
	var blob []byte
	var nullable any
	if err = copy.QueryRow("SELECT i,b,n FROM exact").Scan(&integer, &blob, &nullable); err != nil {
		t.Fatal(err)
	}
	if integer != 9223372036854775807 || !bytes.Equal(blob, []byte{255, 0, 254}) || nullable != nil {
		t.Fatalf("snapshot values changed: %d %v %v", integer, blob, nullable)
	}
}
