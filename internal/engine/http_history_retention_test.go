package engine

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func retentionFixture(t *testing.T) (context.Context, string, *HTTPHistory) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	h, err := OpenHTTPHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return context.Background(), path, h
}
func retentionAdd(t *testing.T, ctx context.Context, h *HTTPHistory, age time.Duration, body string) {
	t.Helper()
	if err := h.Add(ctx, HTTPHistoryEntry{Collection: "synthetic", Recipe: "fixture", Time: time.Now().UTC().Add(-age), Status: 200, Headers: http.Header{"X-Test": {"中文"}}, Body: []byte(body), Transformed: []byte("derived")}); err != nil {
		t.Fatal(err)
	}
}
func retentionApply(t *testing.T, ctx context.Context, path string, p HTTPHistoryPolicy) HTTPHistoryRetentionPreview {
	t.Helper()
	preview, err := PreviewHTTPHistoryRetention(ctx, path, p)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyHTTPHistoryRetention(ctx, path, preview, true)
	if err != nil {
		t.Fatal(err)
	}
	return applied
}
func TestHistoryDefaultNonDestructiveAndPhysicalSize(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 200*24*time.Hour, "old record must survive")
	before, _ := os.ReadFile(path)
	status, err := HTTPHistoryStorageStatus(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("status mutated database")
	}
	info, _ := os.Stat(path)
	if status.Policy != DefaultHTTPHistoryPolicy() || status.Entries != 1 || status.DatabaseBytes != info.Size() || status.TotalBytes < status.DatabaseBytes || status.PayloadBytes <= int64(len("old record must survive")) {
		t.Fatalf("incorrect storage: %+v", status)
	}
	p := HTTPHistoryPolicy{Mode: "stop", MaxEntries: 1, MaxAgeDays: 1}
	plan := retentionApply(t, ctx, path, p)
	if plan.DeleteEntries != 0 || plan.KeepEntries != 1 {
		t.Fatalf("stop deletes records: %+v", plan)
	}
	err = h.Add(ctx, HTTPHistoryEntry{Collection: "synthetic", Time: time.Now(), Body: []byte("later")})
	if !errors.Is(err, ErrHTTPHistoryCapacity) {
		t.Fatalf("limit not enforced: %v", err)
	}
	rows, err := ListHTTPHistory(ctx, path, "synthetic", "")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
func TestHistoryPruneRequiresConfirmationAndFreshPreview(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 0, "one")
	retentionAdd(t, ctx, h, 0, "two")
	p := HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1}
	preview, err := PreviewHTTPHistoryRetention(ctx, path, p)
	if err != nil {
		t.Fatal(err)
	}
	if preview.DeleteEntries != 1 {
		t.Fatal(preview)
	}
	if _, err = ApplyHTTPHistoryRetention(ctx, path, preview, false); err == nil {
		t.Fatal("unconfirmed policy accepted")
	}
	retentionAdd(t, ctx, h, 0, "third")
	if _, err = ApplyHTTPHistoryRetention(ctx, path, preview, true); err == nil {
		t.Fatal("stale preview accepted")
	}
	status, _ := HTTPHistoryStorageStatus(ctx, path)
	if status.Entries != 3 || status.Policy.Mode != "stop" {
		t.Fatal(status)
	}
	preview = retentionApply(t, ctx, path, p)
	if preview.DeleteEntries != 2 {
		t.Fatal(preview)
	}
	retentionAdd(t, ctx, h, 0, "fourth")
	status, err = HTTPHistoryStorageStatus(ctx, path)
	if err != nil || status.Entries != 1 || status.Policy != p {
		t.Fatal(status, err)
	}
	latest, err := h.Latest(ctx, "synthetic", "", "fixture")
	if err != nil || string(latest.Body) != "fourth" {
		t.Fatal(latest, err)
	}
}
func TestHistoryPruneAgeBytesAndOversizedResponse(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 90*24*time.Hour, "expired")
	retentionAdd(t, ctx, h, 0, "recent")
	plan := retentionApply(t, ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxAgeDays: 30, MaxBytes: 200})
	if plan.DeleteEntries != 1 {
		t.Fatal(plan)
	}
	err := h.Add(ctx, HTTPHistoryEntry{Collection: "synthetic", Time: time.Now(), Body: bytes.Repeat([]byte("x"), 201)})
	if !errors.Is(err, ErrHTTPHistoryCapacity) {
		t.Fatal(err)
	}
	status, _ := HTTPHistoryStorageStatus(ctx, path)
	if status.Entries != 1 {
		t.Fatal("oversized entry deleted previous history")
	}
	plan = retentionApply(t, ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxBytes: 1})
	if plan.DeleteEntries != 1 || plan.KeepBytes != 0 {
		t.Fatal(plan)
	}
}
func TestHistoryCompactionExplicitPreservesLiveRows(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	for i := 0; i < 8; i++ {
		retentionAdd(t, ctx, h, 0, string(bytes.Repeat([]byte("x"), 32<<10)))
	}
	retentionApply(t, ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1})
	before, _ := HTTPHistoryStorageStatus(ctx, path)
	if _, err := CompactHTTPHistory(ctx, path, false); err == nil {
		t.Fatal("unconfirmed compaction accepted")
	}
	after, err := CompactHTTPHistory(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	if after.Entries != 1 || after.PayloadBytes != before.PayloadBytes || after.DatabaseBytes >= before.DatabaseBytes {
		t.Fatal(before, after)
	}
}
func TestHistoryPolicyInvalidExpiryAndMissingAreSafe(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 0, "keep")
	for _, p := range []HTTPHistoryPolicy{{Mode: "other"}, {Mode: "stop", MaxAgeDays: -1}, {Mode: "prune", MaxBytes: 1 << 41}} {
		if _, err := PreviewHTTPHistoryRetention(ctx, path, p); err == nil {
			t.Fatal("bad policy accepted", p)
		}
	}
	preview, _ := PreviewHTTPHistoryRetention(ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1})
	preview.At = time.Now().Add(-6 * time.Minute)
	if _, err := ApplyHTTPHistoryRetention(ctx, path, preview, true); err == nil {
		t.Fatal("expired preview accepted")
	}
	missing := filepath.Join(t.TempDir(), "missing.sqlite")
	if _, err := HTTPHistoryStorageStatus(ctx, missing); err == nil {
		t.Fatal("missing status should fail")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read created database")
	}
}

