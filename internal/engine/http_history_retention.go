package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// HTTPHistoryPolicy applies to this complete database, across collections.
// The default bounds future recording without deleting any existing records.
// Prune is enabled only by an explicit, previewed policy confirmation.
type HTTPHistoryPolicy struct {
	Mode       string `json:"mode"`
	MaxAgeDays int64  `json:"max_age_days"`
	MaxEntries int64  `json:"max_entries"`
	MaxBytes   int64  `json:"max_bytes"`
}

func DefaultHTTPHistoryPolicy() HTTPHistoryPolicy {
	return HTTPHistoryPolicy{Mode: "stop", MaxEntries: 10000, MaxBytes: 128 << 20}
}
func (p HTTPHistoryPolicy) Validate() error {
	if p.Mode != "stop" && p.Mode != "prune" {
		return errors.New("历史策略必须为 stop 或 prune")
	}
	if p.MaxAgeDays < 0 || p.MaxAgeDays > 36500 || p.MaxEntries < 0 || p.MaxEntries > 10000000 || p.MaxBytes < 0 || p.MaxBytes > 1<<40 {
		return errors.New("历史限制超出范围；0 表示不限")
	}
	return nil
}

type historyQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func historyPolicy(ctx context.Context, db historyQuerier) (HTTPHistoryPolicy, error) {
	p := DefaultHTTPHistoryPolicy()
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='http_history_policy'").Scan(&exists); err != nil {
		return p, err
	}
	if exists == 0 {
		return p, nil
	}
	var raw string
	err := db.QueryRowContext(ctx, "SELECT policy FROM http_history_policy WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

// PayloadBytes counts UTF-8 headers, raw body and transformed body. Physical
// SQLite, WAL and journal sizes are reported separately, never inferred from it.
type HTTPHistoryStorage struct {
	Entries       int64             `json:"entries"`
	PayloadBytes  int64             `json:"payload_bytes"`
	DatabaseBytes int64             `json:"database_bytes"`
	SidecarBytes  int64             `json:"sidecar_bytes"`
	TotalBytes    int64             `json:"total_bytes"`
	ReusableBytes int64             `json:"reusable_bytes"`
	Policy        HTTPHistoryPolicy `json:"policy"`
}

const historySizeSQL = "length(CAST(headers AS BLOB))+length(body)+coalesce(length(transformed),0)"

func HTTPHistoryStorageStatus(ctx context.Context, path string) (HTTPHistoryStorage, error) {
	var out HTTPHistoryStorage
	db, err := historyManagementDB(path, false)
	if err != nil {
		return out, err
	}
	defer db.Close()
	if err = db.QueryRowContext(ctx, "SELECT count(*),coalesce(sum("+historySizeSQL+"),0) FROM http_history").Scan(&out.Entries, &out.PayloadBytes); err != nil {
		return out, err
	}
	out.Policy, err = historyPolicy(ctx, db)
	if err != nil {
		return out, err
	}
	var page, free int64
	if err = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&page); err != nil {
		return out, err
	}
	if err = db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
		return out, err
	}
	out.ReusableBytes = page * free
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, e := os.Lstat(path + suffix)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return out, e
		}
		if !info.Mode().IsRegular() {
			return out, errors.New("历史数据库及辅助文件必须是普通文件")
		}
		if suffix == "" {
			out.DatabaseBytes = info.Size()
		} else {
			out.SidecarBytes += info.Size()
		}
	}
	out.TotalBytes = out.DatabaseBytes + out.SidecarBytes
	return out, nil
}

type HTTPHistoryRetentionPreview struct {
	Policy        HTTPHistoryPolicy `json:"policy"`
	At            time.Time         `json:"at"`
	Token         string            `json:"token"`
	DeleteEntries int64             `json:"delete_entries"`
	DeleteBytes   int64             `json:"delete_bytes"`
	KeepEntries   int64             `json:"keep_entries"`
	KeepBytes     int64             `json:"keep_bytes"`
	IDs           []int64           `json:"-"`
}

