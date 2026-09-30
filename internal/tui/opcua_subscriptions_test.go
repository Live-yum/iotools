package tui

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/gdamore/tcell/v2"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUABackgroundSubscriptionDoesNotBlockReadsAndQuits(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	srv := server.New(server.EndPoint("127.0.0.1", port), server.EnableAuthMode(ua.UserTokenTypeAnonymous), server.EnableSecurity("None", ua.MessageSecurityModeNone))
	ns := server.NewMapNamespace(srv, "tui-loopback")
	ns.Data["Value"] = int32(21)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e = srv.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	u, _ := newTestUI(t)
	ready := make(chan struct{})
	var once sync.Once
	u.App.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("UI startup timeout")
	}
	endpoint := fmt.Sprintf("opc.tcp://127.0.0.1:%d", port)
	node := ua.NewStringNodeID(ns.ID(), "Value").String()
	r := config.Request{ID: "ua-live", Protocol: "opcua", Action: "read", Endpoint: endpoint, Params: map[string]any{"security_policy": "None", "security_mode": "None", "allow_insecure": true, "node_id": node, "interval_ms": 50}}
	u.App.QueueUpdateDraw(func() {
		u.lastRequest = r
		u.subscribeUA(node)
		if u.running {
			t.Error("background subscription occupied main execution state")
		}
		u.start(r)
	})
	deadline := time.Now().Add(5 * time.Second)
	both := false
	for time.Now().Before(deadline) {
		u.App.QueueUpdateDraw(func() {
			entry := u.uaSubscriptions[uaSubscriptionKey(r, node)]
			both = entry != nil && entry.Active && entry.Count > 0 && !u.running && strings.Contains(strings.Join(u.events, ""), "value")
		})
		if both {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !both {
		u.App.QueueUpdateDraw(func() { u.quit() })
		t.Fatal("simultaneous browse/read and notification not observed")
	}
	u.App.QueueUpdateDraw(func() {
		u.showUASubscriptions()
		if u.uaSubTable.GetRowCount() != 2 {
			t.Error("live panel omitted node")
		}
		u.quit()
	})
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quit did not cancel/close independent subscription")
	}
}
