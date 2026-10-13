package mobileapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

func startScanFixture(s *Session, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runID = runID
	s.scanResult = &scanResult{runID: runID}
}

func TestScanTenThousandNodesSurviveQueueLossAndBoundedPages(t *testing.T) {
	advertised := false
	for _, op := range Catalog()["utilities"].([]string) {
		if op == "opcua.scan.page" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("scan pagination is missing from catalog utilities")
	}
	s := testSession(t)
	startScanFixture(s, "scan-one")
	s.emit("scan-one", "scan-namespace", map[string]any{"fingerprint": "exact-namespace"})
	for n := 0; n < 10000; n++ {
		s.emit("scan-one", "scan-node", map[string]any{"node_id": fmt.Sprintf("ns=2;s=Node%d", n), "parent_id": fmt.Sprintf("ns=2;s=Node%d", n/2), "display_name": "节点"})
	}
	s.emit("scan-one", "scan-summary", map[string]any{"nodes": 10000, "complete": true})
	if err := s.finishScan("scan-one", nil); err != nil {
		t.Fatal(err)
	}
	// Even the summary/done event can be evicted by background traffic.
	for n := 0; n < 600; n++ {
		s.emit("background", "notification", n)
	}
	batch := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	if len(batch["events"].([]any)) > maxQueueEvents || batch["dropped"].(float64) < 10000 {
		t.Fatal("event queue no longer bounded", batch["dropped"])
	}
	descriptor := batch["scan_result"].(map[string]any)
	if descriptor["node_count"] != float64(10000) || descriptor["complete"] != true {
		t.Fatal(descriptor)
	}
	for offset := 0; offset < 10000; {
		page := mustOK(t, s, map[string]any{"op": "opcua.scan.page", "run_id": "scan-one", "offset": offset, "limit": 137}).(map[string]any)
		rows := page["nodes"].([]any)
		if len(rows) == 0 || len(rows) > 137 {
			t.Fatal("page lost forward progress")
		}
		for i, item := range rows {
			n := offset + i
			r := item.(map[string]any)
			if r["node_id"] != fmt.Sprintf("ns=2;s=Node%d", n) || r["parent_id"] != fmt.Sprintf("ns=2;s=Node%d", n/2) {
				t.Fatalf("node/parent/order loss at %d: %v", n, r)
			}
		}
		offset = int(page["next_offset"].(float64))
	}
	for _, c := range []map[string]any{{"offset": -1}, {"offset": 10001}, {"limit": -1}, {"limit": 201}, {"run_id": "stale"}} {
		request := map[string]any{"op": "opcua.scan.page", "run_id": "scan-one"}
		for k, v := range c {
			request[k] = v
		}
		if cmd(t, s, request)["ok"] != false {
			t.Fatal("invalid cursor accepted", request)
		}
	}
}

func TestScanOverflowFailsExplicitlyAndSharesResultBudget(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(fmt.Sprint(oversized), func(t *testing.T) {
			s := testSession(t)
			startScanFixture(s, "bounded")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.cancel = cancel
			for i := 0; i < 4; i++ {
				s.emit("other", "response", map[string]any{"body": strings.Repeat("x", 7<<20)})
			}
			payload := strings.Repeat("x", 60<<10)
			if oversized {
				payload = strings.Repeat("x", maxEventBytes+1)
			}
			for i := 0; i < 400; i++ {
				s.emit("bounded", "scan-node", map[string]any{"node_id": fmt.Sprint(i), "display_name": payload})
				if s.scanResult.failure != "" {
					break
				}
			}
			if ctx.Err() == nil {
				t.Fatal("overflow did not cancel protocol work")
			}
			if err := s.finishScan("bounded", context.Canceled); err == nil || err == context.Canceled {
				t.Fatal("overflow hidden as cancellation", err)
			}
			if s.scanResult.bytes > maxScanResultBytes || s.resultBytes+s.scanResult.bytes > maxResultCacheBytes {
				t.Fatal("combined cache budget exceeded")
			}
			page := mustOK(t, s, map[string]any{"op": "opcua.scan.page", "run_id": "bounded", "limit": 200}).(map[string]any)
			descriptor := page["scan_result"].(map[string]any)
			if descriptor["complete"] != false || descriptor["finished"] != true || descriptor["error"] == "" {
				t.Fatal(descriptor)
			}
			raw, _ := json.Marshal(page)
			if len(raw) >= maxReplyBytes {
				t.Fatal("page exceeds reply budget")
			}
			if !oversized && len(page["nodes"].([]any)) >= 200 {
				t.Fatal("page byte bound ignored")
			}
		})
	}
}

