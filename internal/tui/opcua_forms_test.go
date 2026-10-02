package tui

import (
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"testing"
)

func TestMethodFormCancelAndReadonly(t *testing.T) {
	u, _ := newTestUI(t)
	u.lastRequest = config.Request{Protocol: "opcua", Action: "method-arguments", Endpoint: "opc.tcp://127.0.0.1:1"}
	u.methodForm(map[string]any{"method_id": "i=123", "inputs": []engine.MethodArgument{{Name: "计数", Type: "UInt64"}}})
	_, p := u.pages.GetFrontPage()
	f, ok := p.(*tview.Form)
	if !ok || f.GetFormItemCount() != 2 {
		t.Fatal("missing typed method form")
	}
	f.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if u.pages.HasPage("ua-method") {
		t.Fatal("cancel failed")
	}
	u.readonly = true
	u.confirmDerived(config.Request{Protocol: "opcua", Action: "call"})
	if u.pages.HasPage("derived-confirm") || u.running {
		t.Fatal("readonly allowed method")
	}
	v := methodJSONNumbers([]any{json.Number("18446744073709551615")}).([]any)
	if v[0] != "18446744073709551615" {
		t.Fatal("integer precision lost")
	}
}

func TestSensitiveTemplatePreviewMasked(t *testing.T) {
	r := config.Request{Endpoint: "{{ sensitive ('private') }}", Params: map[string]any{"body": "{{sensitive('hidden')}}", "json": map[string]any{"ordinary": "visible"}}}
	masked := redactPreview(r)
	if masked.Endpoint == r.Endpoint || masked.Params["body"] == r.Params["body"] {
		t.Fatal("sensitive literal leaked")
	}
	if masked.Params["json"].(map[string]any)["ordinary"] != "visible" {
		t.Fatal("ordinary preview changed")
	}
}

func TestBackNeverReplaysMutation(t *testing.T) {
	u, _ := newTestUI(t)
	for _, r := range []config.Request{{Protocol: "opcua", Action: "write"}, {Protocol: "opcua", Action: "call"}, {Protocol: "kafka", Action: "delete-topic"}, {Protocol: "http", Action: "POST"}} {
		u.navigation = []config.Request{r}
		u.back()
		if u.running {
			t.Fatal("back replayed mutation", r)
		}
	}
}

func TestHeaderCredentialsMaskedInPreview(t *testing.T) {
	r := config.Request{Params: map[string]any{"headers": map[string]any{"Authorization": "PRIVATE", "X-API-Key": "PRIVATE", "Cookie": "PRIVATE", "Content-Type": "application/json"}}}
	preview := redactPreview(r)
	h := preview.Params["headers"].(map[string]any)
	for _, key := range []string{"Authorization", "X-API-Key", "Cookie"} {
		if h[key] == "PRIVATE" {
			t.Fatal("credential leaked", key)
		}
	}
	if h["Content-Type"] != "application/json" {
		t.Fatal("ordinary header hidden")
	}
}
