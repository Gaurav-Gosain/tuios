// Package apptarget drives a real app.OS through its real bubbletea Update as a
// fuzz.Target, using nothing but package app's exported surface.
//
// It exists because the demo binary has to link a target and a test file cannot
// be linked: the in-process target in internal/app lives in _test.go files, so
// cmd/tuios-fuzz cannot reach it. The stronger oracle stays there, where it can
// read package app's internals, and it remains the CI gate.
//
// The rules here are the subset that can be decided honestly from outside, so
// the two oracles overlap. That overlap is deliberate redundancy through a
// different surface, not a copy to keep in sync: this one is a strict subset by
// design, and a rule that cannot be stated from outside is simply absent rather
// than approximated.
package apptarget

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
)

// Target is the model under test plus the bookkeeping the oracle needs: the
// size each pane was last told, and the resting state of every global the
// actions can move.
type Target struct {
	m             *app.OS
	told          map[string]*announcedSize
	dir           string
	seq           int
	lastAction    fuzz.Action
	prevStateHome string
	redirected    bool
	unsandbox     func()
	closed        bool
}

type announcedSize struct {
	w, h  int
	calls int
}

// New builds a target. stateDir is a scratch directory the app's XDG state root
// is pointed at for the life of the target, so a run never writes the
// developer's own sidebar state or crash dumps.
func New(stateDir string) (*Target, error) {
	if stateDir == "" {
		return nil, errors.New("apptarget: a state directory is required, or the run writes the developer's own")
	}
	return &Target{dir: stateDir}, nil
}

const (
	hostWidth  = 120
	hostHeight = 40
	startPanes = 3
)

// Reset builds the starting state: a daemon-attached client with three tiled
// panes on workspace 1, the sidebar on the left, shared borders off, and
// animations off. Everything the run does starts from here, so a shrunk
// sequence replays exactly.
func (t *Target) Reset() error {
	t.releaseWindows()

	seed := config.DefaultSettings()
	seed.Motion = config.MotionNone
	seed.SharedBorders = false
	seed.UseASCIIOnly = false
	seed.BorderStyle = "rounded"
	seed.DockbarPosition = "bottom"
	seed.SidebarEnabled = true
	seed.SidebarPosition = "left"
	seed.SidebarWidth = config.SidebarDefaultWidth
	seed.SessionColors = true

	if !t.redirected {
		t.prevStateHome, t.redirected = xdg.StateHome, true
	}
	xdg.StateHome = t.dir
	if t.unsandbox == nil {
		undo, err := fuzz.Sandbox(t.dir)
		if err != nil {
			return err
		}
		t.unsandbox = undo
	}
	if err := os.RemoveAll(filepath.Join(t.dir, "tuios")); err != nil {
		return err
	}

	cfg := config.DefaultConfig()
	m := app.NewOS(app.OSOptions{
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		Settings:        &seed,
		NumWorkspaces:   9,
		Width:           hostWidth,
		Height:          hostHeight,
		SessionName:     "fuzz",
		ConfigReadOnly:  true,
	})
	app.SetInputHandler(input.HandleInput)
	m.AutoTiling = true
	m.UseBSPLayout = true
	m.CurrentWorkspace = 1

	t.m, t.told = m, map[string]*announcedSize{}
	t.seq = 0
	for range startPanes {
		if err := t.addPane(); err != nil {
			return err
		}
	}
	m.FocusedWindow = 0
	m.TileAllWindows()
	t.closed = false
	return nil
}

func (t *Target) addPane() error {
	t.seq++
	id := fmt.Sprintf("fuzzpane%04d", t.seq)
	win := terminal.NewDaemonWindow(id, "fuzz", 0, 0, 60, 20, 0, "pty-"+id, t.m.PTYDataChan, config.DefaultScrollbackLines)
	if win == nil {
		return errfmt.Errorf("NewDaemonWindow(%s) returned nil", id)
	}
	rec := &announcedSize{}
	win.DaemonResizeFunc = func(w, h int) error {
		rec.w, rec.h, rec.calls = w, h, rec.calls+1
		return nil
	}
	win.Workspace = t.m.CurrentWorkspace
	t.told[id] = rec
	t.m.Windows = append(t.m.Windows, win)
	return nil
}

func (t *Target) releaseWindows() {
	if t.m == nil {
		return
	}
	for _, w := range t.m.Windows {
		w.Close()
	}
	t.m.Windows = nil
	t.m.Cleanup()
}

func (t *Target) Close() {
	if t.closed {
		return
	}
	t.closed = true
	t.releaseWindows()
	app.SetInputHandler(nil)
	if t.unsandbox != nil {
		t.unsandbox()
		t.unsandbox = nil
	}
	if t.redirected {
		xdg.StateHome, t.redirected = t.prevStateHome, false
	}
}

