package engine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func historyManagementDB(path string, writable bool) (*sql.DB, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	info, e := os.Lstat(abs)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("历史数据库必须是现有普通文件")
	}
	mode := "ro&_pragma=query_only(1)"
	if writable {
		mode = "rw"
	}
	dsn := (&url.URL{Scheme: "file", Path: sqliteURIPath(abs)}).String() + "?mode=" + mode + "&_pragma=busy_timeout(1000)"
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// ListHTTPHistory is scoped to the explicitly selected collection. Empty recipe
// includes every recipe in that collection, but never silently crosses files.
func ListHTTPHistory(ctx context.Context, path, collection, recipe string) ([]map[string]any, error) {
	db, e := historyManagementDB(path, false)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, e := db.QueryContext(ctx, "SELECT id,recipe,profile,method,created_at,status,length(body) FROM http_history WHERE collection=? AND (?='' OR recipe=?) ORDER BY id DESC LIMIT 1000", collection, recipe, recipe)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, size int64
		var recipe, profile, method, created string
		var status int
		if e = rows.Scan(&id, &recipe, &profile, &method, &created, &status, &size); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": id, "recipe": recipe, "profile": profile, "method": method, "created_at": created, "status": status, "body_bytes": size})
	}
	return out, rows.Err()
}
func GetHTTPHistory(ctx context.Context, path, collection string, id int64) (map[string]any, error) {
	if id <= 0 {
		return nil, fmt.Errorf("历史ID必须为正整数")
	}
	db, e := historyManagementDB(path, false)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var recipe, profile, method, created, headers string
	var status int
	var body, transformed []byte
	e = db.QueryRowContext(ctx, "SELECT recipe,profile,method,created_at,status,headers,body,transformed FROM http_history WHERE collection=? AND id=?", collection, id).Scan(&recipe, &profile, &method, &created, &status, &headers, &body, &transformed)
	if e != nil {
		return nil, e
	}
	if len(body) > maxBody || len(transformed) > maxBody {
		return nil, fmt.Errorf("历史正文超过4MiB")
	}
	text, derived := string(body), string(transformed)
	if !utf8.Valid(body) {
		text = "（二进制，请使用raw_body_base64）"
	}
	if !utf8.Valid(transformed) {
		derived = "（二进制，请使用raw_transformed_base64）"
	}
	return map[string]any{"raw_body_base64": base64.StdEncoding.EncodeToString(body), "raw_transformed_base64": base64.StdEncoding.EncodeToString(transformed), "id": id, "recipe": recipe, "profile": profile, "method": method, "created_at": created, "status": status, "headers": headers, "body": text, "transformed": derived}, nil
}
func DeleteHTTPHistory(ctx context.Context, path, collection string, ids []int64, confirmed bool) (int64, error) {
	if !confirmed {
		return 0, fmt.Errorf("删除历史不可恢复，需要明确授权")
	}
	if collection == "" || len(ids) < 1 || len(ids) > 1000 {
		return 0, fmt.Errorf("必须明确集合和1..1000个历史ID")
	}
	seen := map[int64]bool{}
	args := []any{collection}
	placeholders := []string{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return 0, fmt.Errorf("历史ID必须唯一且为正整数")
		}
		seen[id] = true
		args = append(args, id)
		placeholders = append(placeholders, "?")
	}
	db, e := historyManagementDB(path, true)
	if e != nil {
		return 0, e
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, "DELETE FROM http_history WHERE collection=? AND id IN ("+strings.Join(placeholders, ",")+")", args...)
	if e != nil {
		return 0, e
	}
	count, e := result.RowsAffected()
	if e != nil {
		return 0, e
	}
	if count != int64(len(ids)) {
		return 0, fmt.Errorf("历史ID缺失或不属于当前集合，整批取消")
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return count, nil
}
