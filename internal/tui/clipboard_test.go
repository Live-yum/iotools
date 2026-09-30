package tui

import (
	"bytes"
	"encoding/base64"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"strings"
	"testing"
)

func TestTerminalClipboardEncodingAndBound(t *testing.T) {
	text := "温度\x1b]52;c;evil\a"
	var b bytes.Buffer
	if e := writeTerminalClipboard(&b, text); e != nil {
		t.Fatal(e)
	}
	expected := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	if b.String() != expected {
		t.Fatal("terminal controls not encoded")
	}
	if e := writeTerminalClipboard(&b, strings.Repeat("x", (1<<20)+1)); e == nil {
		t.Fatal("oversized copy accepted")
	}
}
func TestClipboardPreviewCancelDoesNotWrite(t *testing.T) {
	u, _ := newTestUI(t)
	u.copyText("safe preview")
	if !u.pages.HasPage("clipboard") {
		t.Fatal("preview missing")
	}
	_, p := u.pages.GetFrontPage()
	p.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
}