func retentionPlan(ctx context.Context, db historyQuerier, p HTTPHistoryPolicy, at time.Time) (HTTPHistoryRetentionPreview, error) {
	plan := HTTPHistoryRetentionPreview{Policy: p, At: at}
	if err := p.Validate(); err != nil {
		return plan, err
	}
	current, err := historyPolicy(ctx, db)
	if err != nil {
		return plan, err
	}
	hash := sha256.New()
	enc := json.NewEncoder(hash)
	_ = enc.Encode(p)
	_ = enc.Encode(current)
	_ = enc.Encode(at)
	rows, err := db.QueryContext(ctx, "SELECT id,created_at,"+historySizeSQL+" FROM http_history ORDER BY id DESC")
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	cutoff := at.Add(-time.Duration(p.MaxAgeDays) * 24 * time.Hour)
	for rows.Next() {
		var id, size int64
		var created string
		if err = rows.Scan(&id, &created, &size); err != nil {
			return plan, err
		}
		_ = enc.Encode([]any{id, created, size})
		timestamp, e := time.Parse(time.RFC3339Nano, created)
		if e != nil {
			return plan, fmt.Errorf("历史时间无效，未删除任何记录: %w", e)
		}
		remove := p.MaxAgeDays > 0 && timestamp.Before(cutoff) || p.MaxEntries > 0 && plan.KeepEntries >= p.MaxEntries || p.MaxBytes > 0 && plan.KeepBytes+size > p.MaxBytes
		if remove && p.Mode == "prune" {
			plan.IDs = append(plan.IDs, id)
			plan.DeleteEntries++
			plan.DeleteBytes += size
		} else {
			plan.KeepEntries++
			plan.KeepBytes += size
		}
	}
	if err = rows.Err(); err != nil {
		return plan, err
	}
	plan.Token = hex.EncodeToString(hash.Sum(nil))
	return plan, nil
}
func PreviewHTTPHistoryRetention(ctx context.Context, path string, p HTTPHistoryPolicy) (HTTPHistoryRetentionPreview, error) {
	db, e := historyManagementDB(path, false)
	if e != nil {
		return HTTPHistoryRetentionPreview{}, e
	}
	defer db.Close()
	return retentionPlan(ctx, db, p, time.Now().UTC())
}
func pruneHistory(ctx context.Context, tx *sql.Tx, plan HTTPHistoryRetentionPreview) error {
	// Prepared single-row deletes avoid SQLite parameter limits and remain atomic.
	stmt, err := tx.PrepareContext(ctx, "DELETE FROM http_history WHERE id=?")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range plan.IDs {
		if _, err = stmt.ExecContext(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func ApplyHTTPHistoryRetention(ctx context.Context, path string, preview HTTPHistoryRetentionPreview, confirmed bool) (HTTPHistoryRetentionPreview, error) {
	if !confirmed {
		return preview, errors.New("历史策略需要明确确认；自动清理会永久删除记录")
	}
	if age := time.Since(preview.At); age < 0 || age > 5*time.Minute {
		return preview, errors.New("历史策略预览已过期，请重新预览")
	}
	db, err := historyManagementDB(path, true)
	if err != nil {
		return preview, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return preview, err
	}
	defer tx.Rollback()
	fresh, err := retentionPlan(ctx, tx, preview.Policy, preview.At)
	if err != nil {
		return preview, err
	}
	if fresh.Token != preview.Token {
		return preview, errors.New("历史数据或策略已变化，请重新预览")
	}
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS http_history_policy (id INTEGER PRIMARY KEY CHECK(id=1),policy TEXT NOT NULL)"); err != nil {
		return preview, err
	}
	raw, _ := json.Marshal(preview.Policy)
	if _, err = tx.ExecContext(ctx, "INSERT INTO http_history_policy(id,policy) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET policy=excluded.policy", string(raw)); err != nil {
		return preview, err
	}
	if err = pruneHistory(ctx, tx, fresh); err != nil {
		return preview, err
	}
	return fresh, tx.Commit()
}

var ErrHTTPHistoryCapacity = errors.New("历史已达容量限制，本次未记录；已有记录未删除，请在历史管理中调整策略")

func (h *HTTPHistory) addBounded(ctx context.Context, e HTTPHistoryEntry, headers string, body []byte) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := historyPolicy(ctx, tx)
	if err != nil {
		return err
	}
	size := int64(len(headers) + len(body) + len(e.Transformed))
	if p.MaxBytes > 0 && size > p.MaxBytes {
		return ErrHTTPHistoryCapacity
	}
	if p.Mode == "stop" {
		var count, total int64
		if err = tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum("+historySizeSQL+"),0) FROM http_history").Scan(&count, &total); err != nil {
			return err
		}
		if p.MaxEntries > 0 && count >= p.MaxEntries || p.MaxBytes > 0 && total+size > p.MaxBytes || p.MaxAgeDays > 0 && e.Time.Before(time.Now().Add(-time.Duration(p.MaxAgeDays)*24*time.Hour)) {
			return ErrHTTPHistoryCapacity
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO http_history(collection,profile,recipe,method,created_at,status,headers,body,transformed) VALUES(?,?,?,?,?,?,?,?,?)`, e.Collection, e.Profile, e.Recipe, e.Method, e.Time.Format(time.RFC3339Nano), e.Status, headers, body, e.Transformed)
	if err != nil {
		return err
	}
	if p.Mode == "prune" {
		plan, e := retentionPlan(ctx, tx, p, time.Now().UTC())
		if e != nil {
			return e
		}
		if err = pruneHistory(ctx, tx, plan); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CompactHTTPHistory reclaims unused pages; it never removes live records.
// It is explicit and separate from request execution and policy changes. SQLite
// needs temporary disk space and an exclusive write window; callers require idle.
func CompactHTTPHistory(ctx context.Context, path string, confirmed bool) (HTTPHistoryStorage, error) {
	if !confirmed {
		return HTTPHistoryStorage{}, errors.New("整理数据库需要明确确认：可能暂时占用额外磁盘空间并阻止写入")
	}
	db, err := historyManagementDB(path, true)
	if err != nil {
		return HTTPHistoryStorage{}, err
	}
	_, err = db.ExecContext(ctx, "VACUUM")
	closeErr := db.Close()
	if err != nil {
		return HTTPHistoryStorage{}, err
	}
	if closeErr != nil {
		return HTTPHistoryStorage{}, closeErr
	}
	return HTTPHistoryStorageStatus(ctx, path)
}
