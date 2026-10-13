package tui

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopcua/opcua/ua"
)

func uaScanFileTestRows() []uaScanRow {
	t := time.Date(2026, 10, 13, 2, 3, 4, 567890123, time.UTC)
	return []uaScanRow{
		{NodeID: "ns=2;s=Temperature", ParentID: "i=85", DisplayName: "温度,\"传感器\"\n一号", BrowseName: "2:Temperature", NodeClass: "Variable", Depth: 2, HasValue: true, Value: int64(math.MinInt64), ValueType: uint8(8), ValueTypeName: "Int64", Status: "Good", SourceTimestamp: t, ServerTimestamp: t.Add(time.Millisecond), ObservedAt: t, Kind: "value"},
		{NodeID: "ns=2;s=Folder", ParentID: "i=85", DisplayName: "Folder", BrowseName: "2:Folder", NodeClass: "Object", Depth: 1, HasValue: false, Status: "Unread", ObservedAt: t, Kind: "node"},
	}
}

func uaScanFileTestSnapshot() uaScanSnapshot {
	return uaScanSnapshot{Endpoint: "opc.tcp://localhost:4840/path", RootNode: "i=85", ExportedAt: time.Date(2026, 10, 13, 3, 4, 5, 0, time.UTC), ScanComplete: false, ScanReason: "max_nodes", NamespaceFingerprint: "namespace-sha256", Rows: uaScanFileTestRows()}
}

func uaScanFileRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func uaScanFileJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value map[string]any
	if err := d.Decode(&value); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected trailing JSON: %v", err)
	}
	return value
}

func uaScanFileCSV(t *testing.T, b []byte) []map[string]string {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(b))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v\n%s", err, b)
	}
	if len(records) == 0 {
		t.Fatal("missing CSV header")
	}
	var rows []map[string]string
	for _, record := range records[1:] {
		row := make(map[string]string)
		for i, field := range record {
			row[records[0][i]] = field
		}
		rows = append(rows, row)
	}
	return rows
}

func TestUAScanSnapshotFilesRoundTrip(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot."+format)
			snapshot := uaScanFileTestSnapshot()
			if err := writeUAScanSnapshot(context.Background(), path, format, snapshot); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("permissions = %o, want 0600", info.Mode().Perm())
			}
			data := uaScanFileRead(t, path)
			if format == "json" {
				value := uaScanFileJSON(t, data)
				if value["endpoint"] != snapshot.Endpoint || value["root_node"] != snapshot.RootNode || value["scan_complete"] != false || value["scan_reason"] != "max_nodes" || value["namespace_fingerprint"] != "namespace-sha256" {
					t.Fatalf("missing snapshot metadata: %v", value)
				}
				rows := value["rows"].([]any)
				if len(rows) != len(snapshot.Rows) {
					t.Fatalf("rows = %d", len(rows))
				}
				first := rows[0].(map[string]any)
				if first["value"] != json.Number("-9223372036854775808") || first["value_type"] != json.Number("8") || first["has_value"] != true || first["value_is_array"] != false || first["display_name"] != snapshot.Rows[0].DisplayName || first["status"] != "Good" {
					t.Fatalf("row data lost: %v", first)
				}
				if first["source_timestamp"] != "2026-10-13T02:03:04.567890123Z" || first["server_timestamp"] != "2026-10-13T02:03:04.568890123Z" {
					t.Fatalf("timestamp precision lost: %v", first)
				}
				unread := rows[1].(map[string]any)
				if unread["has_value"] != false || unread["value"] != nil || unread["source_timestamp"] != nil || unread["node_class"] != "Object" {
					t.Fatalf("unread node metadata lost: %v", unread)
				}
			} else {
				rows := uaScanFileCSV(t, data)
				if len(rows) != len(snapshot.Rows) {
					t.Fatalf("rows = %d", len(rows))
				}
				first := rows[0]
				want := map[string]string{
					"endpoint": snapshot.Endpoint, "root_node": "i=85", "exported_at": "2026-10-13T03:04:05Z", "scan_complete": "false", "scan_reason": "max_nodes", "node_id": "ns=2;s=Temperature", "parent_id": "i=85", "display_name": snapshot.Rows[0].DisplayName, "browse_name": "2:Temperature", "node_class": "Variable", "depth": "2", "has_value": "true", "value": "-9223372036854775808", "value_type": "8", "value_type_name": "Int64", "value_is_array": "false", "status": "Good", "source_timestamp": "2026-10-13T02:03:04.567890123Z", "server_timestamp": "2026-10-13T02:03:04.568890123Z", "observed_at": "2026-10-13T02:03:04.567890123Z", "kind": "value", "error": "", "namespace_fingerprint": "namespace-sha256",
				}
				if !reflect.DeepEqual(first, want) {
					t.Fatalf("CSV metadata/value changed:\ngot %#v\nwant %#v", first, want)
				}
				if rows[1]["has_value"] != "false" || rows[1]["value"] != "null" || rows[1]["source_timestamp"] != "null" {
					t.Fatalf("unread row lost: %v", rows[1])
				}
			}
		})
	}
}

