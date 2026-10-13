package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
)

// Every engine call in this file is injected. These tests never connect to an
// OPC UA endpoint, and do not certify behavior against an industrial device.
const oneClickTestFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func oneClickTestRequest(id string) config.Request {
	return config.Request{ID: id, Name: id, Protocol: "opcua", Action: "scan", Endpoint: "opc.tcp://example.invalid:4840/" + id, Params: map[string]any{"node_id": "i=85"}}
}

func oneClickTestTarget(t *testing.T, id string) uaOneClickTarget {
	t.Helper()
	target, err := uaOneClickTargetFor(oneClickTestRequest(id))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func oneClickTestDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "export")
}

func oneClickTestScan(emit engine.Emit, complete bool, reason string, ids ...string) {
	emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": oneClickTestFingerprint}})
	emit(scanNode("i=85", "", "Object", 0))
	for _, id := range ids {
		emit(scanNode(id, "i=85", "Variable", 1))
	}
	emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"complete": complete, "reason": reason, "nodes": len(ids) + 1, "variables": len(ids)}})
}

func oneClickTestAssertRead(t *testing.T, r config.Request, write bool) []string {
	t.Helper()
	if write || r.Mutates() || r.Action != "read" {
		t.Fatalf("expected read-only value read, got action=%q write=%v", r.Action, write)
	}
	if got := r.String("expected_namespace_fingerprint", ""); got != oneClickTestFingerprint {
		t.Fatalf("read lost verified namespace fingerprint: %q", got)
	}
	ids := r.Strings("node_ids")
	if len(ids) == 0 || len(ids) > 10000 {
		t.Fatalf("read group has %d NodeIDs; expected 1..10000", len(ids))
	}
	return ids
}

func oneClickTestGoodRunner(t *testing.T) uaOneClickRunner {
	t.Helper()
	return func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		if write || r.Mutates() {
			t.Fatal("one-click export attempted a mutating operation")
		}
		switch r.Action {
		case "scan":
			oneClickTestScan(emit, true, "", "ns=2;s=Value")
		case "read":
			for _, id := range oneClickTestAssertRead(t, r, write) {
				emit(scanValue(id, int64(42)))
			}
		default:
			t.Fatalf("unexpected engine action %q", r.Action)
		}
		return nil
	}
}

func oneClickTestAssertFiles(t *testing.T, result uaOneClickServiceResult, rows int) {
	t.Helper()
	if result.CSV.Error != "" || result.JSON.Error != "" || result.CSV.Path == "" || result.JSON.Path == "" {
		t.Fatalf("snapshot files failed: CSV=%+v JSON=%+v", result.CSV, result.JSON)
	}
	csvRows := uaScanFileCSV(t, uaScanFileRead(t, result.CSV.Path))
	jsonData := uaScanFileJSON(t, uaScanFileRead(t, result.JSON.Path))
	jsonRows, ok := jsonData["rows"].([]any)
	if !ok || len(csvRows) != rows || len(jsonRows) != rows {
		t.Fatalf("persisted row counts: CSV=%d JSON=%d; want %d", len(csvRows), len(jsonRows), rows)
	}
}

