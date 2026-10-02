package tui

import (
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"os"
	"strings"
	"testing"
)

func TestUAHistoryPersistsSelectionNotPassword(t *testing.T) {
	u, _ := newTestUI(t)
	r := config.Request{ID: "ua", Protocol: "opcua", Action: "browse", Endpoint: "opc.tcp://127.0.0.1:4840", Params: map[string]any{"node_id": "ns=2;s=温度", "auth": "username", "username": "operator", "password": "NEVER_SAVE_PASSWORD", "cert_file": "client.pem", "token": "NEVER_SAVE_TOKEN"}}
	u.rememberUAConnection(r)
	b, e := os.ReadFile(uaHistoryPath(u.path))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "NEVER_SAVE") {
		t.Fatal("secret persisted")
	}
	h, e := loadUAHistory(uaHistoryPath(u.path))
	if e != nil || len(h.Records) != 1 || h.Records[0].NodeID != "ns=2;s=温度" || h.Records[0].Preferences["username"] != "operator" {
		t.Fatalf("%+v %v", h, e)
	}
	r.Params["node_id"] = "i=85"
	u.rememberUAConnection(r)
	h, e = loadUAHistory(uaHistoryPath(u.path))
	if e != nil || len(h.Records) != 1 || h.Records[0].NodeID != "i=85" {
		t.Fatal("selection not replaced")
	}
	r.Action = "write"
	r.Params["node_id"] = "i=999"
	u.rememberUAConnection(r)
	h, _ = loadUAHistory(uaHistoryPath(u.path))
	if h.Records[0].NodeID != "i=85" {
		t.Fatal("mutation history replay risk")
	}
	if e := os.WriteFile(uaHistoryPath(u.path), []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	r.Action = "browse"
	u.rememberUAConnection(r)
	b, _ = os.ReadFile(uaHistoryPath(u.path))
	if string(b) != "invalid" {
		t.Fatal("corrupt state silently overwritten")
	}
}
func TestUAEndpointSelectionNeverTrustsDiscoveredPin(t *testing.T) {
	u, _ := newTestUI(t)
	u.lastRequest = config.Request{ID: "ua", Protocol: "opcua", Action: "discover", Endpoint: "opc.tcp://127.0.0.1:4840"}
	u.inspector.reset(u.lastRequest)
	data := map[string]any{"url": u.lastRequest.Endpoint, "security_policy": "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256", "security_mode": "SignAndEncrypt", "certificate_sha256": "UNTRUSTED", "identity_tokens": []string{"Anonymous"}}
	u.inspector.add(engine.Event{Kind: "endpoint", Data: data})
	if u.inspector.table.GetRowCount() != 2 {
		t.Fatal("endpoint table missing")
	}
	u.uaConnectionForm(u.lastRequest, data)
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	if form.GetFormItem(8).(*tview.InputField).GetText() != "" {
		t.Fatal("discovered fingerprint auto-trusted")
	}
	if u.running {
		t.Fatal("opening picker connected automatically")
	}
	form.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	if u.pages.HasPage("ua-connect") {
		t.Fatal("cancel failed")
	}
}