func TestUAScanFilesNeverOverwriteAndRequireExplicitPaths(t *testing.T) {
	for _, recorder := range []bool{false, true} {
		t.Run(fmt.Sprint("recorder=", recorder), func(t *testing.T) {
			write := func(path string) error {
				if recorder {
					r, err := newUAScanRecorder(path, "jsonl", "opc.tcp://localhost:4840")
					if err == nil {
						err = r.Close()
					}
					return err
				}
				return writeUAScanSnapshot(context.Background(), path, "json", uaScanFileTestSnapshot())
			}
			for _, path := range []string{"", " \t\n"} {
				if err := write(path); err == nil || !strings.Contains(err.Error(), "path") {
					t.Fatalf("empty path accepted: %q, %v", path, err)
				}
			}
			dir := t.TempDir()
			target := filepath.Join(dir, "existing")
			if err := os.WriteFile(target, []byte("do not change"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := write(target); !errors.Is(err, os.ErrExist) {
				t.Fatalf("existing file: %v", err)
			}
			if got := string(uaScanFileRead(t, target)); got != "do not change" {
				t.Fatalf("existing file changed: %s", got)
			}
			for i, destination := range []string{target, filepath.Join(dir, "nonexistent")} {
				link := filepath.Join(dir, fmt.Sprintf("link-%d", i))
				if err := os.Symlink(destination, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				if err := write(link); !errors.Is(err, os.ErrExist) {
					t.Fatalf("symlink accepted: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "nonexistent")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dangling symlink target created: %v", err)
			}
		})
	}
}

func TestUAScanFilesValidateFormatsBeforeCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid")
	if err := writeUAScanSnapshot(context.Background(), path, "jsonl", uaScanFileTestSnapshot()); err == nil {
		t.Fatal("snapshot accepted jsonl")
	}
	if _, err := newUAScanRecorder(path, "json", "opc.tcp://host:4840"); err == nil {
		t.Fatal("recorder accepted json")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid request created a file: %v", err)
	}
}

func TestUAScanCSVDefendsEveryStringField(t *testing.T) {
	for _, attack := range []string{"=1+2", "+cmd", "-cmd", "@SUM(A1)", " \t\n=1", "\r-1", "\u2003\t+cmd", "\x00\t@cmd"} {
		t.Run(fmt.Sprintf("%q", attack), func(t *testing.T) {
			row := uaScanRow{NodeID: attack, ParentID: attack, DisplayName: attack, BrowseName: attack, NodeClass: attack, HasValue: true, Value: attack, ValueType: attack, ValueTypeName: attack, Status: attack, SourceTimestamp: attack, ServerTimestamp: attack, Kind: attack, Error: attack}
			data, err := uaScanCSVRecord(uaScanSnapshot{Endpoint: attack, RootNode: attack, ScanReason: attack, NamespaceFingerprint: attack}, row)
			if err != nil {
				t.Fatal(err)
			}
			header, _ := uaScanCSVLine(uaScanCSVColumns)
			got := uaScanFileCSV(t, append(header, data...))[0]
			for _, col := range []string{"endpoint", "root_node", "scan_reason", "node_id", "parent_id", "display_name", "browse_name", "node_class", "value", "value_type", "value_type_name", "status", "source_timestamp", "server_timestamp", "kind", "error", "namespace_fingerprint"} {
				if got[col] != "'"+attack {
					t.Errorf("%s was not neutralized: %q", col, got[col])
				}
			}
		})
	}
	for _, safe := range []string{"", "hello", "  name", "ns=2;s=x", "a+b", "'already escaped"} {
		if got := uaScanCSVSafe(safe); got != safe {
			t.Errorf("safe text changed: %q -> %q", safe, got)
		}
	}
}

func TestUAScanJSONRetainsIntegersAndSpecialFloats(t *testing.T) {
	value := map[string]any{
		"i64": int64(math.MinInt64), "u64": uint64(math.MaxUint64),
		"integers": []uint64{9007199254740993, math.MaxUint64},
		"floats":   []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1.25},
		"f32":      [2]float32{float32(math.Inf(1)), 2.5},
		"nested":   []any{[]any{math.NaN(), int64(math.MaxInt64)}},
		"bytes":    []byte{0, 1, 255}, "nil": []float64(nil),
	}
	for _, format := range []string{"json", "csv", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "special."+format)
			row := uaScanRow{NodeID: "i=1", HasValue: true, Value: value, ValueType: uint64(math.MaxUint64), ValueIsArray: true, Kind: "notification"}
			var data []byte
			if format == "jsonl" {
				r, err := newUAScanRecorder(path, format, "opc.tcp://host:4840")
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Write(row); err != nil {
					t.Fatal(err)
				}
				if err = r.Close(); err != nil {
					t.Fatal(err)
				}
				data = uaScanFileRead(t, path)
			} else {
				if err := writeUAScanSnapshot(context.Background(), path, format, uaScanSnapshot{Endpoint: "opc.tcp://host:4840", Rows: []uaScanRow{row}}); err != nil {
					t.Fatal(err)
				}
				data = uaScanFileRead(t, path)
			}
			var got map[string]any
			if format == "csv" {
				csvRow := uaScanFileCSV(t, data)[0]
				if csvRow["value_type"] != "18446744073709551615" {
					t.Fatalf("CSV uint64 changed: %s", csvRow["value_type"])
				}
				got = uaScanFileJSON(t, []byte(csvRow["value"]))
			} else {
				record := uaScanFileJSON(t, data)
				if format == "json" {
					record = record["rows"].([]any)[0].(map[string]any)
				}
				if record["value_type"] != json.Number("18446744073709551615") {
					t.Fatalf("uint64 type changed: %v", record["value_type"])
				}
				got = record["value"].(map[string]any)
			}
			if got["i64"] != json.Number("-9223372036854775808") || got["u64"] != json.Number("18446744073709551615") {
				t.Fatalf("integer precision lost: %v", got)
			}
			if want := []any{json.Number("9007199254740993"), json.Number("18446744073709551615")}; !reflect.DeepEqual(got["integers"], want) {
				t.Fatalf("integer array precision lost: %v", got["integers"])
			}
			floats := got["floats"].([]any)
			for i, want := range []string{"NaN", "+Inf", "-Inf"} {
				if floats[i].(map[string]any)["$float"] != want {
					t.Errorf("float %d = %v", i, floats[i])
				}
			}
			if floats[3] != json.Number("1.25") || got["bytes"] != "AAH/" || got["nil"] != nil {
				t.Fatalf("finite/ByteString/nil value changed: %v", got)
			}
			if got["f32"].([]any)[0].(map[string]any)["$float"] != "+Inf" {
				t.Fatal("typed float32 array was not preserved")
			}
		})
	}
}