var ticks atomic.Int64

func clock() time.Time {
	return time.Unix(0, 0).Add(time.Duration(ticks.Add(16)) * time.Millisecond)
}

func (t *Target) send(msg tea.Msg) {
	t.m.Update(msg)
	select {
	case <-t.m.PTYDataChan:
	default:
	}
}

// Apply performs one action. Everything a user reaches through a key or a
// pointer goes through Update, because dispatching straight to the OS method a
// keybinding happens to call would skip the routing layer, and the routing layer
// is where a click lands on the wrong target. The few actions with no keystroke
// behind them call the same entry points the daemon paths call.
func (t *Target) Apply(a fuzz.Action) error {
	t.lastAction = a
	if t.m.QuitRequested {
		return nil
	}
	m := t.m

	switch a.Kind {
	case fuzz.Key:
		t.send(keyMsg(a.S))
	case fuzz.Chord:
		t.send(keyMsg(m.Settings.LeaderKey))
		t.send(keyMsg(a.S))
	case fuzz.Text:
		for _, r := range a.S {
			t.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	case fuzz.MousePress:
		t.send(tea.MouseClickMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseMotion:
		t.send(tea.MouseMotionMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseRelease:
		t.send(tea.MouseReleaseMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
		t.send(app.InteractionSettledMsg{})
	case fuzz.MouseWheel:
		b := tea.MouseWheelUp
		if a.C != 0 {
			b = tea.MouseWheelDown
		}
		t.send(tea.MouseWheelMsg{X: a.A, Y: a.B, Button: b})
	case fuzz.Resize:
		t.send(tea.WindowSizeMsg{Width: a.A, Height: a.B})
	case fuzz.NewPane:
		if len(m.Windows) < 12 {
			if err := t.addPane(); err != nil {
				return err
			}
			m.FocusedWindow = len(m.Windows) - 1
			m.TileAllWindows()
			m.SyncDaemonPTYDimensions()
		}
	case fuzz.ClosePane:
		if n := len(m.Windows); n > 1 {
			i := a.A % n
			w := m.Windows[i]
			w.Close()
			delete(t.told, w.ID)
			m.DeleteWindow(i)
			m.TileAllWindows()
			m.SyncDaemonPTYDimensions()
		}
	case fuzz.ZoomPane:
		m.ToggleZoom()
	case fuzz.FocusPane:
		if n := len(m.Windows); n > 0 {
			m.FocusWindow(a.A % n)
		}
	case fuzz.MovePane:
		_ = m.FocusDirection([]string{"left", "right", "up", "down"}[a.A%4])
	case fuzz.SwitchWorkspace:
		m.SwitchToWorkspace(clampWorkspace(a.A, m.NumWorkspaces))
	case fuzz.SwitchSession:
		m.CycleSession(a.A%2*2 - 1)
	case fuzz.ToggleTiling:
		m.ToggleAutoTiling()
	case fuzz.ToggleShared:
		setSharedBorders(m, !m.Settings.SharedBorders)
	case fuzz.LayoutMode:
		switch a.A % 3 {
		case 0:
			m.EnableBSPLayout()
		case 1:
			m.EnableMasterStackLayout()
		default:
			m.EnableScrollingLayout()
		}
	case fuzz.ToggleSidebar:
		m.ToggleSidebar()
	case fuzz.SidebarCollapse:
		m.SidebarSetCollapsed(!m.SidebarCollapsed)
	case fuzz.SidebarPosition:
		m.Settings.SidebarPosition = []string{"left", "right"}[a.A%2]
		m.ApplyAppearanceLive(true)
	case fuzz.OpenOverlay:
		t.openOverlay(a.A)
	case fuzz.CloseOverlay:
		t.send(keyMsg("esc"))
	case fuzz.Rename:
		if w := m.GetFocusedWindow(); w != nil {
			m.BeginRenameWindow(w)
			m.SetRenameText(a.S)
			m.CommitRename()
		}
	case fuzz.Detach:
		m.FireDetached()
		m.EndPointerGrabs()
	case fuzz.Attach:
		m.FireAttached()
		m.SyncDaemonPTYDimensions()
	case fuzz.Setting:
		applySetting(m, a.A, a.B)
	case fuzz.Tick:
		if gen, pending := m.PendingViewportResize(); pending {
			t.send(app.ViewportResizeSettledMsg{Gen: gen})
		}
		t.send(app.TickerMsg(clock()))
	case fuzz.Guest:
		t.guestWrite(a.S)
	case fuzz.AltScreen:
		if a.A%2 == 1 {
			t.guestWrite("\x1b[?1049h\x1b[2J\x1b[H")
		} else {
			t.guestWrite("\x1b[?1049l")
		}
	case fuzz.Burst:
		t.guestWrite(burstText(a.A))
	case fuzz.SecondClient, fuzz.DaemonRestart:
	}
	return nil
}

func setSharedBorders(m *app.OS, v bool) {
	m.Settings.SharedBorders = v
	m.ApplyAppearanceLive(true)
}

func (t *Target) guestWrite(s string) {
	w := t.m.GetFocusedWindow()
	if w == nil || w.Terminal == nil {
		return
	}
	w.LockIO()
	_, _ = w.Terminal.Write([]byte(s))
	w.UnlockIO()
	w.MarkContentDirty()
}

func burstText(n int) string {
	n = min(max(n, 0), 5000)
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "burst %d\r\n", i)
	}
	return b.String()
}

func clampWorkspace(n, count int) int {
	if count <= 0 {
		return 1
	}
	w := n % count
	if w <= 0 {
		w += count
	}
	return w
}

func (t *Target) openOverlay(n int) {
	m := t.m
	switch n % 10 {
	case 0:
		m.ShowHelp = !m.ShowHelp
	case 1:
		m.OpenSettings()
	case 2:
		_ = m.OpenCommandPalette()
	case 3:
		m.OpenSessionSwitcher()
	case 4:
		m.OpenWorkspaceSwitcher()
	case 5:
		m.OpenThemePicker()
	case 6:
		m.OpenQuitMenu()
	case 7:
		m.OpenContextMenu(m.Width/2, m.Height/2)
	case 8:
		if w := m.GetFocusedWindow(); w != nil {
			m.OpenAccentPicker(w.ID)
		}
	default:
		m.OpenAggregateView()
	}
}

func applySetting(m *app.OS, which, value int) {
	on := value&1 != 0
	switch which % 9 {
	case 0:
		setSharedBorders(m, on)
	case 1:
		m.Settings.UseASCIIOnly = on
		m.ApplyAppearanceLive(true)
	case 2:
		if n := len(config.BorderStyles); n > 0 {
			m.Settings.BorderStyle = config.BorderStyles[value%n]
		}
		m.ApplyAppearanceLive(true)
	case 3:
		m.Settings.DockbarPosition = map[bool]string{true: "top", false: "bottom"}[on]
		m.ApplyAppearanceLive(true)
	case 4:
		m.Settings.DockbarPosition = map[bool]string{true: "hidden", false: "bottom"}[on]
		m.ApplyAppearanceLive(true)
	case 5:
		m.Settings.SessionColors = on
	case 6:
		m.Settings.SidebarEnabled = on
		m.ApplyAppearanceLive(true)
	case 7:
		m.Settings.SidebarWidth = map[bool]int{true: config.SidebarDefaultWidth, false: 20}[on]
		m.ApplyAppearanceLive(true)
	default:
		if on {
			m.EnterSidebarFocus()
		} else {
			m.ExitSidebarFocus()
		}
	}
}

func mouseButton(c int) tea.MouseButton {
	switch c {
	case fuzz.ButtonLeft:
		return tea.MouseLeft
	case fuzz.ButtonRight:
		return tea.MouseRight
	case fuzz.ButtonMiddle:
		return tea.MouseMiddle
	}
	return tea.MouseNone
}

func keyMsg(name string) tea.KeyPressMsg {
	msg := tea.KeyPressMsg{}
	for {
		prefix, rest, found := strings.Cut(name, "+")
		if !found {
			break
		}
		switch prefix {
		case "ctrl":
			msg.Mod |= tea.ModCtrl
		case "alt":
			msg.Mod |= tea.ModAlt
		case "shift":
			msg.Mod |= tea.ModShift
		default:
			return finishKey(msg, name)
		}
		name = rest
	}
	return finishKey(msg, name)
}

var namedKeys = map[string]rune{
	"esc": tea.KeyEscape, "enter": tea.KeyEnter, "tab": tea.KeyTab,
	"space": tea.KeySpace, "backspace": tea.KeyBackspace, "delete": tea.KeyDelete,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"insert": tea.KeyInsert,
}

func finishKey(msg tea.KeyPressMsg, name string) tea.KeyPressMsg {
	if code, ok := namedKeys[name]; ok {
		msg.Code = code
		return msg
	}
	r := []rune(name)
	if len(r) == 0 {
		msg.Code = tea.KeySpace
		return msg
	}
	msg.Code = r[0]
	if msg.Mod == 0 {
		msg.Text = string(r[0])
	}
	return msg
}
