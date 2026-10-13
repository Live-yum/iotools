package tui

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gopcua/opcua/ua"
)

func TestUAOneClickMemoryBudgetStopsAccumulationAndContinuesNextTarget(t *testing.T) {
	// Every event aliases one small fixture string. The old implementation can
	// fail this regression without allocating gigabytes or risking a real OOM.
	value := strings.Repeat("x", 1<<20)
	largeTarget, ids := oneClickLargeTestSelection(t, 10001)
	var stopped, healthy bool
	var csv, jsonWrites, reads int
	run := func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
		if r.ID == "healthy" {
			healthy = true
			return oneClickTestGoodRunner(t)(ctx, r, false, emit)
		}
		if r.Action == "scan" {
			oneClickTestScan(emit, true, "", ids...)
			return nil
		}
		reads++
		for _, id := range r.Strings("node_ids") {
			emit(scanValue(id, value))
			if ctx.Err() != nil {
				stopped = true
				// Even a misbehaving emitter cannot retain more after rejection.
				emit(scanValue(ids[len(ids)-1], int64(77)))
				break
			}
		}
		return errors.Join(&engine.OPCUAReadPartialError{Err: errors.New("earlier read failure remains visible")}, ctx.Err())
	}
	writer := func(ctx context.Context, _ string, format string, snapshot uaScanSnapshot) error {
		if ctx.Err() != nil {
			t.Error("network cancellation consumed file persistence context")
		}
		if format == "csv" {
			csv++
		} else {
			jsonWrites++
		}
		if len(snapshot.Rows) != len(ids) {
			return nil // the independent healthy target
		}
		if snapshot.Rows[0].Value != value || !snapshot.Rows[0].HasValue {
			t.Error("earlier accepted observation was discarded")
		}
		if unsafe.StringData(snapshot.Rows[0].Value.(string)) == unsafe.StringData(value) {
			t.Error("snapshot retained response-backed string storage")
		}
		last := snapshot.Rows[len(ids)-1]
		if last.HasValue || last.Value != nil || !strings.Contains(last.Error, "memory budget") {
			t.Errorf("late/rejected value was retained or silently omitted: has_value=%t kind=%q", last.HasValue, last.Kind)
		}
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{largeTarget, oneClickTestTarget(t, "healthy")}, run, writer, nil)
	if err != nil || !stopped || !healthy || reads != 1 || csv != 2 || jsonWrites != 2 || report.Partial != 1 || report.Success != 1 || report.Cancelled {
		t.Fatalf("memory stop was not isolated/persisted: stopped=%t healthy=%t files=%d/%d report=%+v err=%v", stopped, healthy, csv, jsonWrites, report, err)
	}
	result := report.Services[0]
	if result.Read == 0 || result.Read >= len(ids) || result.Unread == 0 || !strings.Contains(result.Reason, "memory budget") || !strings.Contains(result.Reason, "earlier read failure") {
		t.Fatalf("partial reason/counts incomplete: %+v", result)
	}
}

func TestUAOneClickMemoryRejectsSingleOversizedValueOrMetadataBeforeCopy(t *testing.T) {
	oversized := strings.Repeat("x", int(uaOneClickMemoryLimit/32)+1)
	for _, location := range []string{"value", "metadata", "scan-reason"} {
		t.Run(location, func(t *testing.T) {
			stopped, readCalls := false, 0
			run := func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
				if r.Action == "scan" {
					oneClickTestScan(emit, true, "", "ns=2;s=v")
					if location == "metadata" {
						e := scanNode("ns=2;s=too-large", "i=85", "Variable", 1)
						e.Data.(map[string]any)["display_name"] = oversized
						emit(e)
					} else if location == "scan-reason" {
						emit(engine.Event{Kind: "scan-summary", Data: map[string]any{"complete": false, "reason": oversized}})
					}
				} else {
					readCalls++
					emit(scanValue("ns=2;s=v", oversized))
				}
				stopped = stopped || ctx.Err() != nil
				return ctx.Err()
			}
			write := func(_ context.Context, _ string, _ string, s uaScanSnapshot) error {
				if len(s.Rows) != 1 || s.Rows[0].HasValue || s.Rows[0].Value != nil {
					t.Fatal("oversized input entered the owned snapshot")
				}
				return nil
			}
			report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "oversized")}, run, write, nil)
			if err != nil || !stopped || report.Partial != 1 || !strings.Contains(report.Services[0].Reason, "memory budget") {
				t.Fatalf("single input not explicitly rejected: stopped=%t partial=%d err=%v", stopped, report.Partial, err)
			}
			if location != "value" && readCalls != 0 {
				t.Fatal("scan memory failure still started a read")
			}
		})
	}
}

