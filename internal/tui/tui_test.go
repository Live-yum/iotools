package tui

import (
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
	updated := strings.Replace(original, "HTTP · inspect JSON", "HTTP · edited in TUI", 1)
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