func TestUAScanRecorderFlushesEachRowAndTracksCounts(t *testing.T) {
	for _, format := range []string{"csv", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record."+format)
			r, err := newUAScanRecorder(path, format, "opc.tcp://alice:top-secret@localhost:4840/path?token=secret#secret")
			if err != nil {
				t.Fatal(err)
			}
			for i, row := range uaScanFileTestRows() {
				if err := r.Write(row); err != nil {
					t.Fatal(err)
				}
				data := uaScanFileRead(t, path)
				if r.Count() != i+1 || r.Bytes() != int64(len(data)) {
					t.Fatalf("count/bytes = %d/%d, file bytes = %d", r.Count(), r.Bytes(), len(data))
				}
				if strings.Contains(string(data), "alice") || strings.Contains(string(data), "secret") || strings.Contains(string(data), "token") {
					t.Fatalf("credentials leaked: %s", data)
				}
				if format == "csv" {
					rows := uaScanFileCSV(t, data)
					if len(rows) != i+1 || rows[i]["endpoint"] != "opc.tcp://localhost:4840/path" {
						t.Fatalf("CSV row was not flushed: %v", rows)
					}
				} else {
					lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
					if len(lines) != i+1 {
						t.Fatalf("JSONL lines = %d", len(lines))
					}
					if record := uaScanFileJSON(t, lines[i]); record["endpoint"] != "opc.tcp://localhost:4840/path" || record["node_id"] != row.NodeID {
						t.Fatalf("JSONL row lost: %v", record)
					}
				}
			}
			for i := 0; i < 2; i++ {
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Write(uaScanRow{}); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("write after close: %v", err)
			}
			info, _ := os.Stat(path)
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("permissions = %o", info.Mode().Perm())
			}
		})
	}
}