func TestUAOneClickTargetNormalizesReadOnlyAndPreservesIdentity(t *testing.T) {
	r := oneClickTestRequest("identity")
	r.Action = "scan"
	r.Timeout = "45s"
	identity := map[string]any{
		"auth": "username", "username": "operator", "password": "FAKE_PASSWORD_FOR_TEST_ONLY",
		"security_policy": "Basic256Sha256", "security_mode": "SignAndEncrypt", "allow_insecure": false,
		"cert_file": "client-test.pem", "key_file": "client-test.key", "server_cert_sha256": strings.Repeat("b", 64),
		"auth_cert_file": "identity-test.pem", "auth_key_file": "identity-test.key",
	}
	for key, value := range identity {
		r.Params[key] = value
	}
	for key, value := range map[string]any{"method_id": "ns=2;s=Start", "object_id": "ns=2;s=Device", "arguments": []any{1}, "value": 99, "value_type": "Int32", "node_ids": []string{"ns=2;s=Old"}, "expected_namespace_fingerprint": strings.Repeat("c", 64)} {
		r.Params[key] = value
	}
	before := copyRequest(r)
	target, err := uaOneClickTargetFor(r)
	if err != nil {
		t.Fatal(err)
	}
	if target.Error != "" || target.Request.Action != "scan" || target.Request.Mutates() || target.Request.Protocol != "opcua" || target.Request.Endpoint != r.Endpoint || target.Request.Timeout != r.Timeout {
		t.Fatalf("unexpected normalized target: %+v", target)
	}
	if !reflect.DeepEqual(r, before) {
		t.Fatal("normalizing a target modified the saved source request")
	}
	for key, value := range identity {
		if !reflect.DeepEqual(target.Request.Params[key], value) {
			t.Errorf("identity/security parameter %q changed", key)
		}
	}
	if target.Request.Int("max_depth", -1) != 0 {
		t.Fatal("one-click scan unexpectedly imposes a depth limit")
	}
}

func TestUAOneClickInvalidTargetsRemainReportable(t *testing.T) {
	cases := []struct {
		name   string
		change func(*config.Request)
	}{
		{"protocol", func(r *config.Request) { r.Protocol = "http" }},
		{"action", func(r *config.Request) { r.Action = "call" }},
		{"endpoint", func(r *config.Request) { r.Endpoint = "https://example.invalid" }},
		{"empty host", func(r *config.Request) { r.Endpoint = "opc.tcp:///path" }},
		{"query", func(r *config.Request) { r.Endpoint += "?token=FAKE_TOKEN" }},
		{"fragment", func(r *config.Request) { r.Endpoint += "#fragment" }},
		{"embedded credentials", func(r *config.Request) { r.Endpoint = "opc.tcp://user:FAKE_PASSWORD@example.invalid:4840" }},
		{"timeout", func(r *config.Request) { r.Timeout = "-1s" }},
		{"root", func(r *config.Request) { r.Params["node_id"] = "ns=bad;i=85" }},
		{"max nodes", func(r *config.Request) { r.Params["max_nodes"] = 100001 }},
		{"max depth", func(r *config.Request) { r.Params["max_depth"] = -1 }},
		{"fractional depth", func(r *config.Request) { r.Params["max_depth"] = 1.5 }},
		{"read batch", func(r *config.Request) { r.Params["read_batch_size"] = 1001 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := oneClickTestRequest("invalid")
			tc.change(&r)
			target, err := uaOneClickTargetFor(r)
			if err == nil || target.Error == "" {
				t.Fatal("invalid target was accepted or its per-service error was discarded")
			}
			calls := 0
			report, _ := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{target}, func(context.Context, config.Request, bool, engine.Emit) error {
				calls++
				return nil
			}, writeUAScanSnapshot, nil)
			if calls != 0 || len(report.Services) != 1 || report.Failed != 1 || report.Services[0].Outcome != "fail" || report.Services[0].Reason == "" {
				t.Fatalf("invalid target was not reported before network work: calls=%d report=%+v", calls, report)
			}
		})
	}
}

