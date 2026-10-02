package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const historyDatabaseLimit = 64 << 20

type HistoryScriptPreview struct {
	Database   string `json:"database"`
	SQL        string `json:"sql"`
	Token      string `json:"token"`
	Bytes      int    `json:"backup_bytes"`
	Statements int    `json:"statements"`
}
type HistoryScriptResult struct {
	Backup  string             `json:"backup"`
	Results []HistorySQLResult `json:"results"`
}
type HistorySQLResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// historySQLStatements only permits in-process database operations. It rejects
// filesystem/extension access and embedded transaction escapes outside literals.
func historySQLStatements(script string) ([]string, error) {
	if len(script) > 65536 {
		return nil, fmt.Errorf("SQL最多64KiB")
	}
	switch strings.TrimSpace(script) {
	case ".tables":
		script = "SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name"
	case ".schema":
		script = "SELECT sql FROM sqlite_schema WHERE sql IS NOT NULL ORDER BY name"
	}
	parts := []string{}
	start := 0
	quote := byte(0)
	line, block := false, false
	for i := 0; i < len(script); i++ {
		c := script[i]
		if line {
			if c == '\n' {
				line = false
			}
			continue
		}
		if block {
			if c == '*' && i+1 < len(script) && script[i+1] == '/' {
				block = false
				i++
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				if quote != ']' && i+1 < len(script) && script[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if c == '-' && i+1 < len(script) && script[i+1] == '-' {
			line = true
			i++
			continue
		}
		if c == '/' && i+1 < len(script) && script[i+1] == '*' {
			block = true
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			continue
		}
		if c == '[' {
			quote = ']'
			continue
		}
		if c == ';' {
			parts = append(parts, script[start:i])
			start = i + 1
		}
	}
	if quote != 0 || block {
		return nil, fmt.Errorf("SQL引号/注释未闭合")
	}
	parts = append(parts, script[start:])
	out := []string{}
	started, ended := false, false
	for _, part := range parts {
		tokens := historySQLTokens(part)
		if len(tokens) == 0 {
			continue
		}
		// BEGIN/COMMIT are optional outer wrappers; one atomic transaction is owned here.
		if ended {
			return nil, fmt.Errorf("COMMIT之后不能再有语句")
		}
		if tokens[0] == "BEGIN" {
			if len(out) != 0 || started {
				return nil, fmt.Errorf("BEGIN只允许位于脚本起始")
			}
			for _, token := range tokens[1:] {
				if token != "TRANSACTION" && token != "IMMEDIATE" && token != "DEFERRED" && token != "EXCLUSIVE" {
					return nil, fmt.Errorf("无效BEGIN包装")
				}
			}
			started = true
			continue
		}
		if tokens[0] == "COMMIT" || tokens[0] == "END" {
			if len(tokens) > 2 || len(tokens) == 2 && tokens[1] != "TRANSACTION" || len(out) == 0 {
				return nil, fmt.Errorf("无效COMMIT包装")
			}
			ended = true
			continue
		}
		allowed := map[string]bool{"SELECT": true, "WITH": true, "EXPLAIN": true, "INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "CREATE": true, "DROP": true, "ALTER": true}
		if !allowed[tokens[0]] {
			return nil, fmt.Errorf("不支持的SQL语句%s", tokens[0])
		}
		for _, word := range tokens {
			switch word {
			case "ATTACH", "DETACH", "PRAGMA", "VACUUM", "LOAD_EXTENSION", "TRIGGER", "VIRTUAL", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE":
				return nil, fmt.Errorf("历史控制台禁止%s；不能访问其他文件、扩展或逃逸事务", word)
			}
		}
		out = append(out, strings.TrimSpace(part))
	}
	if len(out) == 0 || len(out) > 32 {
		return nil, fmt.Errorf("需要1..32条SQL语句")
	}
	return out, nil
}
func historySQLTokens(s string) []string {
	out := []string{}
	word := strings.Builder{}
	quote := byte(0)
	line, block := false, false
	flush := func() {
		if word.Len() > 0 {
			out = append(out, strings.ToUpper(word.String()))
			word.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if line {
			if c == '\n' {
				line = false
			}
			continue
		}
		if block {
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				block = false
				i++
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				if quote != ']' && i+1 < len(s) && s[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if c == '-' && i+1 < len(s) && s[i+1] == '-' {
			flush()
			line = true
			i++
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			flush()
			block = true
			i++
			continue
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			flush()
			quote = c
			if c == '[' {
				quote = ']'
			}
			continue
		}
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			word.WriteByte(c)
		} else {
			flush()
		}
	}
	flush()
	return out
}
func historySnapshot(ctx context.Context, conn *sql.Conn) ([]byte, error) {
	var pages, size int64
	if err := conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return nil, err
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
		return nil, err
	}
	if pages < 0 || size < 1 || pages > historyDatabaseLimit/size {
		return nil, fmt.Errorf("历史数据库超过64MiB控制台预算")
	}
	var data []byte
	err := conn.Raw(func(driver any) error {
		serializer, ok := driver.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return fmt.Errorf("当前驱动不支持一致快照备份")
		}
		var err error
		data, err = serializer.Serialize()
		return err
	})
	if len(data) > historyDatabaseLimit {
		return nil, fmt.Errorf("快照超过64MiB")
	}
	return data, err
}
func historyScriptToken(path, script string, data []byte) string {
	sum := sha256.New()
	sum.Write([]byte(path))
	sum.Write([]byte{0})
	sum.Write([]byte(script))
	sum.Write([]byte{0})
	sum.Write(data)
	return hex.EncodeToString(sum.Sum(nil))
}
func PreviewHTTPHistoryScript(ctx context.Context, path, script string) (*HistoryScriptPreview, error) {
	statements, err := historySQLStatements(script)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := historyManagementDB(abs, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	data, err := historySnapshot(ctx, conn)
	if err != nil {
		return nil, err
	}
	return &HistoryScriptPreview{Database: abs, SQL: script, Token: historyScriptToken(abs, script, data), Bytes: len(data), Statements: len(statements)}, nil
}
func ExecuteHTTPHistoryScript(ctx context.Context, path, script, token, backup string, confirmed bool) (*HistoryScriptResult, error) {
	if !confirmed || token == "" || backup == "" {
		return nil, fmt.Errorf("写SQL需要明确确认、预览令牌与新的备份文件")
	}
	statements, err := historySQLStatements(script)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	backup, err = filepath.Abs(backup)
	if err != nil {
		return nil, err
	}
	if backup == abs {
		return nil, fmt.Errorf("备份不能覆盖数据库")
	}
	db, err := historyManagementDB(abs, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	data, err := historySnapshot(ctx, conn)
	if err != nil {
		return nil, err
	}
	if historyScriptToken(abs, script, data) != token {
		return nil, fmt.Errorf("数据库或SQL已变更，请重新预览")
	}
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("备份失败，未执行SQL: %w", err)
	}
	result := &HistoryScriptResult{Backup: backup, Results: []HistorySQLResult{}}
	total := 0
	for _, statement := range statements {
		rows, err := conn.QueryContext(ctx, statement)
		if err != nil {
			return nil, fmt.Errorf("整批回滚；备份%s: %w", backup, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		item := HistorySQLResult{Columns: columns, Rows: [][]any{}}
		for rows.Next() {
			if len(item.Rows) >= 1000 {
				rows.Close()
				return nil, fmt.Errorf("超过1000行，整批回滚；请加LIMIT")
			}
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			b, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				return nil, err
			}
			total += len(b)
			if total > maxBody {
				rows.Close()
				return nil, fmt.Errorf("结果超过4MiB，整批回滚")
			}
			item.Rows = append(item.Rows, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		result.Results = append(result.Results, item)
	}
	// Reject oversized changes before commit, still keeping a recoverable original.
	if _, err = historySnapshot(ctx, conn); err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	return result, nil
}

// QueryHTTPHistoryScript supports multiple read-only statements and dot metadata
// commands, still enforcing SQLite query_only on a mode=ro connection.
func QueryHTTPHistoryScript(ctx context.Context, path, script string) ([]HistorySQLResult, error) {
	statements, err := historySQLStatements(script)
	if err != nil {
		return nil, err
	}
	for _, s := range statements {
		word := historySQLTokens(s)[0]
		if word != "SELECT" && word != "WITH" && word != "EXPLAIN" {
			return nil, fmt.Errorf("只读模式禁止%s", word)
		}
	}
	db, err := historyManagementDB(path, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := []HistorySQLResult{}
	total := 0
	for _, statement := range statements {
		rows, err := db.QueryContext(ctx, statement)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		item := HistorySQLResult{Columns: columns, Rows: [][]any{}}
		for rows.Next() {
			if len(item.Rows) >= 1000 {
				rows.Close()
				return nil, fmt.Errorf("查询超过1000行，请加LIMIT")
			}
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			b, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				return nil, err
			}
			total += len(b)
			if total > maxBody {
				rows.Close()
				return nil, fmt.Errorf("结果超过4MiB")
			}
			item.Rows = append(item.Rows, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
