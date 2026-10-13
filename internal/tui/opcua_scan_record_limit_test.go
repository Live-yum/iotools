package tui

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
)

// Injected engines and sinks exercise the recording boundary without connecting
// to any OPC UA endpoint, industrial equipment or production network.
func scanRecordLimitEvent(kind string, value int) engine.Event {
	if kind == "notification" {
		e := scanValue("ns=2;s=Temp", value)
		e.Kind = kind
		return e
	}
	return engine.Event{Kind: kind, Data: "simulated " + kind}
}

func scanRecordLimitRows(t *testing.T, sink *uaScanTestSink, format string) []uaScanRow {
	t.Helper()
	var rows []uaScanRow
	if format == "jsonl" {
		decoder := json.NewDecoder(strings.NewReader(sink.String()))
		for {
			var row uaScanRow
			if err := decoder.Decode(&row); err == io.EOF {
				return rows
			} else if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row)
		}
	}
	records, err := csv.NewReader(strings.NewReader(sink.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("missing CSV header")
	}
	columns := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		columns[name] = i
	}
	for _, record := range records[1:] {
		row := uaScanRow{Kind: record[columns["kind"]], Status: record[columns["status"]], HasValue: record[columns["has_value"]] == "true"}
		if value := record[columns["value"]]; value != "null" {
			row.Value = value
		}
		rows = append(rows, row)
	}
	return rows
}

func TestUAScanRecordRowLimitReservesTerminal(t *testing.T) {
	for _, format := range []string{"jsonl", "csv"} {
		for _, tc := range []struct {
			name       string
			maxRows    int
			kinds      []string
			wantValues int
		}{
			{"notifications", 5, []string{"connected", "notification", "notification", "notification"}, 2},
			{"extra lifecycle", 7, []string{"connected", "error", "reconnecting", "connected", "notification", "error"}, 1},
			{"only lifecycle", 5, []string{"connected", "error", "connected", "error"}, 0},
			{"no connected", 4, []string{"notification", "notification", "notification"}, 2},
			{"only start and end", 2, nil, 0},
		} {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				u, _, w := openScanUI(t)
				fillScan(w)
				w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
				w.apply(scanValue("ns=2;s=Temp", 999))
				startScanTestApp(t, u)
				sink := &uaScanTestSink{}
				calls := 0
				u.App.QueueUpdateDraw(func() {
					w.newRecorder = func(path, format, endpoint string) (*uaScanRecorder, error) {
						return newUAScanRecorderTo(path, format, endpoint, sink, tc.maxRows, uaScanFileLimit)
					}
					w.run = func(ctx context.Context, _ config.Request, _ bool, emit engine.Emit) error {
						calls++
						for i, kind := range tc.kinds {
							emit(scanRecordLimitEvent(kind, i))
						}
						if ctx.Err() == nil {
							return errors.New("row budget did not cancel the subscription")
						}
						// A queued batch may still deliver events after cancellation.
						for i := 0; i < 20; i++ {
							emit(scanRecordLimitEvent("notification", i))
							emit(scanRecordLimitEvent("connected", i))
						}
						return fmt.Errorf("subscription stopped: %w", ctx.Err())
					}
					w.subscribeSelected("injected-limit."+format, format)
				})
				waitScanIdle(t, u, w)
				u.App.QueueUpdateDraw(func() {
					if strings.Contains(w.status, "失败") || strings.Contains(w.status, "不完整") || !strings.Contains(w.status, fmt.Sprintf("已达%d行记录上限", tc.maxRows)) {
						t.Errorf("row budget was not a normal stop: %s", w.status)
					}
					if w.recorded != tc.maxRows || w.received != tc.wantValues {
						t.Errorf("recorded=%d received=%d, want %d and %d", w.recorded, w.received, tc.maxRows, tc.wantValues)
					}
				})
				if tc.maxRows == 2 && calls != 0 {
					t.Errorf("connected with no available event rows: %d calls", calls)
				}
				rows := scanRecordLimitRows(t, sink, format)
				if len(rows) != tc.maxRows {
					t.Fatalf("got %d rows, want %d", len(rows), tc.maxRows)
				}
				for i, row := range rows {
					if row.Kind != "notification" && (row.HasValue || row.Value != nil) {
						t.Errorf("lifecycle row %d reused an observed value: %+v", i, row)
					}
					if i > 0 && i < len(rows)-1 && row.Kind != tc.kinds[i-1] {
						t.Errorf("row %d kind=%s, want %s", i, row.Kind, tc.kinds[i-1])
					}
				}
				last := rows[len(rows)-1]
				if rows[0].Kind != "session_start" || last.Kind != "session_end" || !strings.Contains(last.Status, "正常结束") {
					t.Errorf("session boundaries missing or incomplete: %+v", rows)
				}
				if sink.syncs != 1 || sink.closes != 1 {
					t.Errorf("cleanup syncs=%d closes=%d", sink.syncs, sink.closes)
				}
			})
		}
	}
}

