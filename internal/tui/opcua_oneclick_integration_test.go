package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

// This exercises real OPC UA discovery, sessions, Browse and Read against one
// disposable loopback-only server with four separate namespace roots. It is not
// an industrial-device, LAN or production integration test. Insecure anonymous
// access is explicitly enabled only for this disposable local test fixture.
func TestUAOneClickLoopbackFourRequestsExportCSVAndJSON(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.EndPoint("127.0.0.1", port), server.EnableAuthMode(ua.UserTokenTypeAnonymous), server.EnableSecurity("None", ua.MessageSecurityModeNone))
	endpoint := fmt.Sprintf("opc.tcp://127.0.0.1:%d", port)
	var targets []uaOneClickTarget
	var namespaces []*server.MapNamespace
	for i := 0; i < 4; i++ {
		ns := server.NewMapNamespace(srv, fmt.Sprintf("iotools-oneclick-loopback-%d", i+1))
		ns.Data["Temperature"] = float64(20+i) + 0.5
		ns.Data["Counter"] = int64(math.MaxInt64) - int64(i)
		ns.Data["Flags"] = []bool{true, false, true}
		ns.Data["Message"] = fmt.Sprintf("服务%d,\"quoted\"\nnext line", i+1)
		namespaces = append(namespaces, ns)
		root, _ := srv.Namespace(0)
		root.Objects().AddRef(ns.Objects(), id.HasComponent, true)
		r := config.Request{ID: fmt.Sprintf("loopback-%d", i+1), Protocol: "opcua", Action: "scan", Endpoint: endpoint, Timeout: "15s", Params: map[string]any{
			"node_id": ua.NewNumericNodeID(ns.ID(), id.ObjectsFolder).String(), "max_depth": 0, "max_nodes": 100, "read_batch_size": 2,
			"security_policy": "None", "security_mode": "None", "allow_insecure": true,
		}}
		target, err := uaOneClickTargetFor(r)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = srv.Close() })

	var calls []string
	run := func(ctx context.Context, r config.Request, write bool, emit engine.Emit) error {
		if write || r.Mutates() || (r.Action != "scan" && r.Action != "read") {
			t.Fatalf("one-click export attempted non-read-only action %q", r.Action)
		}
		if r.Endpoint != endpoint {
			t.Fatal("integration tried to leave its explicit loopback endpoint")
		}
		calls = append(calls, r.ID+":"+r.Action)
		return engine.Run(ctx, r, write, emit)
	}
	dir := oneClickTestDirectory(t)
	report, err := runUAOneClick(context.Background(), dir, targets, run, writeUAScanSnapshot, nil)
	if err != nil || report.Success != 4 || report.Partial != 0 || report.Failed != 0 || report.Cancelled || len(report.Services) != 4 || !report.NonAtomic {
		t.Fatalf("loopback batch did not succeed: report=%+v err=%v", report, err)
	}
	var expectedCalls []string
	for _, target := range targets {
		expectedCalls = append(expectedCalls, target.Request.ID+":scan", target.Request.ID+":read")
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("four explicit targets did not execute sequentially: %v", calls)
	}
	for i, result := range report.Services {
		if result.RequestID != targets[i].Request.ID || result.Outcome != "success" || result.Rows != 4 || result.Read != 4 || result.Unread != 0 || result.FailedValues != 0 || !result.ScanComplete || len(result.NamespaceFingerprint) != 64 {
			t.Fatalf("service %d incomplete: %+v", i+1, result)
		}
		oneClickTestAssertFiles(t, result, 4)
		csvRows := uaScanFileCSV(t, uaScanFileRead(t, result.CSV.Path))
		byNode := make(map[string]map[string]string)
		for _, row := range csvRows {
			byNode[row["node_id"]] = row
			if row["scan_complete"] != "true" || row["namespace_fingerprint"] != result.NamespaceFingerprint || row["has_value"] != "true" {
				t.Fatalf("CSV lost verified scan/value metadata: %+v", row)
			}
		}
		counterID := ua.NewStringNodeID(namespaces[i].ID(), "Counter").String()
		if byNode[counterID]["value"] != strconv.FormatInt(int64(math.MaxInt64)-int64(i), 10) {
			t.Fatalf("wire-to-CSV roundtrip lost Int64 precision: %+v", byNode[counterID])
		}
		jsonData := uaScanFileJSON(t, uaScanFileRead(t, result.JSON.Path))
		for _, raw := range jsonData["rows"].([]any) {
			row := raw.(map[string]any)
			if row["node_id"] == counterID && row["value"].(json.Number).String() != strconv.FormatInt(int64(math.MaxInt64)-int64(i), 10) {
				t.Fatalf("wire-to-JSON roundtrip lost Int64 precision: %+v", row)
			}
		}
		t.Logf("%s: real loopback scan/read; %d variables, CSV + JSON verified; root %s", result.RequestID, result.Rows, result.RootNode)
	}
	var saved uaOneClickReport
	if err := json.Unmarshal(uaScanFileRead(t, filepath.Join(dir, "summary.json")), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Success != 4 || saved.Partial != 0 || saved.Failed != 0 || len(saved.Services) != 4 || !saved.NonAtomic || saved.Directory != report.Directory {
		t.Fatalf("summary.json does not reflect the actual four-service result: %+v", saved)
	}
}