type uaScanTestSink struct {
	bytes.Buffer
	writeErr, syncErr, closeErr error
	short                       bool
	writes, syncs, closes       int
	afterWrite                  func()
}

func (s *uaScanTestSink) Write(p []byte) (int, error) {
	s.writes++
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	if s.short {
		return s.Buffer.Write(p[:len(p)/2])
	}
	n, err := s.Buffer.Write(p)
	if s.afterWrite != nil {
		s.afterWrite()
	}
	return n, err
}
func (s *uaScanTestSink) Sync() error  { s.syncs++; return s.syncErr }
func (s *uaScanTestSink) Close() error { s.closes++; return s.closeErr }

func TestUAScanFileLimitsNeverSilentlyDropRows(t *testing.T) {
	if uaScanRecordLimit != 100000 || uaScanFileLimit != 128<<20 {
		t.Fatal("unexpected production limits")
	}
	t.Run("recorder rows", func(t *testing.T) {
		sink := &uaScanTestSink{}
		r, err := newUAScanRecorderTo("test.jsonl", "jsonl", "opc.tcp://host:4840", sink, 2, uaScanFileLimit)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := r.Write(uaScanRow{NodeID: fmt.Sprint(i)}); err != nil {
				t.Fatal(err)
			}
		}
		before := sink.String()
		if err := r.Write(uaScanRow{NodeID: "overflow"}); err == nil || !strings.Contains(err.Error(), "record limit (2") || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("record limit was not reported: %v", err)
		}
		if r.Count() != 2 || sink.String() != before {
			t.Fatal("limit changed the existing records")
		}
		if err := r.Close(); err == nil {
			t.Fatal("close hid the record failure")
		}
	})
	t.Run("recorder bytes", func(t *testing.T) {
		sink := &uaScanTestSink{}
		r, err := newUAScanRecorderTo("test.jsonl", "jsonl", "opc.tcp://host:4840", sink, 10, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Write(uaScanRow{NodeID: "first"}); err != nil {
			t.Fatal(err)
		}
		before := sink.String()
		if err := r.Write(uaScanRow{Value: strings.Repeat("x", 1024)}); err == nil || !strings.Contains(err.Error(), "byte limit (1024") {
			t.Fatalf("byte limit was not reported: %v", err)
		}
		if sink.String() != before || r.Count() != 1 || r.Bytes() > 1024 {
			t.Fatal("overflow record was partially written")
		}
		_ = r.Close()
	})
	t.Run("snapshot rows", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "limit.json")
		err := writeUAScanSnapshot(context.Background(), path, "json", uaScanSnapshot{Rows: make([]uaScanRow, uaScanRecordLimit+1)})
		if err == nil || !strings.Contains(err.Error(), "record limit") {
			t.Fatalf("snapshot limit was not reported: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("oversized snapshot created a file")
		}
	})
	t.Run("snapshot bytes", func(t *testing.T) {
		sink := &uaScanTestSink{}
		err := writeUAScanSnapshotTo(context.Background(), "limit.json", "json", uaScanFileTestSnapshot(), sink, 256)
		if err == nil || !strings.Contains(err.Error(), "byte limit") || !strings.Contains(err.Error(), "incomplete") || sink.Len() > 256 {
			t.Fatalf("snapshot byte limit failure: %v, bytes %d", err, sink.Len())
		}
		if sink.syncs != 1 || sink.closes != 1 {
			t.Fatal("snapshot did not close after limit")
		}
	})
}