func TestUAOneClickServiceFailuresContinueAndPartialScanStillReads(t *testing.T) {
	targets := []uaOneClickTarget{oneClickTestTarget(t, "offline"), oneClickTestTarget(t, "partial"), oneClickTestTarget(t, "healthy")}
	var calls []string
	run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		calls = append(calls, r.ID+":"+r.Action)
		if write || r.Mutates() {
			t.Fatal("export permitted writes")
		}
		switch r.Action {
		case "scan":
			if r.ID == "offline" {
				return errors.New("injected connection failure")
			}
			if r.ID == "partial" {
				oneClickTestScan(emit, false, "max_nodes reached", "ns=2;s=Partial")
				return errors.New("max_nodes reached")
			}
			oneClickTestScan(emit, true, "", "ns=2;s=Healthy")
		case "read":
			for _, id := range oneClickTestAssertRead(t, r, write) {
				emit(scanValue(id, int64(9223372036854775807)))
			}
		default:
			t.Fatalf("unexpected engine action %q", r.Action)
		}
		return nil
	}
	dir := oneClickTestDirectory(t)
	report, err := runUAOneClick(context.Background(), dir, targets, run, writeUAScanSnapshot, nil)
	if err != nil {
		t.Fatalf("per-service errors aborted the batch: %v", err)
	}
	wantCalls := []string{"offline:scan", "partial:scan", "partial:read", "healthy:scan", "healthy:read"}
	if !reflect.DeepEqual(calls, wantCalls) || report.Directory != dir || report.Cancelled || report.Failed != 1 || report.Partial != 1 || report.Success != 1 || len(report.Services) != 3 {
		t.Fatalf("batch continuation/counts incorrect: calls=%v report=%+v", calls, report)
	}
	if report.Services[0].Outcome != "fail" || !strings.Contains(report.Services[0].Reason, "injected connection failure") {
		t.Fatalf("connection error missing from service report: %+v", report.Services[0])
	}
	partial := report.Services[1]
	if partial.Outcome != "partial" || partial.ScanComplete || partial.Read != 1 || partial.Unread != 0 || partial.Rows != 1 || !strings.Contains(partial.Reason, "max_nodes") {
		t.Fatalf("partial scan was promoted to success or lost results: %+v", partial)
	}
	for _, result := range report.Services[1:] {
		if result.NamespaceFingerprint != oneClickTestFingerprint {
			t.Fatalf("service lost its namespace fingerprint: %+v", result)
		}
		oneClickTestAssertFiles(t, result, 1)
	}
}

func TestUAOneClickBadAndUnreadValuesCannotReportSuccess(t *testing.T) {
	ids := []string{"ns=2;s=Good", "ns=2;s=Bad", "ns=2;s=Unavailable", "ns=2;s=Missing"}
	run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		if r.Action == "scan" {
			oneClickTestScan(emit, true, "", ids...)
			emit(scanNode("ns=2;s=Method", "i=85", "Method", 1))
			emit(scanNode(ids[0], "i=85", "Variable", 1)) // A repeated reference is not a second variable.
			return nil
		}
		if got := oneClickTestAssertRead(t, r, write); !reflect.DeepEqual(got, ids) {
			t.Fatalf("read includes duplicate/non-variable nodes: %v", got)
		}
		emit(scanValue(ids[0], int64(7)))
		bad := scanValue(ids[1], nil)
		bad.Data.(map[string]any)["status"] = "BadNotReadable"
		bad.Data.(map[string]any)["has_value"] = false
		emit(bad)
		unavailable := scanValue(ids[2], nil)
		unavailable.Data.(map[string]any)["status"] = "Unavailable"
		unavailable.Data.(map[string]any)["has_value"] = false
		unavailable.Data.(map[string]any)["error"] = "server returned no DataValue"
		emit(unavailable)
		emit(scanValue("ns=2;s=Foreign", int64(99)))
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "values")}, run, writeUAScanSnapshot, nil)
	if err != nil || len(report.Services) != 1 {
		t.Fatalf("export failed: report=%+v err=%v", report, err)
	}
	result := report.Services[0]
	if result.Outcome != "partial" || result.Rows != 4 || result.Read != 3 || result.Unread != 1 || result.FailedValues != 2 || report.Success != 0 || report.Partial != 1 {
		t.Fatalf("bad/unread values were miscounted or treated as successful: %+v", report)
	}
	oneClickTestAssertFiles(t, result, 4)
	rows := uaScanFileCSV(t, uaScanFileRead(t, result.CSV.Path))
	byID := map[string]map[string]string{}
	for _, row := range rows {
		byID[row["node_id"]] = row
	}
	if byID[ids[1]]["status"] != "BadNotReadable" || byID[ids[1]]["has_value"] != "false" || byID[ids[2]]["has_value"] != "false" || byID[ids[2]]["error"] == "" || byID[ids[3]]["has_value"] != "false" || byID[ids[3]]["status"] == "" {
		t.Fatalf("export did not retain bad/unread diagnostics: %+v", byID)
	}
}