func TestScanCancellationAndNewRunInvalidateCursors(t *testing.T) {
	s := testSession(t)
	startScanFixture(s, "partial")
	s.emit("partial", "scan-node", map[string]any{"node_id": "i=85"})
	if err := s.finishScan("partial", context.Canceled); err != context.Canceled {
		t.Fatal(err)
	}
	page := mustOK(t, s, map[string]any{"op": "opcua.scan.page", "run_id": "partial"}).(map[string]any)
	if page["scan_result"].(map[string]any)["complete"] != false {
		t.Fatal("cancelled scan claimed complete")
	}
	_, err := s.startTask("local-test", func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	done := s.runDone
	s.mu.Unlock()
	<-done
	if cmd(t, s, map[string]any{"op": "opcua.scan.page", "run_id": "partial"})["ok"] != false {
		t.Fatal("new run retained old cursor")
	}
	startScanFixture(s, "new")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			s.emit("new", "scan-node", map[string]any{"node_id": i})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.Command(`{"op":"opcua.scan.page","run_id":"new"}`)
		}
	}()
	s.Close()
	wg.Wait()
	if s.scanResult != nil || s.resultBytes != 0 {
		t.Fatal("close retained scan/cache")
	}
	if cmd(t, s, map[string]any{"op": "opcua.scan.page", "run_id": "new"})["ok"] != false {
		t.Fatal("closed scan still readable")
	}
}

// Real disposable loopback OPC UA server/session. This is not physical-device
// acceptance; it verifies engine callbacks and public mobile commands together.
func TestScanLoopbackPublicPagesWithoutPolling(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	srv := server.New(server.EndPoint("127.0.0.1", port), server.EnableAuthMode(ua.UserTokenTypeAnonymous), server.EnableSecurity("None", ua.MessageSecurityModeNone))
	ns := server.NewNodeNameSpace(srv, "mobile-scan-page-fixture")
	for i := 0; i < 600; i++ {
		node := ns.AddNewVariableStringNode(fmt.Sprintf("Node%d", i), int32(i))
		ns.Objects().AddRef(node, id.HasComponent, true)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	r := config.Request{ID: "scan", Protocol: "opcua", Action: "scan", Endpoint: fmt.Sprintf("opc.tcp://127.0.0.1:%d", port), Timeout: "20s", Params: map[string]any{"node_id": ns.Objects().ID().String(), "security_policy": "None", "security_mode": "None", "allow_insecure": true}}
	s := testSession(t, r)
	started := mustOK(t, s, map[string]any{"op": "run", "token": previewToken(t, s, "scan")}).(map[string]any)
	s.mu.Lock()
	done := s.runDone
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("loopback scan timed out")
	}
	batch := mustOK(t, s, map[string]any{"op": "events"}).(map[string]any)
	descriptor := batch["scan_result"].(map[string]any)
	if descriptor["complete"] != true || descriptor["node_count"] != float64(601) || batch["dropped"].(float64) == 0 {
		t.Fatal(descriptor, batch["dropped"])
	}
	count := 0
	for count < 601 {
		p := mustOK(t, s, map[string]any{"op": "opcua.scan.page", "run_id": started["run_id"], "offset": count, "limit": 200}).(map[string]any)
		count += len(p["nodes"].([]any))
	}
	if count != 601 {
		t.Fatal(count)
	}
}
