package mobile

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

type canvasPacket struct {
	text  string
	paste bool
	event tcell.Event
	size  int
}

func (s *Session) queueCanvas(packet canvasPacket) error {
	if s.Canvas == nil {
		return fmt.Errorf("当前不是原生屏幕会话")
	}
	if packet.size < 1 {
		packet.size = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("终端已停止")
	}
	if s.inputBytes+packet.size > 65536 {
		return fmt.Errorf("输入缓冲最多64KiB")
	}
	select {
	case s.input <- packet:
		s.inputBytes += packet.size
		return nil
	default:
		return fmt.Errorf("输入队列已满")
	}
}
func (s *Session) CanvasInput(data []byte, paste bool) error {
	if len(data) > 65536 || !utf8.Valid(data) {
		return fmt.Errorf("输入必须是64KiB以内UTF-8")
	}
	return s.queueCanvas(canvasPacket{text: string(data), paste: paste, size: len(data)})
}
func (s *Session) CanvasKey(key tcell.Key, r rune, mods tcell.ModMask) error {
	return s.queueCanvas(canvasPacket{event: tcell.NewEventKey(key, r, mods)})
}
func (s *Session) CanvasMouse(x, y int, pressed bool) error {
	if x < 0 || x >= 300 || y < 0 || y >= 150 {
		return fmt.Errorf("触屏位置越界")
	}
	buttons := tcell.ButtonNone
	if pressed {
		buttons = tcell.Button1
	}
	return s.queueCanvas(canvasPacket{event: tcell.NewEventMouse(x, y, buttons, 0)})
}
func (s *Session) CanvasResize(w, h int) error {
	if w < 20 || w > 300 || h < 8 || h > 150 {
		return fmt.Errorf("终端尺寸范围20..300列、8..150行")
	}
	s.control(func(screen tcell.Screen) { s.Canvas.SetSize(w, h); _ = screen.PostEvent(tcell.NewEventResize(w, h)) })
	return nil
}
func (s *Session) canvasInputLoop() {
	post := func(event tcell.Event) bool {
		for {
			select {
			case <-s.done:
				return false
			default:
			}
			if s.screen.PostEvent(event) == nil {
				return true
			}
			select {
			case <-s.done:
				return false
			case <-time.After(time.Millisecond):
			}
		}
	}
	for {
		select {
		case <-s.done:
			return
		case packet := <-s.input:
			ok := true
			if packet.event != nil {
				ok = post(packet.event)
			} else {
				if packet.paste {
					ok = post(tcell.NewEventPaste(true))
				}
				if key, known := canvasKeys[packet.text]; known && !packet.paste {
					ok = post(tcell.NewEventKey(key, 0, 0))
				} else {
					for _, r := range packet.text {
						if !ok {
							break
						}
						key := tcell.KeyRune
						if r == '\r' || r == '\n' {
							key = tcell.KeyEnter
							r = 0
						} else if r == '\t' {
							key = tcell.KeyTab
							r = 0
						} else if r == 127 {
							key = tcell.KeyBackspace2
							r = 0
						} else if r < 32 {
							key = tcell.KeyCtrlSpace + tcell.Key(r)
							r = 0
						}
						ok = post(tcell.NewEventKey(key, r, 0))
					}
				}
				if packet.paste && ok {
					ok = post(tcell.NewEventPaste(false))
				}
			}
			s.mu.Lock()
			s.inputBytes -= packet.size
			s.mu.Unlock()
			if !ok {
				return
			}
		}
	}
}

var canvasKeys = map[string]tcell.Key{
	"\b":   tcell.KeyBackspace,
	"\x1b": tcell.KeyEscape, "\x1b[A": tcell.KeyUp, "\x1b[B": tcell.KeyDown, "\x1b[C": tcell.KeyRight, "\x1b[D": tcell.KeyLeft,
	"\x1bOP": tcell.KeyF1, "\x1bOQ": tcell.KeyF2, "\x1bOR": tcell.KeyF3, "\x1bOS": tcell.KeyF4,
	"\x1b[15~": tcell.KeyF5, "\x1b[17~": tcell.KeyF6, "\x1b[18~": tcell.KeyF7, "\x1b[19~": tcell.KeyF8, "\x1b[20~": tcell.KeyF9, "\x1b[21~": tcell.KeyF10, "\x1b[23~": tcell.KeyF11, "\x1b[24~": tcell.KeyF12,
	"\x1b[5~": tcell.KeyPgUp, "\x1b[6~": tcell.KeyPgDn, "\x1b[Z": tcell.KeyBacktab, "\x1b[H": tcell.KeyHome, "\x1b[F": tcell.KeyEnd, "\x1b[3~": tcell.KeyDelete,
}
