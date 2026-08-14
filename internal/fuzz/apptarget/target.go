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
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
)

// Target is the model under test plus the bookkeeping the oracle needs: the
// size each pane was last told, and the resting state of every global the
// actions can move.
type Target struct {
	m    *app.OS
	told map[string]*announcedSize
	// saved is the resting state of the globals, taken once at construction so
	// Close puts back what the process started with rather than what the
	// previous Reset left.
	saved configSnapshot
	dir   string
	seq   int
	// lastAction is carried into Check so a violation can name what caused it.
	lastAction fuzz.Action
	// prevStateHome is the XDG state root to put back on Close. See Reset.
	prevStateHome string
	redirected    bool
	closed        bool
}

// announcedSize is the last size a pane's daemon PTY was told, recorded through
// the exported resize hook. It is the pane's only statement about the size it
// believes its guest has, so without it there is nothing to hold the emulator's
// grid against.
type announcedSize struct {
	w, h  int
	calls int
}

// configSnapshot is every package-global the action alphabet can move. Missing
// one makes a finding unreproducible, so the list is exhaustive over the
// setting table and the sidebar and border actions rather than over what a given
// run happens to touch.
type configSnapshot struct {
	shared, anim, ascii, sessionColors bool
	sidebarOn                          bool
	borderStyle, dockPos, sidebarPos   string
	sidebarWidth                       int
}

func snapshotConfig() configSnapshot {
	return configSnapshot{
		shared: config.SharedBorders, anim: config.AnimationsEnabled,
		ascii: config.UseASCIIOnly, sessionColors: config.SessionColors,
		sidebarOn: config.SidebarEnabled, borderStyle: config.BorderStyle,
		dockPos: config.DockbarPosition, sidebarPos: config.SidebarPosition,
		sidebarWidth: config.SidebarWidth,
	}
}

func (c configSnapshot) restore() {
	config.SharedBorders, config.AnimationsEnabled = c.shared, c.anim
	config.UseASCIIOnly, config.SessionColors = c.ascii, c.sessionColors
	config.SidebarEnabled, config.BorderStyle = c.sidebarOn, c.borderStyle
	config.DockbarPosition, config.SidebarPosition = c.dockPos, c.sidebarPos
	config.SidebarWidth = c.sidebarWidth
}