func TestUAScanFileWriteSyncAndCloseFailures(t *testing.T) {
	writeErr, syncErr, closeErr := errors.New("disk write failed"), errors.New("disk sync failed"), errors.New("file close failed")
	t.Run("recorder write and close", func(t *testing.T) {
		sink := &uaScanTestSink{writeErr: writeErr, syncErr: syncErr, closeErr: closeErr}
		r, err := newUAScanRecorderTo("fail.jsonl", "jsonl", "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Write(uaScanRow{}); !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("write failure not reported immediately: %v", err)
		}
		if r.Count() != 0 || r.Bytes() != 0 {
			t.Fatal("failed record counted as successful")
		}
		err = r.Close()
		for _, want := range []error{writeErr, syncErr, closeErr} {
			if !errors.Is(err, want) {
				t.Errorf("close hid %v: %v", want, err)
			}
		}
		_ = r.Close()
		if sink.syncs != 1 || sink.closes != 1 {
			t.Fatalf("close operations = %d/%d", sink.syncs, sink.closes)
		}
	})
	t.Run("CSV header flush", func(t *testing.T) {
		sink := &uaScanTestSink{writeErr: writeErr}
		r, err := newUAScanRecorderTo("fail.csv", "csv", "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
		if r != nil || !errors.Is(err, writeErr) || sink.closes != 1 {
			t.Fatalf("CSV constructor lost header flush failure: %v", err)
		}
	})
	t.Run("short write", func(t *testing.T) {
		sink := &uaScanTestSink{short: true}
		r, err := newUAScanRecorderTo("short.jsonl", "jsonl", "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Write(uaScanRow{}); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("short write not reported: %v", err)
		}
		if r.Count() != 0 || r.Bytes() != int64(sink.Len()) || r.Bytes() == 0 {
			t.Fatal("partial-write accounting wrong")
		}
		_ = r.Close()
	})
	for _, format := range []string{"json", "csv"} {
		t.Run("snapshot "+format, func(t *testing.T) {
			sink := &uaScanTestSink{writeErr: writeErr, syncErr: syncErr, closeErr: closeErr}
			err := writeUAScanSnapshotTo(context.Background(), "fail."+format, format, uaScanFileTestSnapshot(), sink, uaScanFileLimit)
			for _, want := range []error{writeErr, syncErr, closeErr} {
				if !errors.Is(err, want) {
					t.Errorf("snapshot lost %v: %v", want, err)
				}
			}
			if sink.closes != 1 || sink.syncs != 1 {
				t.Fatal("snapshot failed to release sink")
			}
		})
	}
}

func TestUAScanSnapshotCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "cancel.json")
	if err := writeUAScanSnapshot(ctx, path, "json", uaScanFileTestSnapshot()); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel not reported: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-cancel created a file")
	}
	for _, format := range []string{"json", "csv"} {
		for _, cancelAt := range []int{1, 2, 3, 4} {
			if format == "csv" && cancelAt == 4 {
				continue // CSV has a header and two rows, with no trailer.
			}
			t.Run(fmt.Sprintf("%s/write-%d", format, cancelAt), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				sink := &uaScanTestSink{}
				sink.afterWrite = func() {
					if sink.writes == cancelAt {
						cancel()
					}
				}
				err := writeUAScanSnapshotTo(ctx, "cancel."+format, format, uaScanFileTestSnapshot(), sink, uaScanFileLimit)
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "incomplete") {
					t.Fatalf("mid-write cancellation not reported: %v", err)
				}
				if sink.writes != cancelAt || sink.closes != 1 || sink.syncs != 1 {
					t.Fatalf("continued after cancellation or leaked sink: writes=%d syncs=%d closes=%d", sink.writes, sink.syncs, sink.closes)
				}
			})
		}
	}
}

func TestUAScanFilesRejectUnserializableValues(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	for _, value := range []any{make(chan int), cycle, map[bool]int{true: 1}} {
		sink := &uaScanTestSink{}
		r, err := newUAScanRecorderTo("bad.jsonl", "jsonl", "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Write(uaScanRow{Value: value}); err == nil || !strings.Contains(err.Error(), "encode record") || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("unsupported value silently accepted: %v", err)
		}
		if r.Count() != 0 || r.Bytes() != 0 {
			t.Fatal("unsupported value produced a partial row")
		}
		_ = r.Close()
	}
}

