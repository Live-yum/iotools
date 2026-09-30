package tui

import (
	"encoding/json"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/sample"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestUI(t *testing.T) (*UI, tcell.SimulationScreen) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "iotools.yaml")
	if e := os.WriteFile(p, sample.Collection, 0600); e != nil {
		t.Fatal(e)
	}
	u, e := New(p, "local", true)
	if e != nil {
		t.Fatal(e)
	}
	s := tcell.NewSimulationScreen("UTF-8")
	u.App.SetScreen(s)
	s.SetSize(110, 38)
	t.Cleanup(s.Fini)
	return u, s
}
func snapshot(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := cells[y*w+x].Runes
			if len(r) > 0 {
				b.WriteRune(r[0])
			} else {
				b.WriteRune(' ')
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}
func TestUnifiedPanelsRenderAndResize(t *testing.T) {
	u, s := newTestUI(t)
	u.App.ForceDraw()
	text := snapshot(s)
	for _, want := range []string{"Collections", "Request", "Structured results", "READ ONLY", "http-get"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q\n%s", want, text)
		}
	}
	s.SetSize(60, 20)
	u.App.ForceDraw()
	if !strings.Contains(snapshot(s), "Collections") {
		t.Fatal("resize lost UI")
	}
	u.populate("mqtt")
	if u.list.GetItemCount() != 2 {
		t.Fatalf("MQTT filter: %d", u.list.GetItemCount())
	}
	u.App.ForceDraw()
}
func TestEditorCancelInvalidSaveAndReopen(t *testing.T) {
	u, _ := newTestUI(t)
	original := string(u.raw)
	u.edit()
	_, p := u.pages.GetFrontPage()
	editor := p.(*tview.TextArea)
	editor.SetText("version: 999", false)
	editor.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlS, 0, 0))
	if name, _ := u.pages.GetFrontPage(); name != "editor" {
		t.Fatal("invalid edit closed")
	}
	b, _ := os.ReadFile(u.path)
	if string(b) != original {
		t.Fatal("invalid editor changed disk")
	}
	editor.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	u.edit()
	_, p = u.pages.GetFrontPage()
	editor = p.(*tview.TextArea)
	if editor.GetText() != original {
		t.Fatal("cancel did not restore source")
	}
	updated := strings.Replace(original, "HTTP · 查看 JSON", "HTTP · 终端内编辑", 1)
	editor.SetText(updated, false)
	editor.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlS, 0, 0))
	b, _ = os.ReadFile(u.path)
	if string(b) != updated {
		t.Fatal("editor save failed")
	}
}
func TestReadOnlyWriteBlockedBeforeNetwork(t *testing.T) {
	u, _ := newTestUI(t)
	u.selected = 1
	u.execute()
	name, p := u.pages.GetFrontPage()
	if name != "modal" {
		t.Fatal("expected write blocked modal")
	}
	_ = p
	if u.running {
		t.Fatal("read-only write started")
	}
}
func TestTerminalEscapeSanitization(t *testing.T) {
	if strings.ContainsRune(clean("evil\x1b[2J\x00"), '\x1b') {
		t.Fatal("terminal escape preserved")
	}
}
func TestPreviewMasksSecretsWithoutChangingSource(t *testing.T) {
	r := config.Request{Params: map[string]any{"password": "PRIVATE-PASSWORD", "crypto": map[string]any{"local": map[string]any{"key": "PRIVATE-KEY", "iv": "PRIVATE-IV"}}}}
	masked := redactPreview(r)
	b, _ := json.Marshal(masked)
	if strings.Contains(string(b), "PRIVATE-") {
		t.Fatal("preview leaked a secret")
	}
	if r.Params["password"] != "PRIVATE-PASSWORD" {
		t.Fatal("redaction changed source")
	}
}
func TestRequestFormSaveAndCancel(t *testing.T) {
	u, _ := newTestUI(t)
	u.editRequest()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	form.GetFormItem(1).(*tview.InputField).SetText("表单内编辑名称")
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if u.collection.Requests[0].Name != "表单内编辑名称" {
		t.Fatal("form save failed")
	}
	u.editRequest()
	_, p = u.pages.GetFrontPage()
	form = p.(*tview.Form)
	form.GetFormItem(1).(*tview.InputField).SetText("不保存")
	form.GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if u.collection.Requests[0].Name == "不保存" {
		t.Fatal("cancel changed request")
	}
}

func TestPreviewMasksSecretsWithoutChangingEditorSource(t *testing.T) {
	r := config.Request{Protocol: "http", Params: map[string]any{"password": "secret-password", "crypto": map[string]any{"test": map[string]any{"key": "secret-key", "iv": "secret-iv"}}}}
	masked := redactPreview(r)
	b, _ := json.Marshal(masked)
	if strings.Contains(string(b), "secret-") {
		t.Fatal("preview secret leaked")
	}
	if r.Params["password"] != "secret-password" {
		t.Fatal("redaction modified source")
	}
}

func TestChineseHelpScrollAndClose(t *testing.T) {
	u, s := newTestUI(t)
	s.SetSize(60, 20)
	u.showHelp()
	u.App.ForceDraw()
	if !strings.Contains(snapshot(s), "IOTOOLS") {
		t.Fatal("Chinese help content not rendered")
	}
	_, p := u.pages.GetFrontPage()
	v := p.(*tview.TextView)
	v.ScrollToEnd()
	u.App.ForceDraw()
	v.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if page, _ := u.pages.GetFrontPage(); page != "main" {
		t.Fatal("help did not return to main")
	}
}
func TestYAMLEditorPreservesExternalChanges(t *testing.T) {
	u, _ := newTestUI(t)
	u.edit()
	_, p := u.pages.GetFrontPage()
	editor := p.(*tview.TextArea)
	changed := append([]byte("# external change\n"), u.raw...)
	if e := os.WriteFile(u.path, changed, 0600); e != nil {
		t.Fatal(e)
	}
	editor.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlS, 0, 0))
	b, _ := os.ReadFile(u.path)
	if string(b) != string(changed) {
		t.Fatal("editor overwrote concurrent external change")
	}
}