// New builds a target. stateDir is a scratch directory the app's XDG state root
// is pointed at for the life of the target, so a run never writes the
// developer's own sidebar state or crash dumps.
func New(stateDir string) (*Target, error) {
	if stateDir == "" {
		return nil, errors.New("apptarget: a state directory is required, or the run writes the developer's own")
	}
	return &Target{saved: snapshotConfig(), dir: stateDir}, nil
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
	t.saved.restore()

	config.AnimationsEnabled = false
	config.SharedBorders = false
	config.UseASCIIOnly = false
	config.BorderStyle = "rounded"
	config.DockbarPosition = "bottom"
	config.SidebarEnabled = true
	config.SidebarPosition = "left"
	config.SidebarWidth = config.SidebarDefaultWidth
	config.SessionColors = true

	// Redirected for the life of the target, not just for this call: the rail
	// persists a collapse, a drag order and a width, and it does so from inside
	// the actions rather than at shutdown. XDG_STATE_HOME is no use, because the
	// xdg package resolves its paths once at init; the variable it resolved into
	// is read afresh on every write, so moving that is what actually redirects.
	if !t.redirected {
		t.prevStateHome, t.redirected = xdg.StateHome, true
	}
	xdg.StateHome = t.dir
	// Cleared, not just redirected. Every replay of a shrink candidate shares
	// this directory, so a run that dragged the rail wider persisted that width
	// and the next replay loaded it back at construction and started somewhere
	// else. A shrunk script that begins from a different rail is a repro that
	// does not reproduce.
	if err := os.RemoveAll(filepath.Join(t.dir, "tuios")); err != nil {
		return err
	}

	cfg := config.DefaultConfig()
	// No daemon client, on purpose. The panes are daemon-backed windows, which
	// is what gives them an emulator and a resize hook without forking a shell,
	// but the transport is left out: a client with no connection panics inside
	// its own send, which would be reported as a finding in tuios and is not
	// one. Everything this oracle reads is client-side, and the announcement it
	// depends on arrives through DaemonResizeFunc per pane.
	m := app.NewOS(app.OSOptions{
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		NumWorkspaces:   9,
		Width:           hostWidth,
		Height:          hostHeight,
		SessionName:     "fuzz",
	})
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

// addPane attaches a live daemon window with its own emulator, which is what
// makes the announced-size rule meaningful: a pane with no emulator cannot be
// asked what size it thinks it is.
func (t *Target) addPane() error {
	t.seq++
	id := fmt.Sprintf("fuzzpane%04d", t.seq)
	win := terminal.NewDaemonWindow(id, "fuzz", 0, 0, 60, 20, 0, "pty-"+id, t.m.PTYDataChan)
	if win == nil {
		return fmt.Errorf("NewDaemonWindow(%s) returned nil", id)
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
}

func (t *Target) Close() {
	if t.closed {
		return
	}
	t.closed = true
	t.releaseWindows()
	t.saved.restore()
	if t.redirected {
		xdg.StateHome, t.redirected = t.prevStateHome, false
	}
}

// clock is a monotonic stand-in for wall time. Ticks carry a timestamp and
// several code paths compare it against a stored one, so a run driven by
// time.Now would take a different branch on a slow machine and stop
// reproducing.
var ticks atomic.Int64

func clock() time.Time {
	return time.Unix(0, 0).Add(time.Duration(ticks.Add(16)) * time.Millisecond)
}

// send pushes one message through the real Update. Update recovers panics
// internally, so a panic is not a crash here; the oracle notices it by reading
// the log instead.
func (t *Target) send(msg tea.Msg) {
	t.m.Update(msg)
	// The PTY channel is buffered at one and nothing drains it here, so a pane
	// that produced output would eventually block its writer. Drain it.
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
	m := t.m

	switch a.Kind {
	case fuzz.Key:
		t.send(keyMsg(a.S))
	case fuzz.Chord:
		t.send(keyMsg(config.LeaderKey))
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
		setSharedBorders(m, !config.SharedBorders)
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
		// Through the entry point the keybinding and the strip's own control
		// use, not the bare field. Collapsing changes the columns the rail
		// reserves, so the panes have to be re-laid out into the region that
		// leaves them.
		m.SidebarSetCollapsed(!m.SidebarCollapsed)
	case fuzz.SidebarPosition:
		config.SidebarPosition = []string{"left", "right"}[a.A%2]
		m.ApplyAppearanceLive(true)
	case fuzz.OpenOverlay:
		t.openOverlay(a.A)
	case fuzz.CloseOverlay:
		t.send(keyMsg("esc"))
	case fuzz.Rename:
		if w := m.GetFocusedWindow(); w != nil {
			m.BeginRenameWindow(w)
			m.RenameBuffer = a.S
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
		// A tick is where time passes, and passing time is what ends a resize
		// deferral. Update returns the settle as a command and nothing here runs
		// commands, so the timer is delivered by hand; without it the deferral
		// stays fresh forever, every retile takes the visual-only branch, and no
		// pane is ever told its real size.
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
		// Both need a daemon on the far end of a socket, and in process there is
		// none: the model owns its panes directly. They are carried in the shared
		// alphabet so a PTY finding replays here as far as it can, and the PTY
		// target is where they do their work.
	}
	return nil
}

// setSharedBorders flips the shared-border setting the way the settings page
// does: the global moves and the layout is reflowed, because the cells a pane
// withholds from its guest follow the border it draws.
func setSharedBorders(m *app.OS, v bool) {
	config.SharedBorders = v
	m.ApplyAppearanceLive(true)
}

// guestWrite feeds bytes to the focused pane's emulator the way its own program
// would.
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

// burstText is n numbered lines. The number makes a line identifiable, which is
// what lets the PTY oracle notice a pane rendering two stretches of a stream
// with the middle missing; in process it is a cheap way to fill the scrollback.
func burstText(n int) string {
	n = min(max(n, 0), 5000)
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "burst %d\r\n", i)
	}
	return b.String()
}

// clampWorkspace keeps the index inside the model's own range, since the
// out-of-range case is the model's own clamp and has its own test.
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

// openOverlay picks one of the overlays a user can reach. Opening them through
// their own entry points rather than through a chord makes the choice
// deterministic, which is what the shrinker needs.
func (t *Target) openOverlay(n int) {
	m := t.m
	switch n % 10 {
	case 0:
		m.ShowHelp = !m.ShowHelp
	case 1:
		m.OpenSettings()
	case 2:
		m.OpenCommandPalette()
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

// applySetting is the runtime-settings surface: the flips a user makes from the
// settings panel or the command palette while panes are on screen. Each one
// changes what a pane's frame looks like, which is why they belong in the
// alphabet rather than in the fixture.
func applySetting(m *app.OS, which, value int) {
	on := value&1 != 0
	switch which % 9 {
	case 0:
		setSharedBorders(m, on)
	case 1:
		config.UseASCIIOnly = on
		m.ApplyAppearanceLive(true)
	case 2:
		// Every style the settings page offers, not two of them: a style swapped
		// under a live layout is the sequence a table test cannot reach.
		if n := len(config.BorderStyles); n > 0 {
			config.BorderStyle = config.BorderStyles[value%n]
		}
		m.ApplyAppearanceLive(true)
	case 3:
		config.DockbarPosition = map[bool]string{true: "top", false: "bottom"}[on]
		m.ApplyAppearanceLive(true)
	case 4:
		config.DockbarPosition = map[bool]string{true: "hidden", false: "bottom"}[on]
		m.ApplyAppearanceLive(true)
	case 5:
		config.SessionColors = on
	case 6:
		config.SidebarEnabled = on
		m.ApplyAppearanceLive(true)
	case 7:
		config.SidebarWidth = map[bool]int{true: config.SidebarDefaultWidth, false: 20}[on]
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

// keyMsg turns a key name from the alphabet into the message bubbletea would
// deliver. The named codes have to be spelled out because a keybinding matches
// on the code, not on the text.
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
			// Not a modifier, so the "+" belongs to the key itself.
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