func TestUAOneClickRequiresNamespaceBeforeRead(t *testing.T) {
	for _, fingerprint := range []string{"", "too-short", strings.Repeat("z", 64)} {
		t.Run(fmt.Sprintf("fingerprint_%d_%s", len(fingerprint), fingerprint), func(t *testing.T) {
			reads := 0
			run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
				if r.Action == "read" {
					reads++
					return nil
				}
				if fingerprint != "" {
					emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": fingerprint}})
				}
				emit(scanNode("ns=2;s=Value", "i=85", "Variable", 1))
				emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"complete": true}})
				return nil
			}
			report, _ := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "namespace")}, run, writeUAScanSnapshot, nil)
			if reads != 0 || report.Success != 0 || len(report.Services) != 1 || report.Services[0].Reason == "" || report.Services[0].Unread != 1 {
				t.Fatalf("unverified namespace permitted a read or success: reads=%d report=%+v", reads, report)
			}
			oneClickTestAssertFiles(t, report.Services[0], 1)
		})
	}
}

func TestUAOneClickNamespaceChangeRetainsUnreadRowsAndContinues(t *testing.T) {
	good := oneClickTestGoodRunner(t)
	run := func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
		if r.ID == "changed" && r.Action == "read" {
			oneClickTestAssertRead(t, r, write)
			return errors.New("NamespaceArray changed; rescan required")
		}
		return good(ctx, r, write, emit)
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "changed"), oneClickTestTarget(t, "healthy")}, run, writeUAScanSnapshot, nil)
	if err != nil || len(report.Services) != 2 || report.Success != 1 {
		t.Fatalf("namespace mismatch aborted later service: report=%+v err=%v", report, err)
	}
	result := report.Services[0]
	if result.Outcome == "success" || result.Read != 0 || result.Unread != 1 || !strings.Contains(result.Reason, "rescan required") {
		t.Fatalf("namespace mismatch incorrectly succeeded: %+v", result)
	}
	oneClickTestAssertFiles(t, result, 1)
	oneClickTestAssertFiles(t, report.Services[1], 1)
}

func TestUAOneClickExistingDestinationPreventsEngineCalls(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "export")
			switch kind {
			case "directory":
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(dir, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "nonexistent"), dir); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			calls, writes := 0, 0
			_, err := runUAOneClick(context.Background(), dir, []uaOneClickTarget{oneClickTestTarget(t, "blocked")}, func(context.Context, config.Request, bool, engine.Emit) error { calls++; return nil }, func(context.Context, string, string, uaScanSnapshot) error { writes++; return nil }, nil)
			if err == nil || calls != 0 || writes != 0 {
				t.Fatalf("existing destination did not fail before I/O: err=%v calls=%d writes=%d", err, calls, writes)
			}
			if kind == "file" && string(uaScanFileRead(t, dir)) != "keep" {
				t.Fatal("existing destination was overwritten")
			}
		})
	}
}

func TestUAOneClickWriteFailureDoesNotBlockOtherFormatOrService(t *testing.T) {
	writes := 0
	writer := func(ctx context.Context, path, format string, snapshot uaScanSnapshot) error {
		writes++
		if snapshot.Endpoint == oneClickTestRequest("disk-error").Endpoint && format == "csv" {
			return errors.New("injected disk full")
		}
		return writeUAScanSnapshot(ctx, path, format, snapshot)
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "disk-error"), oneClickTestTarget(t, "healthy")}, oneClickTestGoodRunner(t), writer, nil)
	if err != nil || writes != 4 || len(report.Services) != 2 || report.Success != 1 {
		t.Fatalf("file failure aborted the batch: writes=%d report=%+v err=%v", writes, report, err)
	}
	first := report.Services[0]
	if first.Outcome == "success" || !strings.Contains(first.CSV.Error, "injected disk full") || first.JSON.Error != "" || first.JSON.Path == "" {
		t.Fatalf("file errors were hidden or JSON export was skipped: %+v", first)
	}
	uaScanFileJSON(t, uaScanFileRead(t, first.JSON.Path))
	oneClickTestAssertFiles(t, report.Services[1], 1)
}

