package mobile

import (
	"bytes"
	"encoding/binary"
	"github.com/gdamore/tcell/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCanvasCodecGraphemesStylesCursorAndBounds(t *testing.T) {
	s := NewCanvasScreen(80, 24)
	if e := s.Init(); e != nil {
		t.Fatal(e)
	}
	defer s.Fini()
	style := tcell.StyleDefault.Foreground(tcell.NewRGBColor(10, 20, 30)).Background(tcell.ColorNavy).Bold(true).Reverse(true).Underline(tcell.UnderlineStyleDouble, tcell.ColorYellow)
	s.SetContent(0, 0, '客', nil, style)
	s.SetContent(2, 0, 'e', []rune{0x301}, style)
	s.SetContent(3, 0, '👩', []rune{0x200d, '💻'}, style)
	s.ShowCursor(3, 0)
	s.Show()
	b, e := s.TakeFrame()
	if e != nil {
		t.Fatal(e)
	}
	f, e := DecodeCanvasFrame(b)
	if e != nil {
		t.Fatal(e)
	}
	if f.Cells[0].Text != "客" || f.Cells[0].Width != 2 || f.Cells[1].Width != 0 || f.Cells[2].Text != "é" || f.Cells[2].Width != 1 || f.Cells[3].Text != "👩‍💻" || f.Cells[3].Width != 2 {
		t.Fatalf("lost graphemes: %+v", f.Cells[:5])
	}
	p := f.Styles[f.Cells[0].Style]
	if p.FG != 0xff0a141e || p.Attr&uint32(tcell.AttrReverse) == 0 || p.Attr&uint32(tcell.AttrBold) == 0 || p.Underline != uint8(tcell.UnderlineStyleDouble) {
		t.Fatalf("style lost: %+v", p)
	}
	if !f.CursorVisible || f.CursorX != 3 || f.CursorY != 0 {
		t.Fatalf("cursor lost: %+v", f)
	}
	if repeat, _ := s.TakeFrame(); len(repeat) != 0 {
		t.Fatal("same frame emitted twice")
	}
	for _, bad := range [][]byte{nil, b[:12], append(append([]byte{}, b...), 1), bytes.Repeat([]byte{1}, MaxCanvasFrame+1)} {
		if _, e := DecodeCanvasFrame(bad); e == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	wrong := append([]byte{}, b...)
	binary.LittleEndian.PutUint16(wrong[4:], 301)
	if _, e := DecodeCanvasFrame(wrong); e == nil {
		t.Fatal("oversize dimensions accepted")
	}
}
func waitCanvas(t *testing.T, s *Session, text string) CanvasFrame {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		b, e := s.Canvas.Snapshot()
		if e != nil {
			t.Fatal(e)
		}
		if len(b) > 0 {
			f, e := DecodeCanvasFrame(b)
			if e != nil {
				t.Fatal(e)
			}
			var out strings.Builder
			for _, c := range f.Cells {
				out.WriteString(c.Text)
			}
			if strings.Contains(out.String(), text) {
				return f
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := s.Canvas.Snapshot()
	f, _ := DecodeCanvasFrame(b)
	var out strings.Builder
	for _, c := range f.Cells {
		out.WriteString(c.Text)
	}
	t.Fatalf("canvas text missing: %s; final=%s", text, out.String())
	return CanvasFrame{}
}
func TestCanvasRealHTTPChineseEditorPauseAndAtomicSave(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"客服":"原生屏幕协议成功"}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "iotools.yaml")
	yaml := "version: 1\nprofiles:\n  local: {}\nrequests:\n  - id: customer\n    name: 客服验收\n    protocol: http\n    action: GET\n    endpoint: " + server.URL + "\n"
	if e := os.WriteFile(path, []byte(yaml), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := StartCanvas(path, "native", 80, 24, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	waitCanvas(t, s, "客服验收")
	if requests.Load() != 0 {
		t.Fatal("startup network request")
	}
	if e = s.CanvasInput([]byte("\r"), false); e != nil {
		t.Fatal(e)
	}
	waitCanvas(t, s, "原生屏幕协议成功")
	if requests.Load() != 1 {
		t.Fatal("HTTP not called once")
	}
	if e = s.CanvasInput([]byte("\x1bOS"), false); e != nil {
		t.Fatal(e)
	}
	waitCanvas(t, s, "请求配置 YAML")
	if e = s.CanvasInput([]byte("# 客服备注 😀\n"), true); e != nil {
		t.Fatal(e)
	}
	waitCanvas(t, s, "客服备注")
	s.Pause()
	s.Resume()
	waitCanvas(t, s, "客服备注")
	if requests.Load() != 1 {
		t.Fatal("resume replayed HTTP")
	}
	if e = s.CanvasInput([]byte{19}, false); e != nil {
		t.Fatal(e)
	}
	waitCanvas(t, s, "配置已保存")
	saved, e := os.ReadFile(path)
	if e != nil || !bytes.Contains(saved, []byte("客服备注 😀")) {
		t.Fatalf("saved UTF8 lost: %s %v", saved, e)
	}
	if e = s.CanvasResize(100, 30); e != nil {
		t.Fatal(e)
	}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		b, _ := s.Canvas.Snapshot()
		f, _ := DecodeCanvasFrame(b)
		if f.Width == 100 && f.Height == 30 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s.Stop()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("native screen did not stop")
	}
}
func TestCanvasStopDuringLargePasteAndImmediateStart(t *testing.T) {
	for n := 0; n < 5; n++ {
		s, e := StartCanvas(filepath.Join(t.TempDir(), "iotools.yaml"), "native", 80, 24, Options{})
		if e != nil {
			t.Fatal(e)
		}
		_ = s.CanvasInput([]byte(strings.Repeat("x", 65536)), true)
		s.Stop()
		select {
		case <-s.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("stop blocked during paste")
		}
	}
}
