package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func rotationTargetFile(t *testing.T, dir string, modbus bool) string {
	t.Helper()
	path := filepath.Join(dir, "next.yaml")
	protocol, action, endpoint := "http", "GET", "https://example.invalid/private?token=hidden"
	if modbus {
		protocol, action, endpoint = "modbus", "read-input", "mock://local"
	}
	raw := "version: 1\nrequests:\n  - id: next\n    protocol: " + protocol + "\n    action: " + action + "\n    endpoint: " + endpoint + "\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestModbusRotationPreviewCancelSwitchAndOriginFallback(t *testing.T) {
	u, _, _ := modbusTestView(t)
	source := u.path
	before := append([]byte{}, u.raw...)
	target := rotationTargetFile(t, t.TempDir(), false)
	candidate, err := u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	u.modbusRotationPreview(candidate)
	_, p := u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	if strings.Contains(panel.GetItem(0).(*tview.TextView).GetText(false), "hidden") {
		t.Fatal("rotation summary exposed endpoint query")
	}
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if u.path != source || !bytes.Equal(u.raw, before) {
		t.Fatal("preview cancel changed source")
	}
	u.modbusRotationPreview(candidate)
	_, p = u.pages.GetFrontPage()
	panel = p.(*tview.Flex)
	modbusTestClick(panel.GetItem(2).(*tview.Form), 0)
	if u.path != target || u.running || !u.readonly || u.lastRequest.Protocol != "" || len(u.navigation) != 0 {
		t.Fatal("rotation unsafe state")
	}
	if u.modbusRotationTarget() != source {
		t.Fatalf("origin fallback: %s != %s", u.modbusRotationTarget(), source)
	}
	origin, err := u.modbusPrepareRotation(u.modbusRotationTarget())
	if err != nil {
		t.Fatal(err)
	}
	if err = u.modbusApplyRotation(origin, "local"); err != nil {
		t.Fatal(err)
	}
	if u.path != source || !bytes.Equal(before, u.raw) || u.running {
		t.Fatal("origin return lost source")
	}
}
func TestModbusRotationDirtySaveDiscardAndTargetChange(t *testing.T) {
	u, v, _ := modbusTestView(t)
	target := rotationTargetFile(t, t.TempDir(), true)
	v.modbus.columns = []string{"address", "time", "u64"}
	changes, err := v.modbusUnsavedLayout()
	if err != nil || len(changes) == 0 {
		t.Fatal("temporary layout not detected")
	}
	candidate, err := u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	u.modbusRotationPreview(candidate)
	_, p := u.pages.GetFrontPage()
	buttons := p.(*tview.Flex).GetItem(2).(*tview.Form)
	if buttons.GetButtonCount() != 3 {
		t.Fatal("dirty preview lacks save/discard/cancel")
	}
	modbusTestClick(buttons, 2)
	if len(v.modbus.columns) != 3 {
		t.Fatal("cancel discarded temporary layout")
	}
	// Mutating destination after review must never be accepted, including the save path.
	u.modbusRotationPreview(candidate)
	_, p = u.pages.GetFrontPage()
	buttons = p.(*tview.Flex).GetItem(2).(*tview.Form)
	if err := os.WriteFile(target, append(candidate.raw, []byte("\n# changed after preview\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	modbusTestClick(buttons, 0)
	if u.path == target {
		t.Fatal("save-and-switch silently accepted changed target")
	}
	saved, err := v.modbusSavedRequest()
	if err != nil || saved.Params["columns"] == nil {
		t.Fatal("explicit save part failed")
	}
	// A fresh reviewed snapshot may now switch, without any device run.
	candidate, err = u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = u.modbusApplyRotation(candidate, ""); err != nil {
		t.Fatal(err)
	}
	if u.running {
		t.Fatal("switch auto-connected")
	}
}
func TestModbusRotationExternalChangeIdentityAndBusy(t *testing.T) {
	u, _, _ := modbusTestView(t)
	target := rotationTargetFile(t, t.TempDir(), true)
	c, err := u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	u.running = true
	if err = u.modbusApplyRotation(c, ""); err == nil {
		t.Fatal("switched while running")
	}
	u.running = false
	u.localCancels = map[uint64]context.CancelFunc{1: func() {}}
	if _, err = u.modbusPrepareRotation(target); err == nil {
		t.Fatal("switched while local work active")
	}
	u.localCancels = nil
	replacement := target + ".replacement"
	if err = os.WriteFile(replacement, c.raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, target); err != nil {
		t.Fatal(err)
	}
	if err = u.modbusApplyRotation(c, ""); err == nil {
		t.Fatal("same bytes with different target identity accepted")
	}
	c, err = u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(u.path, append(c.sourceRaw, []byte("\n# source edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err = u.modbusApplyRotation(c, ""); err == nil {
		t.Fatal("source conflict silently discarded")
	}
}
func TestModbusRotationPathValidationAndRelativeResolution(t *testing.T) {
	u, v, _ := modbusTestView(t)
	target := rotationTargetFile(t, filepath.Dir(u.path), true)
	for _, path := range []string{"", "https://host/config.yaml", "file:///tmp/a", "http:bad", "${env:SECRET}", "{{foo}}", `\\server\file`, "//server/file", "bad\x01name", strings.Repeat("x", 4097)} {
		if err := modbusLocalConfigPath(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	candidate, err := u.modbusPrepareRotation("next.yaml")
	if err != nil || candidate.path != target {
		t.Fatal("relative path not relative to current collection", err)
	}
	if err = v.modbusSaveParams(map[string]any{"next_config": "next.yaml"}); err != nil {
		t.Fatal(err)
	}
	if u.modbusRotationTarget() != "next.yaml" {
		t.Fatal("saved next_config ignored")
	}
	bad := filepath.Join(t.TempDir(), "source.json")
	if err = os.WriteFile(bad, []byte(`{"device":{"interface":"mock"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = u.modbusPrepareRotation(bad); err == nil {
		t.Fatal("MTUI JSON silently activated")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "alias.yaml")
		if err = os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err = u.modbusPrepareRotation(link); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}

func TestModbusRotationDialogsFitSmallTerminal(t *testing.T) {
	u, screen := newTestUI(t)
	screen.SetSize(80, 24)
	for i, r := range u.collection.Requests {
		if r.Protocol == "modbus" && r.Action == "read-holding" {
			u.selected = i
			u.lastRequest = r
			u.inspector.reset(r)
			break
		}
	}
	check := func(form *tview.Form) {
		t.Helper()
		u.App.ForceDraw()
		for i := 0; i < form.GetButtonCount(); i++ {
			x, y, w, h := form.GetButton(i).GetRect()
			if w == 0 || h == 0 || x < 0 || x+w > 80 || y < 0 || y+h > 24 {
				t.Fatalf("button %d clipped: %d,%d %dx%d", i, x, y, w, h)
			}
		}
	}
	u.modbusRotationForm()
	_, p := u.pages.GetFrontPage()
	form := p.(*tview.Form)
	check(form)
	modbusTestClick(form, 2)
	target := rotationTargetFile(t, t.TempDir(), true)
	candidate, err := u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	u.inspector.modbus.columns = []string{"address", "time"}
	u.modbusRotationPreview(candidate)
	_, p = u.pages.GetFrontPage()
	panel := p.(*tview.Flex)
	check(panel.GetItem(2).(*tview.Form))
	panel.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	u.modbusActivityExport()
	_, p = u.pages.GetFrontPage()
	check(p.(*tview.Form))
}

func TestModbusRotationDiscardDoesNotPersistTemporaryLayout(t *testing.T) {
	u, v, _ := modbusTestView(t)
	source := u.path
	raw := append([]byte{}, u.raw...)
	v.modbus.columns = []string{"address", "time", "u64"}
	target := rotationTargetFile(t, t.TempDir(), true)
	c, err := u.modbusPrepareRotation(target)
	if err != nil {
		t.Fatal(err)
	}
	u.modbusRotationPreview(c)
	_, p := u.pages.GetFrontPage()
	modbusTestClick(p.(*tview.Flex).GetItem(2).(*tview.Form), 1)
	if u.path != target {
		t.Fatal("discard choice did not switch")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("discard saved temporary layout")
	}
}