func TestUAScanRecorderConcurrentAccess(t *testing.T) {
	sink := &uaScanTestSink{}
	r, err := newUAScanRecorderTo("concurrent.jsonl", "jsonl", "opc.tcp://host:4840", sink, 1000, uaScanFileLimit)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := r.Write(uaScanRow{NodeID: fmt.Sprintf("%d:%d", i, j)}); err != nil {
					t.Error(err)
				}
				_ = r.Count()
				_ = r.Bytes()
			}
		}(i)
	}
	wg.Wait()
	if r.Count() != 200 || r.Bytes() != int64(sink.Len()) {
		t.Fatalf("lost concurrent rows: %d", r.Count())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if lines := bytes.Count(sink.Bytes(), []byte("\n")); lines != 200 {
		t.Fatalf("JSONL lines = %d", lines)
	}
}

func TestUAScanEmptySnapshotsAreParseable(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		sink := &uaScanTestSink{}
		if err := writeUAScanSnapshotTo(context.Background(), "empty."+format, format, uaScanSnapshot{}, sink, uaScanFileLimit); err != nil {
			t.Fatal(err)
		}
		if format == "json" {
			if rows, ok := uaScanFileJSON(t, sink.Bytes())["rows"].([]any); !ok || len(rows) != 0 {
				t.Fatalf("empty rows should be an array: %s", sink.String())
			}
		} else if rows := uaScanFileCSV(t, sink.Bytes()); len(rows) != 0 {
			t.Fatalf("empty CSV has rows: %v", rows)
		}
	}
}

func TestUAScanSnapshotDoesNotPersistEndpointCredentials(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		snapshot := uaScanFileTestSnapshot()
		snapshot.Endpoint = "opc.tcp://private-user:private-password@host:4840/path?password=private-query#private-fragment"
		sink := &uaScanTestSink{}
		if err := writeUAScanSnapshotTo(context.Background(), "credentials."+format, format, snapshot, sink, uaScanFileLimit); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sink.String(), "private-") || strings.Contains(sink.String(), "password") || !strings.Contains(sink.String(), "opc.tcp://host:4840/path") {
			t.Fatalf("endpoint credentials leaked: %s", sink.String())
		}
	}
	if got := uaScanFileEndpoint("opc.tcp://user:secret@bad%host"); strings.Contains(got, "secret") {
		t.Fatal("invalid endpoint retained credentials")
	}
}

func TestUAScanJSONScalarSpecialFloats(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{math.NaN(), "NaN"}, {math.Inf(1), "+Inf"}, {math.Inf(-1), "-Inf"},
		{float32(math.NaN()), "NaN"}, {float32(math.Inf(1)), "+Inf"}, {float32(math.Inf(-1)), "-Inf"},
	} {
		b, err := json.Marshal(uaScanEncodedRow(uaScanRow{Value: tc.value, ValueType: tc.value, SourceTimestamp: tc.value, ServerTimestamp: tc.value}))
		if err != nil {
			t.Fatal(err)
		}
		row := uaScanFileJSON(t, b)
		for _, key := range []string{"value", "value_type", "source_timestamp", "server_timestamp"} {
			if row[key].(map[string]any)["$float"] != tc.want {
				t.Fatalf("%s = %v, want %s", key, row[key], tc.want)
			}
		}
	}
}

func TestUAScanRecorderExactByteLimit(t *testing.T) {
	row := uaScanFileTestRows()[0]
	measure := &uaScanTestSink{}
	r, err := newUAScanRecorderTo("measure.jsonl", "jsonl", "opc.tcp://host:4840", measure, 10, uaScanFileLimit)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write(row); err != nil {
		t.Fatal(err)
	}
	limit := r.Bytes()
	_ = r.Close()
	for _, allowance := range []int64{limit - 1, limit} {
		sink := &uaScanTestSink{}
		r, err := newUAScanRecorderTo("exact.jsonl", "jsonl", "opc.tcp://host:4840", sink, 10, allowance)
		if err != nil {
			t.Fatal(err)
		}
		err = r.Write(row)
		if allowance == limit {
			if err != nil || r.Bytes() != limit || r.Count() != 1 {
				t.Fatalf("exact byte boundary rejected: %v, bytes %d", err, r.Bytes())
			}
		} else if err == nil || r.Bytes() != 0 || r.Count() != 0 {
			t.Fatalf("over byte boundary accepted or partly written: %v", err)
		}
		_ = r.Close()
	}
}