func TestUAScanRecordRowLimitUserCancellation(t *testing.T) {
	u, _, w := openScanUI(t)
	fillScan(w)
	w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	startScanTestApp(t, u)
	sink := &uaScanTestSink{}
	ready := make(chan struct{})
	u.App.QueueUpdateDraw(func() {
		w.newRecorder = func(path, format, endpoint string) (*uaScanRecorder, error) {
			return newUAScanRecorderTo(path, format, endpoint, sink, 8, uaScanFileLimit)
		}
		w.run = func(ctx context.Context, _ config.Request, _ bool, emit engine.Emit) error {
			emit(scanRecordLimitEvent("connected", 0))
			emit(scanRecordLimitEvent("notification", 1))
			close(ready)
			<-ctx.Done()
			return ctx.Err()
		}
		w.subscribeSelected("injected-cancel.jsonl", "jsonl")
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("subscription did not start")
	}
	u.App.QueueUpdateDraw(func() { w.stop() })
	waitScanIdle(t, u, w)
	u.App.QueueUpdateDraw(func() {
		if !strings.Contains(w.status, "已停止") || strings.Contains(w.status, "失败") || strings.Contains(w.status, "记录上限") {
			t.Errorf("user cancellation changed into a row limit or failure: %s", w.status)
		}
	})
	rows := scanRecordLimitRows(t, sink, "jsonl")
	if len(rows) != 4 || rows[3].Kind != "session_end" || rows[3].HasValue || rows[3].Value != nil || !strings.Contains(rows[3].Status, "context canceled") {
		t.Fatalf("cancellation did not retain a clean terminal row: %+v", rows)
	}
	if sink.syncs != 1 || sink.closes != 1 {
		t.Errorf("cancellation leaked sink: syncs=%d closes=%d", sink.syncs, sink.closes)
	}
}

func TestUAScanRecordRowLimitDoesNotHideFailures(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		prepare    func(*uaScanRecorder, *uaScanTestSink)
		runError   error
	}{
		{"terminal write", "disk write failed", func(_ *uaScanRecorder, sink *uaScanTestSink) { sink.writeErr = errors.New("disk write failed") }, nil},
		{"terminal short write", "short write", func(_ *uaScanRecorder, sink *uaScanTestSink) { sink.short = true }, nil},
		{"terminal byte limit", "byte limit", func(recorder *uaScanRecorder, _ *uaScanTestSink) { recorder.writer.counter.limit = recorder.Bytes() }, nil},
		{"sync", "disk sync failed", func(_ *uaScanRecorder, sink *uaScanTestSink) { sink.syncErr = errors.New("disk sync failed") }, nil},
		{"close", "file close failed", func(_ *uaScanRecorder, sink *uaScanTestSink) { sink.closeErr = errors.New("file close failed") }, nil},
		{"engine and cancel", "engine cleanup failed", func(_ *uaScanRecorder, _ *uaScanTestSink) {}, errors.New("engine cleanup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _, w := openScanUI(t)
			fillScan(w)
			w.key(0, tcell.NewEventKey(tcell.KeyRune, 'a', 0))
			startScanTestApp(t, u)
			sink := &uaScanTestSink{}
			var recorder *uaScanRecorder
			u.App.QueueUpdateDraw(func() {
				w.newRecorder = func(path, format, endpoint string) (_ *uaScanRecorder, err error) {
					recorder, err = newUAScanRecorderTo(path, format, endpoint, sink, 4, uaScanFileLimit)
					return recorder, err
				}
				w.run = func(ctx context.Context, _ config.Request, _ bool, emit engine.Emit) error {
					emit(scanRecordLimitEvent("connected", 0))
					emit(scanRecordLimitEvent("notification", 1))
					if ctx.Err() == nil {
						return errors.New("expected row-limit cancellation")
					}
					tc.prepare(recorder, sink)
					return errors.Join(ctx.Err(), tc.runError)
				}
				w.subscribeSelected("injected-failure.jsonl", "jsonl")
			})
			waitScanIdle(t, u, w)
			u.App.QueueUpdateDraw(func() {
				if !strings.Contains(w.status, "失败") || !strings.Contains(w.status, tc.want) || strings.Contains(w.status, "record limit") {
					t.Errorf("real error hidden by row-limit cancellation: %s", w.status)
				}
			})
			if recorder.Count() > 4 || sink.syncs != 1 || sink.closes != 1 {
				t.Errorf("failure overran or leaked sink: count=%d syncs=%d closes=%d", recorder.Count(), sink.syncs, sink.closes)
			}
		})
	}
}