func TestUAOneClickCancellationPreservesCurrentAndCompletedFiles(t *testing.T) {
	for _, cancelAt := range []string{"scan", "read"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			good := oneClickTestGoodRunner(t)
			var calls []string
			run := func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
				calls = append(calls, r.ID+":"+r.Action)
				if r.ID != "cancelled" {
					return good(ctx, r, write, emit)
				}
				if r.Action == "scan" {
					oneClickTestScan(emit, cancelAt != "scan", "", "ns=2;s=First", "ns=2;s=Second")
					if cancelAt != "scan" {
						return nil
					}
				} else {
					oneClickTestAssertRead(t, r, write)
					emit(scanValue("ns=2;s=First", int64(9)))
				}
				cancel()
				return ctx.Err()
			}
			finalWrites := 0
			writer := func(fileCtx context.Context, path, format string, snapshot uaScanSnapshot) error {
				if snapshot.Endpoint == oneClickTestRequest("cancelled").Endpoint {
					finalWrites++
					if fileCtx.Err() != nil {
						t.Fatal("cancelled engine context reused for final snapshot")
					}
					deadline, ok := fileCtx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Minute {
						t.Fatal("final snapshot lacks a short bounded independent context")
					}
				}
				return writeUAScanSnapshot(fileCtx, path, format, snapshot)
			}
			targets := []uaOneClickTarget{oneClickTestTarget(t, "completed"), oneClickTestTarget(t, "cancelled"), oneClickTestTarget(t, "not-started")}
			report, err := runUAOneClick(ctx, oneClickTestDirectory(t), targets, run, writer, nil)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("unexpected batch error: %v", err)
			}
			if !report.Cancelled || len(report.Services) != 3 || report.Success != 1 || finalWrites != 2 {
				t.Fatalf("cancelled batch lost current/completed service: calls=%v report=%+v finalWrites=%d", calls, report, finalWrites)
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "not-started:") {
					t.Fatal("cancellation did not stop later services")
				}
			}
			skipped := report.Services[2]
			if skipped.Outcome == "success" || skipped.Reason == "" || skipped.CSV.Path != "" || skipped.JSON.Path != "" {
				t.Fatalf("unstarted service acquired misleading results: %+v", skipped)
			}
			current := report.Services[1]
			wantRead := 0
			if cancelAt == "read" {
				wantRead = 1
			}
			if current.Outcome == "success" || current.Rows != 2 || current.Read != wantRead || current.Unread != 2-wantRead || current.Reason == "" {
				t.Fatalf("cancelled service lost its rows/diagnostics: %+v", current)
			}
			oneClickTestAssertFiles(t, report.Services[0], 1)
			oneClickTestAssertFiles(t, current, 2)
		})
	}
}

func TestUAOneClickOverTenThousandVariablesAreAllReadAndExported(t *testing.T) {
	const count = 10037
	target := oneClickTestTarget(t, "large")
	target.Request.Params["max_nodes"] = 100000
	seen := make(map[string]int, count)
	readCalls := 0
	run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		if r.Action == "scan" {
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprintf("ns=2;s=Value%05d", i)
			}
			oneClickTestScan(emit, true, "", ids...)
			return nil
		}
		readCalls++
		for _, id := range oneClickTestAssertRead(t, r, write) {
			seen[id]++
			emit(scanValue(id, int64(42)))
		}
		return nil
	}
	formats := map[string]bool{}
	writer := func(_ context.Context, _ string, format string, snapshot uaScanSnapshot) error {
		formats[format] = true
		if len(snapshot.Rows) != count || snapshot.NamespaceFingerprint != oneClickTestFingerprint {
			t.Fatalf("export silently truncated variables: rows=%d fingerprint=%q", len(snapshot.Rows), snapshot.NamespaceFingerprint)
		}
		for _, row := range snapshot.Rows {
			if !row.HasValue || row.Value != int64(42) || seen[row.NodeID] != 1 {
				t.Fatalf("lost/duplicate read in snapshot: %+v", row)
			}
		}
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{target}, run, writer, nil)
	if err != nil || readCalls < 2 || len(seen) != count || !formats["csv"] || !formats["json"] || len(report.Services) != 1 || report.Success != 1 || report.Services[0].Rows != count || report.Services[0].Read != count || report.Services[0].Unread != 0 {
		t.Fatalf("large read did not cover all variables: calls=%d seen=%d report=%+v err=%v", readCalls, len(seen), report, err)
	}
}