func TestUAOneClickMemoryPreflightBoundaryAndOwnedStorage(t *testing.T) {
	backing := []byte(strings.Repeat("payload", 100))
	text := strings.Repeat("substring", 100)
	event := scanValue("ns=2;s=v", map[string]any{"bytes": backing[300:305], "text": text[200:205], "matrix": [][]int64{{1, 2}, {3, 9007199254740993}}})
	probe := uaOneClickMemory{remaining: uaOneClickMemoryLimit}
	if _, err := probe.ownEvent(event); err != nil {
		t.Fatal(err)
	}
	needed := uaOneClickMemoryLimit - probe.remaining
	for _, delta := range []int64{-1, 0, 1} {
		budget := uaOneClickMemory{remaining: needed + delta}
		owned, err := budget.ownEvent(event)
		if delta < 0 {
			if !errors.Is(err, errUAOneClickMemoryLimit) || budget.remaining != needed+delta {
				t.Fatal("over-limit preflight allocated/committed a partial admission", err)
			}
			continue
		}
		if err != nil || budget.remaining != delta {
			t.Fatal("exact reservation boundary incorrect", err, budget.remaining)
		}
		value := owned.Data.(map[string]any)["value"].(map[string]any)
		bytes := value["bytes"].([]byte)
		if cap(bytes) != len(bytes) || &bytes[0] == &backing[300] || unsafe.StringData(value["text"].(string)) == unsafe.StringData(text[200:205]) {
			t.Fatal("owned data pins original backing storage")
		}
		backing[300]++
		if bytes[0] == backing[300] {
			t.Fatal("mutation of response changed owned observation")
		}
		backing[300]--
	}
}

func TestUAOneClickMemoryRejectedReplacementRetainsAndLabelsPriorValue(t *testing.T) {
	oversized := make([]byte, int(uaOneClickMemoryLimit/32)+1)
	run := func(ctx context.Context, r config.Request, _ bool, emit engine.Emit) error {
		if r.Action == "scan" {
			oneClickTestScan(emit, true, "", "ns=2;s=v")
			return nil
		}
		emit(scanValue("ns=2;s=v", int64(9007199254740993)))
		emit(scanValue("ns=2;s=v", oversized))
		return ctx.Err()
	}
	writer := func(ctx context.Context, _ string, _ string, snapshot uaScanSnapshot) error {
		row := snapshot.Rows[0]
		if ctx.Err() != nil || row.Value != int64(9007199254740993) || !row.HasValue || !strings.Contains(row.Error, "memory budget") {
			t.Fatal("rejected replacement discarded/unlabelled the prior observation")
		}
		return nil
	}
	report, err := runUAOneClick(context.Background(), oneClickTestDirectory(t), []uaOneClickTarget{oneClickTestTarget(t, "replace")}, run, writer, nil)
	if err != nil || report.Partial != 1 || report.Services[0].Read != 1 || report.Services[0].FailedValues != 1 {
		t.Fatalf("replacement failure was silently counted as success: %+v %v", report, err)
	}
}

type uaOneClickExplosiveJSON struct{ called *bool }

func (v uaOneClickExplosiveJSON) MarshalJSON() ([]byte, error) {
	*v.called = true
	return nil, errors.New("must not run before admission")
}

type uaOneClickExplosiveKey int

func (uaOneClickExplosiveKey) MarshalText() ([]byte, error) {
	panic("unbounded key marshaler must never run")
}

func TestUAOneClickMemoryRejectsCyclesAndUnboundedMarshalers(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	called := false
	for _, value := range []any{cycle, uaOneClickExplosiveJSON{&called}, map[uaOneClickExplosiveKey]string{1: "key"}} {
		budget := uaOneClickMemory{remaining: uaOneClickMemoryLimit}
		if _, err := budget.ownEvent(scanValue("ns=2;s=v", value)); err == nil {
			t.Fatalf("unbounded value accepted: %T", value)
		}
	}
	if called {
		t.Fatal("custom marshaler ran during size preflight")
	}
}

