// Package tui provides one keyboard-first interface for every protocol.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/Live-yum/iotools/internal/config"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const help = `IOTOOLS • keyboard reference

Tab / Shift-Tab   Cycle requests, details, results, search
↑ ↓ / j k        Select saved request
Enter / F5       Run selected request (writes require confirmation)
F4               Edit collection YAML inside this terminal
F6               Select environment profile
F8               Cancel running request / subscription
Ctrl-L           Clear current results
? / F1           This help
Ctrl-C / q       Cancel and quit

Editor: Ctrl-S validates and saves, Esc cancels.
No network request runs on startup or selection.
Profiles: ${name}; environment secret: ${env:VARIABLE}.
Edit timeouts, limits and protocol params in the collection.
Data stays in memory unless you explicitly export CLI output.
Streaming results are bounded; use the CLI for long captures.`

type UI struct {
	App                    *tview.Application
	pages                  *tview.Pages
	list                   *tview.List
	detail, result, status *tview.TextView
	search                 *tview.InputField
	collection             *config.Collection
	raw                    []byte
	path, profile          string
	readonly               bool
	indexes                []int
	selected               int
	cancel                 context.CancelFunc
	running                bool
	quitting               bool
	mu                     sync.Mutex
	events                 []string
	focus                  int
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return '�'
	}, s)
}
func New(path, profile string, readonly bool) (*UI, error) {
	c, b, e := config.Load(path)
	if e != nil {
		return nil, e
	}
	if profile != "" {
		if _, ok := c.Profiles[profile]; !ok {
			return nil, fmt.Errorf("unknown profile %q", profile)
		}
	}
	u := &UI{App: tview.NewApplication(), collection: c, raw: b, path: path, profile: profile, readonly: readonly}
	u.pages = tview.NewPages()
	u.list = tview.NewList().ShowSecondaryText(true)
	u.list.SetBorder(true).SetTitle(" Collections ")
	u.detail = tview.NewTextView().SetWrap(true)
	u.detail.SetBorder(true).SetTitle(" Request ")
	u.result = tview.NewTextView().SetWrap(false)
	u.result.SetBorder(true).SetTitle(" Results / live events ")
	u.status = tview.NewTextView().SetTextColor(tcell.ColorAqua)
	u.search = tview.NewInputField().SetLabel(" Filter: ")
	u.search.SetChangedFunc(func(s string) { u.populate(s) })
	top := tview.NewFlex().AddItem(u.list, 30, 1, true).AddItem(u.detail, 0, 2, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(u.search, 1, 0, false).AddItem(top, 0, 1, true).AddItem(u.result, 0, 1, false).AddItem(u.status, 2, 0, false)
	u.pages.AddPage("main", root, true, true)
	u.list.SetChangedFunc(func(index int, main, secondary string, shortcut rune) {
		if index >= 0 && index < len(u.indexes) {
			u.selected = u.indexes[index]
			u.preview()
		}
	})
	u.list.SetSelectedFunc(func(_ int, _, _ string, _ rune) { u.execute() })
	u.populate("")
	u.setStatus("Ready • Enter run · F4 edit · F6 profile · F8 cancel · ? help")
	u.App.SetRoot(u.pages, true).EnableMouse(true)
	u.App.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyCtrlC {
			u.quit()
			return nil
		}
		name, _ := u.pages.GetFrontPage()
		if name != "main" {
			return e
		}
		switch e.Key() {
		case tcell.KeyCtrlC:
			u.quit()
			return nil
		case tcell.KeyF1:
			u.modal(help)
			return nil
		case tcell.KeyF4:
			u.edit()
			return nil
		case tcell.KeyF5:
			u.execute()
			return nil
		case tcell.KeyF6:
			u.profiles()
			return nil
		case tcell.KeyF8:
			u.stop()
			u.setStatus("Cancelling…")
			return nil
		case tcell.KeyCtrlL:
			u.events = nil
			u.result.Clear()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			items := []tview.Primitive{u.list, u.detail, u.result, u.search}
			delta := 1
			if e.Key() == tcell.KeyBacktab {
				delta = 3
			}
			u.focus = (u.focus + delta) % 4
			u.App.SetFocus(items[u.focus])
			return nil
		}
		if !u.search.HasFocus() {
			switch e.Rune() {
			case '?':
				u.modal(help)
				return nil
			case 'q':
				u.quit()
				return nil
			}
		}
		return e
	})
	return u, nil
}
func (u *UI) setStatus(s string) {
	mode := ""
	if u.readonly {
		mode = " • READ ONLY"
	}
	u.status.SetText(clean(s) + "\nProfile: " + u.profile + mode + " • " + u.path)
}
func (u *UI) populate(filter string) {
	u.list.Clear()
	u.indexes = nil
	for i, r := range u.collection.Requests {
		if strings.Contains(strings.ToLower(r.ID+" "+r.Name+" "+r.Protocol), strings.ToLower(filter)) {
			u.indexes = append(u.indexes, i)
			name := r.Name
			if name == "" {
				name = r.ID
			}
			u.list.AddItem(clean(name), clean(strings.ToUpper(r.Protocol)+" · "+r.Action), 0, nil)
		}
	}
	if len(u.indexes) > 0 {
		u.selected = u.indexes[0]
		u.list.SetCurrentItem(0)
		u.preview()
	} else {
		u.detail.SetText("No matching requests. F4 opens the collection editor.")
	}
}
func (u *UI) preview() {
	if u.selected >= len(u.collection.Requests) {
		return
	}
	r := u.collection.Requests[u.selected]
	b, _ := json.MarshalIndent(r, "", "  ")
	note := ""
	if r.Mutates() {
		note = "WRITE OPERATION • confirmation required\n\n"
	}
	u.detail.SetText(clean(note + string(b)))
}
func (u *UI) modal(text string) {
	m := tview.NewModal().SetText(clean(text)).AddButtons([]string{"Close"})
	m.SetDoneFunc(func(_ int, _ string) { u.pages.RemovePage("modal"); u.App.SetFocus(u.list) })
	m.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			u.pages.RemovePage("modal")
			u.App.SetFocus(u.list)
			return nil
		}
		return e
	})
	u.pages.AddPage("modal", m, true, true)
	u.App.SetFocus(m)
}
func (u *UI) execute() {
	if u.running {
		u.setStatus("A request is running. F8 cancels it first.")
		return
	}
	if len(u.indexes) == 0 {
		return
	}
	r, e := u.collection.Resolve(u.collection.Requests[u.selected], u.profile)
	if e != nil {
		u.modal(e.Error())
		return
	}
	if r.Mutates() {
		if u.readonly {
			u.modal("Read-only mode blocks this operation.")
			return
		}
		m := tview.NewModal().SetText(clean("Execute write operation?\n" + r.Protocol + " / " + r.Action + "\n" + r.Endpoint + "\n\nThis may change data on the selected server.")).AddButtons([]string{"Cancel", "Execute"})
		m.SetDoneFunc(func(i int, _ string) {
			u.pages.RemovePage("confirm")
			u.App.SetFocus(u.list)
			if i == 1 {
				u.start(r)
			}
		})
		m.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			if e.Key() == tcell.KeyEscape {
				u.pages.RemovePage("confirm")
				u.App.SetFocus(u.list)
				return nil
			}
			return e
		})
		u.pages.AddPage("confirm", m, true, true)
		u.App.SetFocus(m)
		return
	}
	u.start(r)
}
func (u *UI) start(r config.Request) {
	ctx, cancel := context.WithCancel(context.Background())
	u.mu.Lock()
	u.cancel = cancel
	u.mu.Unlock()
	u.running = true
	u.events = nil
	u.result.Clear()
	u.setStatus("Running " + r.ID + " • F8 cancels")
	go func() {
		e := engine.Run(ctx, r, true, func(event engine.Event) {
			b, err := json.MarshalIndent(event, "", "  ")
			if err != nil {
				b = []byte(fmt.Sprintf("Unable to render event: %v", err))
			}
			s := clean(string(b))
			if len(s) > 32768 {
				s = s[:32768] + "\n… event truncated in TUI (use CLI for full data)"
			}
			u.App.QueueUpdateDraw(func() {
				u.events = append(u.events, s)
				if len(u.events) > 128 {
					u.events = u.events[len(u.events)-128:]
				}
				u.result.SetText(strings.Join(u.events, "\n\n"))
				u.result.ScrollToEnd()
			})
		})
		cancel()
		u.App.QueueUpdateDraw(func() {
			u.running = false
			if u.quitting {
				u.App.Stop()
				return
			}
			u.mu.Lock()
			u.cancel = nil
			u.mu.Unlock()
			if e != nil {
				u.setStatus("Stopped: " + e.Error())
			} else {
				u.setStatus("Completed " + r.ID)
			}
		})
	}()
}
func (u *UI) stop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil {
		u.cancel()
	}
}
func (u *UI) edit() {
	if u.running {
		u.modal("Cancel the active request before editing.")
		return
	}
	editor := tview.NewTextArea().SetText(string(u.raw), false)
	editor.SetBorder(true).SetTitle(" Collection YAML • Ctrl-S save · Esc cancel ")
	editor.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			u.pages.RemovePage("editor")
			u.App.SetFocus(u.list)
			return nil
		case tcell.KeyCtrlS:
			b := []byte(editor.GetText())
			if e := config.Save(u.path, b); e != nil {
				editor.SetTitle(" Save failed: " + clean(e.Error()) + " • Esc cancel ")
				return nil
			}
			c, e := config.Parse(b)
			if e != nil {
				return nil
			}
			u.collection = c
			u.raw = b
			u.pages.RemovePage("editor")
			u.populate(u.search.GetText())
			u.App.SetFocus(u.list)
			u.setStatus("Collection saved")
			return nil
		}
		return e
	})
	u.pages.AddPage("editor", editor, true, true)
	u.App.SetFocus(editor)
}
func (u *UI) profiles() {
	if u.running {
		u.modal("Cancel the active request before switching profiles.")
		return
	}
	names := []string{""}
	for name := range u.collection.Profiles {
		names = append(names, name)
	}
	sort.Strings(names[1:])
	list := tview.NewList()
	list.SetBorder(true).SetTitle(" Select profile • Esc closes ")
	for _, name := range names {
		n := name
		label := n
		if n == "" {
			label = "(none)"
		}
		list.AddItem(label, "", 0, func() {
			u.profile = n
			u.pages.RemovePage("profiles")
			u.App.SetFocus(u.list)
			u.setStatus("Profile selected")
		})
	}
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			u.pages.RemovePage("profiles")
			u.App.SetFocus(u.list)
			return nil
		}
		return e
	})
	u.pages.AddPage("profiles", list, true, true)
	u.App.SetFocus(list)
}
func (u *UI) Run() error { defer u.stop(); return u.App.Run() }
func Run(path, profile string, readonly bool) error {
	if _, e := os.Stat(path); e != nil {
		return e
	}
	u, e := New(path, profile, readonly)
	if e != nil {
		return e
	}
	return u.Run()
}

func (u *UI) quit() {
	u.quitting = true
	u.stop()
	if !u.running {
		u.App.Stop()
	}
}