func TestUAOneClickSameEndpointDifferentIdentityRemainsSeparate(t *testing.T) {
	var targets []uaOneClickTarget
	for _, username := range []string{"operator-a", "operator-b"} {
		r := oneClickTestRequest(username)
		r.Endpoint = "opc.tcp://example.invalid:4840/shared"
		r.Params["auth"], r.Params["username"], r.Params["password"] = "username", username, "FAKE_PASSWORD_"+username
		r.Params["security_mode"], r.Params["security_policy"], r.Params["allow_insecure"] = "SignAndEncrypt", "Basic256Sha256", false
		r.Params["cert_file"], r.Params["key_file"], r.Params["server_cert_sha256"] = username+".pem", username+".key", strings.Repeat("d", 64)
		target, err := uaOneClickTargetFor(r)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	seen := map[string]int{}
	good := oneClickTestGoodRunner(t)
	run := func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
		username := r.String("username", "")
		seen[username+":"+r.Action]++
		if r.String("password", "") != "FAKE_PASSWORD_"+username || r.String("auth", "") != "username" || r.String("security_mode", "") != "SignAndEncrypt" || r.String("security_policy", "") != "Basic256Sha256" || r.Bool("allow_insecure") || r.String("cert_file", "") != username+".pem" || r.String("key_file", "") != username+".key" || r.String("server_cert_sha256", "") != strings.Repeat("d", 64) {
			t.Fatal("scan/read changed authentication or certificate parameters")
		}
		return good(ctx, r, write, emit)
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), targets, run, writeUAScanSnapshot, nil)
	if err != nil || report.Success != 2 || len(report.Services) != 2 || len(seen) != 4 {
		t.Fatalf("distinct identities were consolidated: seen=%v report=%+v err=%v", seen, report, err)
	}
	paths := map[string]bool{}
	for _, result := range report.Services {
		oneClickTestAssertFiles(t, result, 1)
		for _, file := range []uaOneClickFileResult{result.CSV, result.JSON} {
			if paths[file.Path] {
				t.Fatal("different services share an output file")
			}
			paths[file.Path] = true
			if strings.Contains(string(uaScanFileRead(t, file.Path)), "FAKE_PASSWORD_") {
				t.Fatal("snapshot persisted authentication parameters")
			}
		}
	}
}

func TestUAOneClickUnfinishedOrErroredScanNeverReportsSuccess(t *testing.T) {
	for _, mode := range []string{"missing-summary", "scan-error", "conflicting-namespace"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
				if r.Action == "scan" {
					emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": oneClickTestFingerprint}})
					emit(scanNode("ns=2;s=Value", "i=85", "Variable", 1))
					if mode == "scan-error" {
						emit(engine.Event{Kind: "scan-error", Data: map[string]any{"node_id": "ns=2;s=Folder", "error": "injected browse denied"}})
					}
					if mode == "conflicting-namespace" {
						emit(engine.Event{Kind: "scan-namespace", Data: map[string]any{"fingerprint": strings.Repeat("b", 64)}})
					}
					if mode != "missing-summary" {
						emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"complete": true}})
					}
					return nil
				}
				reads++
				for _, id := range oneClickTestAssertRead(t, r, write) {
					emit(scanValue(id, int64(42)))
				}
				return nil
			}
			report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, mode)}, run, writeUAScanSnapshot, nil)
			if err != nil || len(report.Services) != 1 || report.Success != 0 || report.Services[0].Outcome != "partial" || report.Services[0].Reason == "" {
				t.Fatalf("incomplete/inconsistent scan incorrectly succeeded: report=%+v err=%v", report, err)
			}
			if mode == "conflicting-namespace" {
				if reads != 0 || report.Services[0].NamespaceFingerprint != "" || report.Services[0].Unread != 1 {
					t.Fatal("conflicting namespace fingerprints allowed a value read")
				}
			} else if reads != 1 || report.Services[0].ScanComplete {
				t.Fatalf("available partial data was not read or scan was declared complete: reads=%d service=%+v", reads, report.Services[0])
			}
			oneClickTestAssertFiles(t, report.Services[0], 1)
		})
	}
}