func TestUAOneClickMemoryCountsBytesNestedArraysAndPrivateUAPayloads(t *testing.T) {
	large := make([]byte, int(uaOneClickMemoryLimit/32)+1)
	shared := strings.Repeat("x", 1<<20)
	stringsArray := make([]string, 9)
	for i := range stringsArray {
		stringsArray[i] = shared
	}
	for _, value := range []any{
		large,
		[][][]string{{stringsArray}},
		ua.MustVariant(stringsArray),
		&ua.DataValue{Value: ua.MustVariant(large)},
		&ua.ExtensionObject{EncodingMask: ua.ExtensionObjectBinary, Value: struct{ Payload []byte }{large}},
		ua.NewByteStringNodeID(2, large),
	} {
		t.Run(reflect.TypeOf(value).String(), func(t *testing.T) {
			budget := uaOneClickMemory{remaining: uaOneClickMemoryLimit}
			if _, err := budget.ownEvent(scanValue("ns=2;s=v", value)); !errors.Is(err, errUAOneClickMemoryLimit) {
				t.Fatalf("oversized graph was not rejected before normalization: %v", err)
			}
			if budget.remaining != uaOneClickMemoryLimit {
				t.Fatal("rejected graph committed a partial reservation")
			}
		})
	}
}

func TestUAOneClickMemoryNodeIDOwnsPrivateByteStorage(t *testing.T) {
	backing := make([]byte, 4096)
	backing[3000] = 123
	node := ua.NewByteStringNodeID(2, backing[3000:3001])
	budget := uaOneClickMemory{remaining: uaOneClickMemoryLimit}
	owned, err := budget.ownEvent(scanValue("ns=2;s=v", node))
	if err != nil {
		t.Fatal(err)
	}
	copy := owned.Data.(map[string]any)["value"].(*ua.NodeID)
	want := copy.String()
	backing[3000] = 42
	if copy == node || copy.String() != want || copy.String() == node.String() {
		t.Fatal("owned NodeID retained private response-backed bytes")
	}
}

type uaOneClickExplosiveStringer int

func (uaOneClickExplosiveStringer) String() string { panic("Stringer must not run before admission") }

func TestUAOneClickMemoryRejectsMetadataBeforeFormatting(t *testing.T) {
	for _, key := range []string{"node_id", "depth", "status"} {
		t.Run(key, func(t *testing.T) {
			budget := uaOneClickMemory{remaining: uaOneClickMemoryLimit}
			event := scanNode("ns=2;s=v", "i=85", "Variable", 1)
			event.Data.(map[string]any)[key] = uaOneClickExplosiveStringer(1)
			if _, err := budget.ownEvent(event); err == nil {
				t.Fatal("unbounded metadata formatter accepted")
			}
		})
	}
}

func TestUAOneClickMemoryPreservesComplexValueFileEncoding(t *testing.T) {
	value := map[string]any{
		"variants":  []*ua.Variant{ua.MustVariant(int64(9007199254740993)), ua.MustVariant([][]float64{{math.NaN(), math.Inf(1)}, {3, 4}})},
		"data":      &ua.DataValue{Value: ua.MustVariant(uint64(math.MaxUint64)), SourceTimestamp: time.Date(2026, 10, 13, 1, 2, 3, 4, time.FixedZone("private-location", 7200))},
		"extension": &ua.ExtensionObject{EncodingMask: ua.ExtensionObjectBinary, TypeID: ua.NewNumericExpandedNodeID(2, 1001), Value: struct{ Payload *ua.Variant }{ua.MustVariant([]byte{1, 2, 3})}},
		"node":      ua.NewByteStringNodeID(2, []byte{4, 5, 6}),
	}
	event := scanValue("ns=2;s=v", value)
	budget := uaOneClickMemory{remaining: uaOneClickMemoryLimit}
	owned, err := budget.ownEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	before, after := uaScanValueRow(uaScanRow{}, event), uaScanValueRow(uaScanRow{}, owned)
	for _, encode := range []func(uaScanRow) ([]byte, error){func(row uaScanRow) ([]byte, error) { return json.Marshal(uaScanEncodedRow(row)) }, func(row uaScanRow) ([]byte, error) { return uaScanCSVRecord(uaScanSnapshot{}, row) }} {
		want, err := encode(before)
		if err != nil {
			t.Fatal(err)
		}
		got, err := encode(after)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("owned value changed lossless file representation:\nwant %s\ngot %s\nerr %v", want, got, err)
		}
	}
}
