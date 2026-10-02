package engine

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Run this explicit cross-driver check in three separate test processes using
// the same temporary directory: pure-Go create, sqlite_android_test migrate,
// pure-Go verify. No database format is rewritten simply to select a driver.
// Ordinary test invocations skip it rather than pretending to test both drivers.
func TestSQLiteDriverExistingDatabaseExchange(t *testing.T) {
	dir := os.Getenv("IOTOOLS_SQLITE_INTEROP_DIR")
	stage := os.Getenv("IOTOOLS_SQLITE_INTEROP_STAGE")
	if dir == "" || stage == "" {
		t.Skip("requires the explicit three-process driver interoperability run")
	}
	path := filepath.Join(dir, "历史 中文 %?#.sqlite")
	ctx := context.Background()
	created := time.Date(2026, 10, 1, 9, 40, 0, 123456789, time.UTC)
	switch stage {
	case "create":
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("fixture must be a new file: %v", err)
		}
		h, err := OpenHTTPHistory(path)
		if err != nil {
			t.Fatal(err)
		}
		err = h.Add(ctx, HTTPHistoryEntry{Collection: "before.yml", Profile: "local", Recipe: "driver-exchange", Method: "POST", Time: created, Status: 200, Headers: http.Header{"X-Text": []string{"中文 🙂"}}, Body: []byte{255, 0, 254}, Transformed: []byte("结果 🙂")})
		closeErr := h.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		db, err := historyManagementDB(path, true)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err = db.Exec("CREATE TABLE driver_values(i INTEGER,b BLOB,n TEXT); INSERT INTO driver_values VALUES(9223372036854775807,x'ff00fe',NULL)"); err != nil {
			t.Fatal(err)
		}
	case "migrate":
		row, err := GetHTTPHistory(ctx, path, "before.yml", 1)
		if err != nil {
			t.Fatal(err)
		}
		if row["raw_body_base64"] != base64.StdEncoding.EncodeToString([]byte{255, 0, 254}) || row["transformed"] != "结果 🙂" || row["created_at"] != created.Format(time.RFC3339Nano) {
			t.Fatalf("existing data changed: %#v", row)
		}
		script := "UPDATE http_history SET collection='after.yml',status=201 WHERE id=1; SELECT i,b,n FROM driver_values"
		preview, err := PreviewHTTPHistoryScript(ctx, path, script)
		if err != nil {
			t.Fatal(err)
		}
		result, err := ExecuteHTTPHistoryScript(ctx, path, script, preview.Token, path+".before", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Results) != 2 || len(result.Results[1].Rows) != 1 || result.Results[1].Rows[0][0] != int64(9223372036854775807) || result.Results[1].Rows[0][2] != nil {
			t.Fatalf("SQL result types changed: %#v", result)
		}
	case "verify":
		row, err := GetHTTPHistory(ctx, path, "after.yml", 1)
		if err != nil {
			t.Fatal(err)
		}
		if row["status"] != 201 || row["raw_body_base64"] != "/wD+" || row["transformed"] != "结果 🙂" || row["created_at"] != created.Format(time.RFC3339Nano) {
			t.Fatalf("migrated data changed: %#v", row)
		}
		original, err := GetHTTPHistory(ctx, path+".before", "before.yml", 1)
		if err != nil || original["status"] != 200 {
			t.Fatalf("pre-migration backup not readable: %#v %v", original, err)
		}
		rows, err := QueryHTTPHistory(ctx, path, "SELECT i,b,n FROM driver_values")
		if err != nil || len(rows) != 1 || rows[0]["i"] != int64(9223372036854775807) || rows[0]["n"] != nil {
			t.Fatalf("exact values changed: %#v %v", rows, err)
		}
	default:
		t.Fatalf("unsupported interoperability stage %q", stage)
	}
}