func TestUAOneClickUsesOneServiceDeadlineAcrossScanAndReadGroups(t *testing.T) {
	target := oneClickTestTarget(t, "budget")
	target.Request.Timeout = "45s"
	target.Request.Params["max_nodes"] = 100000
	var deadline time.Time
	calls := 0
	run := func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
		calls++
		current, ok := ctx.Deadline()
		if !ok {
			t.Fatal("service network work has no deadline")
		}
		if deadline.IsZero() {
			deadline = current
		} else if !deadline.Equal(current) {
			t.Fatal("scan/read group reset the overall service timeout budget")
		}
		if r.Action == "scan" {
			ids := make([]string, 10001)
			for i := range ids {
				ids[i] = fmt.Sprintf("ns=2;s=%d", i)
			}
			oneClickTestScan(emit, true, "", ids...)
			return nil
		}
		for _, id := range oneClickTestAssertRead(t, r, write) {
			emit(scanValue(id, int64(42)))
		}
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{target}, run, func(context.Context, string, string, uaScanSnapshot) error { return nil }, nil)
	if err != nil || calls != 3 || report.Success != 1 {
		t.Fatalf("shared-budget export failed: calls=%d report=%+v err=%v", calls, report, err)
	}
}

func TestUAOneClickTimedOutServicePreservesRowsAndContinues(t *testing.T) {
	timedOut := oneClickTestTarget(t, "timeout")
	timedOut.Request.Timeout = "10ms"
	good := oneClickTestGoodRunner(t)
	var calls []string
	run := func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
		calls = append(calls, r.ID+":"+r.Action)
		if r.ID == "timeout" {
			if r.Action != "scan" {
				t.Fatal("timed-out service started a new read")
			}
			oneClickTestScan(emit, false, "deadline interrupted scan", "ns=2;s=Discovered")
			<-ctx.Done()
			return ctx.Err()
		}
		return good(ctx, r, write, emit)
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{timedOut, oneClickTestTarget(t, "healthy")}, run, writeUAScanSnapshot, nil)
	if err != nil || report.Cancelled || report.Partial != 1 || report.Success != 1 || len(report.Services) != 2 || !reflect.DeepEqual(calls, []string{"timeout:scan", "healthy:scan", "healthy:read"}) {
		t.Fatalf("one timeout cancelled the entire batch: calls=%v report=%+v err=%v", calls, report, err)
	}
	if report.Services[0].Rows != 1 || report.Services[0].Unread != 1 || !strings.Contains(report.Services[0].Reason, "deadline") {
		t.Fatalf("timeout lost observations or reason: %+v", report.Services[0])
	}
	oneClickTestAssertFiles(t, report.Services[0], 1)
	oneClickTestAssertFiles(t, report.Services[1], 1)
}

func TestUAOneClickEmptySuccessfulScanExportsBothEmptyFormats(t *testing.T) {
	calls := 0
	run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		calls++
		if r.Action != "scan" || write {
			t.Fatal("empty scan attempted an unnecessary read or write")
		}
		oneClickTestScan(emit, true, "")
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "empty")}, run, writeUAScanSnapshot, nil)
	if err != nil || calls != 1 || report.Success != 1 || len(report.Services) != 1 || report.Services[0].Rows != 0 {
		t.Fatalf("valid empty address space incorrectly failed: calls=%d report=%+v err=%v", calls, report, err)
	}
	oneClickTestAssertFiles(t, report.Services[0], 0)
}