func TestUAScanRecorderReportsCloseOnlyFailures(t *testing.T) {
	for _, operation := range []string{"sync", "close"} {
		t.Run(operation, func(t *testing.T) {
			failure := errors.New(operation + " failed")
			sink := &uaScanTestSink{}
			if operation == "sync" {
				sink.syncErr = failure
			} else {
				sink.closeErr = failure
			}
			r, err := newUAScanRecorderTo("close.jsonl", "jsonl", "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Write(uaScanRow{}); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); !errors.Is(err, failure) || !strings.Contains(err.Error(), "incomplete") {
				t.Fatalf("close-only error lost: %v", err)
			}
			if sink.closes != 1 || sink.syncs != 1 {
				t.Fatal("close did not attempt all operations")
			}
		})
	}
}

func TestUAScanRecorderPreservesLifecycleAndReadErrors(t *testing.T) {
	for _, format := range []string{"jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			sink := &uaScanTestSink{}
			r, err := newUAScanRecorderTo("lifecycle."+format, format, "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
			if err != nil {
				t.Fatal(err)
			}
			rows := []uaScanRow{
				{Kind: "session_start", Status: "Starting"},
				{Kind: "connected", Status: "Connected"},
				{Kind: "reconnecting", Status: "Reconnecting"},
				{Kind: "value", NodeID: "i=1", Status: "Unavailable", Error: "read timed out"},
				{Kind: "session_end", Status: "Stopped"},
			}
			for _, row := range rows {
				if err := r.Write(row); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if r.Count() != len(rows) {
				t.Fatalf("lifecycle rows not counted: %d", r.Count())
			}
			if format == "jsonl" {
				lines := bytes.Split(bytes.TrimSpace(sink.Bytes()), []byte("\n"))
				for i, expected := range rows {
					got := uaScanFileJSON(t, lines[i])
					if got["kind"] != expected.Kind || got["status"] != expected.Status || got["has_value"] != false || got["value"] != nil || got["source_timestamp"] != nil || got["server_timestamp"] != nil {
						t.Fatalf("non-data row changed: %v", got)
					}
					if expected.Error != "" && got["error"] != expected.Error {
						t.Fatalf("read error was lost: %v", got)
					}
					if expected.Error == "" {
						if _, present := got["error"]; present {
							t.Fatal("empty error should be omitted")
						}
					}
				}
			} else {
				got := uaScanFileCSV(t, sink.Bytes())
				for i, expected := range rows {
					if got[i]["kind"] != expected.Kind || got[i]["status"] != expected.Status || got[i]["has_value"] != "false" || got[i]["value"] != "null" || got[i]["source_timestamp"] != "null" || got[i]["server_timestamp"] != "null" || got[i]["error"] != expected.Error {
						t.Fatalf("non-data row changed: %v", got[i])
					}
				}
			}
		})
	}
}

