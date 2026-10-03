package engine

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryRetentionConsentCannotCrossIdenticalDatabases(t *testing.T) {
	ctx, sourcePath, source := retentionFixture(t)
	_, targetPath, target := retentionFixture(t)
	// Identical timestamps and rows ensure contents alone cannot distinguish the
	// databases. Changing only the target path must invalidate deletion consent.
	at := time.Now().UTC()
	for _, body := range []string{"old", "keep"} {
		entry := HTTPHistoryEntry{Collection: "synthetic", Recipe: "fixture",
			Method: "GET", Time: at, Status: 200,
			Headers: http.Header{"X-Test": {"synthetic"}}, Body: []byte(body)}
		for _, h := range []*HTTPHistory{source, target} {
			if err := h.Add(ctx, entry); err != nil {
				t.Fatal(err)
			}
		}
	}
	policy := HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1}
	preview, err := PreviewHTTPHistoryRetention(ctx, sourcePath, policy)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyHTTPHistoryRetention(ctx, targetPath, preview, true); err == nil {
		t.Fatal("source consent deleted rows in another database")
	}
	after, err := os.ReadFile(targetPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("wrong-target rejection changed database bytes: %v", err)
	}
	status, err := HTTPHistoryStorageStatus(ctx, targetPath)
	if err != nil || status.Entries != 2 || status.Policy != DefaultHTTPHistoryPolicy() {
		t.Fatalf("wrong-target rejection changed rows or policy: %+v %v", status, err)
	}
	applied, err := ApplyHTTPHistoryRetention(ctx, sourcePath, preview, true)
	if err != nil || applied.DeleteEntries != 1 {
		t.Fatalf("original database no longer accepts its preview: %+v %v", applied, err)
	}
}

func TestHistoryRetentionConsentAcceptsCleanPathAlias(t *testing.T) {
	ctx, path, h := retentionFixture(t)
	retentionAdd(t, ctx, h, 0, "old")
	retentionAdd(t, ctx, h, 0, "keep")
	alias := filepath.Dir(path) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(path)
	preview, err := PreviewHTTPHistoryRetention(ctx, alias, HTTPHistoryPolicy{Mode: "prune", MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyHTTPHistoryRetention(ctx, path, preview, true)
	if err != nil || applied.DeleteEntries != 1 {
		t.Fatalf("normalized path rejected: %+v %v", applied, err)
	}
}
