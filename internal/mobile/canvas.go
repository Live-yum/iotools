package mobile

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const MaxCanvasFrame = 4 << 20

type CanvasStyle struct {
	FG, BG, Attr   uint32
	Underline      uint8
	UnderlineColor uint32
}
type CanvasCell struct {
	Text  string
	Width uint8
	Style uint16
}
type CanvasFrame struct {
	Width, Height    int
	CursorX, CursorY int
	CursorVisible    bool
	Styles           []CanvasStyle
	Cells            []CanvasCell
}

// CanvasScreen is the actual screen backend for the Android native view. tcell's
// in-memory screen retains tview's cell/style/event semantics; no protocol or
// widget result is simulated. Publish happens on the UI draw thread, and JNI
// receives an immutable, bounded frame rather than an ANSI terminal stream.
type CanvasScreen struct {
	tcell.SimulationScreen
	width, height int
	mu            sync.Mutex
	frame         []byte
	pending       bool
	err           error
}

func NewCanvasScreen(w, h int) *CanvasScreen {
	return &CanvasScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8"), width: w, height: h}
}
func (s *CanvasScreen) Init() error {
	if err := s.SimulationScreen.Init(); err != nil {
		return err
	}
	s.SimulationScreen.SetSize(s.width, s.height)
	return nil
}
func (s *CanvasScreen) Show() { s.SimulationScreen.Show(); s.publish() }
func (s *CanvasScreen) Sync() { s.SimulationScreen.Sync(); s.publish() }
func (s *CanvasScreen) publish() {
	cells, w, h := s.SimulationScreen.GetContents()
	cx, cy, visible := s.SimulationScreen.GetCursor()
	frame := CanvasFrame{Width: w, Height: h, CursorX: cx, CursorY: cy, CursorVisible: visible, Cells: make([]CanvasCell, w*h)}
	palette := map[CanvasStyle]uint16{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			source := cells[y*w+x]
			glyph := string(source.Runes)
			if glyph == "" {
				glyph = " "
			}
			width := uniseg.StringWidth(glyph)
			if width < 1 {
				width = 1
			}
			if width > 2 {
				width = 2
			}
			if x+width > w {
				glyph = " "
				width = 1
			}
			fg, bg, attr := source.Style.Decompose()
			style := CanvasStyle{FG: canvasColor(fg, 0xffeeeeee), BG: canvasColor(bg, 0xff000000), Attr: uint32(attr), Underline: uint8(source.Style.GetUnderlineStyle()), UnderlineColor: canvasColor(source.Style.GetUnderlineColor(), 0)}
			index, ok := palette[style]
			if !ok {
				index = uint16(len(frame.Styles))
				palette[style] = index
				frame.Styles = append(frame.Styles, style)
			}
			frame.Cells[y*w+x] = CanvasCell{Text: glyph, Width: uint8(width), Style: index}
			if width == 2 {
				frame.Cells[y*w+x+1] = CanvasCell{Width: 0, Style: index}
				x++
			}
		}
	}
	encoded, err := EncodeCanvasFrame(frame)
	s.mu.Lock()
	s.err = err
	if err == nil {
		s.frame = encoded
		s.pending = true
	}
	s.mu.Unlock()
}
func canvasColor(c tcell.Color, fallback uint32) uint32 {
	r, g, b := c.RGB()
	if r < 0 || g < 0 || b < 0 {
		return fallback
	}
	return 0xff000000 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}
func (s *CanvasScreen) TakeFrame() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if !s.pending {
		return nil, nil
	}
	s.pending = false
	return s.frame, nil
}
func (s *CanvasScreen) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.frame...), s.err
}

// IOF1: dimensions/cursor (u16 LE), visibility (u8), palette count (u16),
// palettes (fg/bg/attr u32, underline u8/color u32), then row-major cells
// (palette u16, width u8, UTF-8 length u32, UTF-8 bytes). A zero-width cell is
// the continuation of the preceding double-width grapheme.
func EncodeCanvasFrame(f CanvasFrame) ([]byte, error) {
	if f.Width < 20 || f.Width > 300 || f.Height < 8 || f.Height > 150 || len(f.Cells) != f.Width*f.Height || len(f.Styles) == 0 || len(f.Styles) > 45000 {
		return nil, fmt.Errorf("原生屏幕尺寸或调色板无效")
	}
	var out bytes.Buffer
	out.WriteString("IOF1")
	put := func(v any) { _ = binary.Write(&out, binary.LittleEndian, v) }
	put(uint16(f.Width))
	put(uint16(f.Height))
	put(uint16(f.CursorX + 1))
	put(uint16(f.CursorY + 1))
	if f.CursorVisible {
		put(uint8(1))
	} else {
		put(uint8(0))
	}
	put(uint16(len(f.Styles)))
	for _, s := range f.Styles {
		put(s.FG)
		put(s.BG)
		put(s.Attr)
		put(s.Underline)
		put(s.UnderlineColor)
	}
	for _, c := range f.Cells {
		if int(c.Style) >= len(f.Styles) || c.Width > 2 || !utf8.ValidString(c.Text) || len(c.Text) > 65536 {
			return nil, fmt.Errorf("原生屏幕字符无效或过长")
		}
		put(c.Style)
		put(c.Width)
		put(uint32(len(c.Text)))
		out.WriteString(c.Text)
		if out.Len() > MaxCanvasFrame {
			return nil, fmt.Errorf("原生屏幕帧超过4MiB")
		}
	}
	return out.Bytes(), nil
}
func DecodeCanvasFrame(data []byte) (CanvasFrame, error) {
	var f CanvasFrame
	if len(data) > MaxCanvasFrame || len(data) < 15 || string(data[:4]) != "IOF1" {
		return f, fmt.Errorf("原生屏幕帧头无效")
	}
	r := bytes.NewReader(data[4:])
	get := func(v any) error { return binary.Read(r, binary.LittleEndian, v) }
	var w, h, cx, cy, count uint16
	var visible uint8
	for _, v := range []any{&w, &h, &cx, &cy, &visible, &count} {
		if e := get(v); e != nil {
			return f, e
		}
	}
	f.Width, f.Height, f.CursorX, f.CursorY, f.CursorVisible = int(w), int(h), int(cx)-1, int(cy)-1, visible == 1
	if w < 20 || w > 300 || h < 8 || h > 150 || count == 0 || count > 45000 || visible > 1 {
		return f, fmt.Errorf("原生屏幕尺寸无效")
	}
	f.Styles = make([]CanvasStyle, count)
	for n := range f.Styles {
		s := &f.Styles[n]
		for _, v := range []any{&s.FG, &s.BG, &s.Attr, &s.Underline, &s.UnderlineColor} {
			if e := get(v); e != nil {
				return f, e
			}
		}
	}
	f.Cells = make([]CanvasCell, int(w)*int(h))
	for n := range f.Cells {
		c := &f.Cells[n]
		var length uint32
		for _, v := range []any{&c.Style, &c.Width, &length} {
			if e := get(v); e != nil {
				return f, e
			}
		}
		if int(c.Style) >= len(f.Styles) || c.Width > 2 || length > 65536 || int(length) > r.Len() {
			return f, fmt.Errorf("原生屏幕字符字段无效")
		}
		buf := make([]byte, length)
		_, _ = r.Read(buf)
		if !utf8.Valid(buf) {
			return f, fmt.Errorf("原生屏幕UTF-8无效")
		}
		c.Text = string(buf)
	}
	if r.Len() != 0 {
		return f, fmt.Errorf("原生屏幕存在多余字节")
	}
	return f, nil
}