func TestUAScanFilesPreserveNestedUAVariants(t *testing.T) {
	value := map[string]any{
		"variants":      []*ua.Variant{ua.MustVariant(int64(9007199254740993)), ua.MustVariant("sensor"), ua.MustVariant([][]float64{{math.NaN(), math.Inf(1)}, {3, 4}})},
		"variant_value": *ua.MustVariant(uint64(math.MaxUint64)),
		"data_value":    &ua.DataValue{EncodingMask: ua.DataValueValue | ua.DataValueStatusCode, Value: ua.MustVariant(math.NaN()), Status: ua.StatusBadNotReadable, SourcePicoseconds: 123, ServerPicoseconds: 456},
		"extension": &ua.ExtensionObject{EncodingMask: ua.ExtensionObjectBinary, TypeID: ua.NewNumericExpandedNodeID(2, 1001), Value: struct {
			Payload *ua.Variant
			Float   float64
		}{ua.MustVariant(uint64(math.MaxUint64)), math.Inf(-1)}},
		"nil_variant": (*ua.Variant)(nil),
	}
	for _, format := range []string{"json", "jsonl", "csv"} {
		t.Run(format, func(t *testing.T) {
			sink := &uaScanTestSink{}
			row := uaScanRow{HasValue: true, Value: value}
			if format == "json" {
				if err := writeUAScanSnapshotTo(context.Background(), "variant.json", format, uaScanSnapshot{Rows: []uaScanRow{row}}, sink, uaScanFileLimit); err != nil {
					t.Fatal(err)
				}
			} else {
				r, err := newUAScanRecorderTo("variant."+format, format, "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
				if err != nil {
					t.Fatal(err)
				}
				if err := r.Write(row); err != nil {
					t.Fatal(err)
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var got map[string]any
			if format == "csv" {
				got = uaScanFileJSON(t, []byte(uaScanFileCSV(t, sink.Bytes())[0]["value"]))
			} else {
				out := uaScanFileJSON(t, sink.Bytes())
				if format == "json" {
					out = out["rows"].([]any)[0].(map[string]any)
				}
				got = out["value"].(map[string]any)
			}
			variants := got["variants"].([]any)
			first := variants[0].(map[string]any)
			if first["value"] != json.Number("9007199254740993") || first["value_type"] != json.Number(fmt.Sprint(uint8(ua.TypeIDInt64))) || first["value_is_array"] != false || first["$type"] != "Variant" {
				t.Fatalf("Variant metadata/value lost: %v", first)
			}
			if variants[1].(map[string]any)["value"] != "sensor" {
				t.Fatalf("Variant string lost: %v", variants[1])
			}
			matrix := variants[2].(map[string]any)
			if matrix["value_is_array"] != true || !reflect.DeepEqual(matrix["array_dimensions"], []any{json.Number("2"), json.Number("2")}) {
				t.Fatalf("Variant dimensions lost: %v", matrix)
			}
			if matrix["value"].([]any)[0].([]any)[0].(map[string]any)["$float"] != "NaN" {
				t.Fatalf("Variant matrix nonfinite value lost: %v", matrix)
			}
			if got["variant_value"].(map[string]any)["value"] != json.Number("18446744073709551615") || got["nil_variant"] != nil {
				t.Fatalf("non-pointer or nil Variant changed: %v", got)
			}
			data := got["data_value"].(map[string]any)
			if data["Value"].(map[string]any)["value"].(map[string]any)["$float"] != "NaN" || data["Status"] != json.Number(fmt.Sprint(uint32(ua.StatusBadNotReadable))) || data["SourcePicoseconds"] != json.Number("123") || data["ServerPicoseconds"] != json.Number("456") {
				t.Fatalf("nested DataValue fields lost: %v", data)
			}
			ext := got["extension"].(map[string]any)
			payload := ext["Value"].(map[string]any)
			if payload["Payload"].(map[string]any)["value"] != json.Number("18446744073709551615") || payload["Float"].(map[string]any)["$float"] != "-Inf" {
				t.Fatalf("nested ExtensionObject lost: %v", ext)
			}
			if !strings.Contains(string(sink.Bytes()), "1001") {
				t.Fatal("ExtensionObject TypeID lost")
			}
		})
	}
}

func TestUAScanFilesRejectUndecodedOrOpaquePayloads(t *testing.T) {
	for _, value := range []any{
		&ua.ExtensionObject{EncodingMask: ua.ExtensionObjectBinary, TypeID: ua.NewNumericExpandedNodeID(2, 9999)},
		ua.ExtensionObject{EncodingMask: ua.ExtensionObjectXML, TypeID: ua.NewNumericExpandedNodeID(2, 9999), Value: (*ua.Variant)(nil)},
		struct{ hidden int }{42},
	} {
		for _, format := range []string{"jsonl", "csv"} {
			sink := &uaScanTestSink{}
			r, err := newUAScanRecorderTo("opaque."+format, format, "opc.tcp://host:4840", sink, 10, uaScanFileLimit)
			if err != nil {
				t.Fatal(err)
			}
			before := sink.String()
			if err := r.Write(uaScanRow{HasValue: true, Value: value}); err == nil || !strings.Contains(err.Error(), "unsupported") || !strings.Contains(err.Error(), "incomplete") {
				t.Fatalf("undecoded payload silently lost: %v", err)
			}
			if r.Count() != 0 || sink.String() != before {
				t.Fatal("unsupported payload was partly recorded")
			}
			_ = r.Close()
		}
	}
}