func TestUAOneClickGoodNullValueRemainsAnObservedValue(t *testing.T) {
	run := func(_ context.Context, r config.Request, write bool, emit engine.Emit) error {
		if r.Action == "scan" {
			oneClickTestScan(emit, true, "", "ns=2;s=Null")
			return nil
		}
		for _, id := range oneClickTestAssertRead(t, r, write) {
			event := scanValue(id, nil)
			event.Data.(map[string]any)["has_value"] = true
			event.Data.(map[string]any)["value_type"] = uint8(0)
			event.Data.(map[string]any)["value_type_name"] = "Null"
			emit(event)
		}
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "null")}, run, writeUAScanSnapshot, nil)
	if err != nil || report.Success != 1 || len(report.Services) != 1 || report.Services[0].Read != 1 || report.Services[0].Unread != 0 || report.Services[0].FailedValues != 0 {
		t.Fatalf("valid UA Null confused with missing DataValue: report=%+v err=%v", report, err)
	}
	oneClickTestAssertFiles(t, report.Services[0], 1)
	row := uaScanFileCSV(t, uaScanFileRead(t, report.Services[0].CSV.Path))[0]
	if row["has_value"] != "true" || row["value_type"] != "0" || row["value"] != "null" {
		t.Fatalf("valid UA Null lost in CSV: %+v", row)
	}
}

// synctest advances only a virtual clock: the CSV consumes its full 30-second
// allowance without making this regression test wait in real time.
func TestUAOneClickSnapshotFormatsHaveIndependentTimeouts(t *testing.T) {
	for _, cancelNetwork := range []bool{false, true} {
		t.Run(fmt.Sprintf("network_cancelled=%t", cancelNetwork), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				good := oneClickTestGoodRunner(t)
				run := func(ctx context.Context, r config.Request, allowWrites bool, emit engine.Emit) error {
					err := good(ctx, r, allowWrites, emit)
					if cancelNetwork && r.Action == "scan" {
						cancel()
						return context.Canceled
					}
					return err
				}
				var formats []string
				var fileContexts []context.Context
				started := time.Now()
				write := func(fileCtx context.Context, _ string, format string, _ uaScanSnapshot) error {
					formats = append(formats, format)
					fileContexts = append(fileContexts, fileCtx)
					deadline, bounded := fileCtx.Deadline()
					if !bounded || deadline.Sub(time.Now()) != 30*time.Second || fileCtx.Err() != nil {
						t.Errorf("%s did not get its own full live 30-second budget: deadline=%s now=%s err=%v", format, deadline, time.Now(), fileCtx.Err())
					}
					if format == "csv" {
						<-fileCtx.Done() // Expire CSV in virtual time.
						return fileCtx.Err()
					}
					if fileCtx == fileContexts[0] {
						t.Error("JSON reused the CSV context")
					}
					// JSON can still spend almost its entire independent budget.
					time.Sleep(29 * time.Second)
					return fileCtx.Err()
				}
				result := runUAOneClickService(ctx, "unused-no-files-created", 0, oneClickTestTarget(t, "separate-budgets"), run, write)
				if fmt.Sprint(formats) != "[csv json]" || time.Since(started) != 59*time.Second {
					t.Fatalf("unexpected write sequence/virtual duration: %v %s", formats, time.Since(started))
				}
				if result.Outcome != "partial" || result.CSV.Complete || !strings.Contains(result.CSV.Error, "deadline exceeded") || !result.JSON.Complete || result.JSON.Error != "" {
					t.Fatalf("CSV timeout prevented independent successful JSON: %+v", result)
				}
				for _, fileCtx := range fileContexts {
					if fileCtx.Err() == nil {
						t.Error("file context was not cancelled after writer returned")
					}
				}
			})
		})
	}
}
