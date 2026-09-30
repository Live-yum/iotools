package mobile

import (
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"github.com/Live-yum/iotools/internal/sample"
	"github.com/Live-yum/iotools/internal/tui"
	"github.com/gdamore/tcell/v2"
	_ "github.com/gdamore/tcell/v2/terminfo/x/xterm"
	"os"
	"path/filepath"
	"sync"
)

type Session struct {
	TTY     *TTY
	ui      *tui.UI
	done    chan struct{}
	mu      sync.Mutex
	err     error
	pending []func(tcell.Screen)
	closed  bool
}

type Options struct {
	ReadOnly bool
	History  bool
}

func Start(path, version string, width, height int) (*Session, error) {
	return StartWithOptions(path, version, width, height, Options{})
}
func StartWithOptions(path, version string, width, height int, options Options) (*Session, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("配置路径必须是应用私有目录绝对路径")
	}
	if width < 20 || width > 300 || height < 8 || height > 150 {
		return nil, fmt.Errorf("终端尺寸无效")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(sample.Collection)
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
	} else if err != nil {
		return nil, err
	}
	ui, err := tui.New(path, "", options.ReadOnly)
	if err != nil {
		return nil, err
	}
	tty := NewTTY(width, height)
	screen, err := tcell.NewTerminfoScreenFromTty(tty)
	if err != nil {
		return nil, err
	}
	if options.History {
		ui.HTTPHistoryPath = filepath.Join(filepath.Dir(path), "history.sqlite")
	}
	ui.App.SetScreen(screen)
	ui.BuildVersion = version
	session := &Session{TTY: tty, ui: ui, done: make(chan struct{})}
	ui.App.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		session.mu.Lock()
		pending := session.pending
		session.pending = nil
		session.mu.Unlock()
		for _, operation := range pending {
			operation(screen)
		}
		return false
	})

	go func() {
		err := ui.Run()
		session.mu.Lock()
		session.err = err
		session.closed = true
		session.pending = nil
		session.mu.Unlock()
		tty.Close()
		close(session.done)
	}()
	return session, nil
}
func (s *Session) Stop() {
	select {
	case <-s.done:
		return
	default:
		s.ui.App.QueueEvent(tcell.NewEventKey(tcell.KeyCtrlC, 0, 0))
	}
}
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func ValidateConfig(path string) error   { _, _, err := engine.LoadCollection(path); return err }

// ImportConfig preserves the old bytes until validation succeeds and replaces
// atomically in the same app-private directory. Caller must stop the session.
func ImportConfig(staged, target string) error {
	if filepath.Dir(staged) != filepath.Dir(target) {
		return fmt.Errorf("导入暂存文件必须位于应用私有目录")
	}
	info, err := os.Lstat(staged)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("配置必须为不超过1MiB的普通文件")
	}
	if err = ValidateConfig(staged); err != nil {
		return err
	}
	if old, err := os.ReadFile(target); err == nil {
		backup, err := os.CreateTemp(filepath.Dir(target), "config-before-import-*.yaml")
		if err != nil {
			return err
		}
		_, err = backup.Write(old)
		ce := backup.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
	}
	return os.Rename(staged, target)
}

func (s *Session) control(operation func(tcell.Screen)) {
	s.mu.Lock()
	if s.closed || len(s.pending) >= 16 {
		s.mu.Unlock()
		return
	}
	s.pending = append(s.pending, operation)
	s.mu.Unlock()
	// At most16 pending controls ensure this cannot fill tview's event queue
	// even if Run exits concurrently. BeforeDraw also drains after paste ends.
	s.ui.App.QueueEvent(tcell.NewEventKey(tcell.KeyF24, 0, 0))
}
func (s *Session) Pause()  { s.control(func(tcell.Screen) { s.ui.CancelMobileWork() }) }
func (s *Session) Resume() { s.control(func(screen tcell.Screen) { screen.Sync() }) }
func (s *Session) ApplyOptions(options Options, path string) {
	s.control(func(tcell.Screen) {
		history := ""
		if options.History {
			history = filepath.Join(filepath.Dir(path), "history.sqlite")
		}
		s.ui.ApplyMobileOptions(options.ReadOnly, history)
	})
}