func TestHistoryPruneRejectsSameLengthAndMetadataEdits(t *testing.T) {
	for _, statement := range []string{
		"UPDATE http_history SET body=X'6E6577' WHERE id=1",
		"UPDATE http_history SET collection='migrated' WHERE id=1",
		"UPDATE http_history SET status=201 WHERE id=1",
		"UPDATE http_history SET headers=replace(headers,'Test','Next') WHERE id=1",
		"UPDATE http_history SET transformed=X'6368616E676564' WHERE id=1",
	} {
		t.Run(statement, func(t *testing.T) {
			ctx, path, h := retentionFixture(t)
			retentionAdd(t, ctx, h, 0, "old")
			retentionAdd(t, ctx, h, 0, "keep")
			preview, err := PreviewHTTPHistoryRetention(ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if _, err = ApplyHTTPHistoryRetention(ctx, path, preview, true); err == nil {
				t.Fatal("changed record passed stale deletion consent")
			}
			status, err := HTTPHistoryStorageStatus(ctx, path)
			if err != nil || status.Entries != 2 || status.Policy.Mode != "stop" {
				t.Fatal(status, err)
			}
		})
	}
}

func TestHistoryPreviewRejectsOversizedEditedBlob(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 0, "keep")
	if _, err := h.db.ExecContext(ctx, "UPDATE http_history SET body=zeroblob(?)", maxBody+1); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewHTTPHistoryRetention(ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1}); err == nil {
		t.Fatal("oversized edited blob accepted")
	}
	status, err := HTTPHistoryStorageStatus(ctx, path)
	if err != nil || status.Entries != 1 || status.Policy.Mode != "stop" {
		t.Fatal(status, err)
	}
}

func TestHistoryAutomaticPlanMatchesBoundPreview(t *testing.T) {
	ctx, _, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 90*24*time.Hour, "old")
	retentionAdd(t, ctx, h, 0, "new")
	retentionAdd(t, ctx, h, 0, "newest")
	policy := HTTPHistoryPolicy{Mode: "prune", MaxAgeDays: 30, MaxEntries: 1, MaxBytes: 200}
	at := time.Now().UTC()
	bound, err := retentionPlan(ctx, h.db, policy, at, true)
	if err != nil {
		t.Fatal(err)
	}
	light, err := retentionPlan(ctx, h.db, policy, at, false)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Token == "" || light.Token != "" {
		t.Fatal("only full previews may carry an approval token")
	}
	bound.Token = ""
	if !reflect.DeepEqual(bound, light) {
		t.Fatal("automatic plan differs from preview", bound, light)
	}
}

func TestHistoryPruneRejectsNullToEmptyTransformation(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 0, "old")
	retentionAdd(t, ctx, h, 0, "keep")
	if _, err := h.db.ExecContext(ctx, "UPDATE http_history SET transformed=NULL WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewHTTPHistoryRetention(ctx, path, HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.ExecContext(ctx, "UPDATE http_history SET transformed=X'' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyHTTPHistoryRetention(ctx, path, preview, true); err == nil {
		t.Fatal("NULL to empty BLOB change passed stale consent")
	}
}
