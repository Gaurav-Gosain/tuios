package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The in-process fuzz target. It drives a real OS through the real
// bubbletea Update for keys, mouse, and resizes, which is the whole point:
// dispatching straight to the OS method a keybinding happens to call would skip
// the routing layer, and the routing layer is where a click lands on the wrong
// target.
//
// Everything is deterministic on purpose. Animations stay off because they are
// driven by wall time, and every package-global the run can flip is snapshotted
// and restored on Close, because a replay that inherits the previous replay's
// SharedBorders is a replay that does not reproduce.

// fuzzOS is the model under test plus the bookkeeping the oracle needs: the
// size each pane was last told, so a spurious announcement can be spotted, and
// the resting state of every global the actions can move.
type fuzzOS struct {
	m     *OS
	told  map[string]*toldSize
	saved configSnapshot
	dir   func() string
	seq   int
	// drawable is the drawable size of every visible pane before the action now
	// being applied, which is what the no-spurious-winch rule compares against.
	drawable map[string][2]int
	calls    map[string]int
	// lastAction is carried into Check so a violation can name what caused it.
	lastAction fuzz.Action
	// prevSignature and prevRail let the cache rule see whether the rail's drawn
	// output moved without its signature moving.
	prevSignature string
	prevRail      string
	// settledDrawable and settledCalls are the baseline the no-spurious-winch
	// rule compares against: the last state with no resize in flight.
	settledDrawable map[string][2]int
	// movedSinceSettled records the panes whose drawable size left the baseline
	// at any point since it was taken, including while a resize was in flight.
	movedSinceSettled map[string]bool
	settledCalls      map[string]int
	// prevStateDir is the sidebar state directory to put back on Close. See Reset.
	prevStateDir func() string
	closed       bool
}

// configSnapshot is every package-global the action alphabet can move. Missing
// one makes a finding unreproducible, so the list is exhaustive over the
// Setting table and the sidebar and border actions rather than over what a
// given run happens to touch.
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

// newFuzzTarget builds a target. stateDir is a scratch directory the sidebar's
// persisted order is redirected into, so a run never writes the developer's
// real state.
func newFuzzTarget(stateDir string) (fuzz.Target, error) {
	return &fuzzOS{saved: snapshotConfig(), dir: func() string { return stateDir }}, nil
}

const (
	fuzzWidth  = 120
	fuzzHeight = 40
	fuzzPanes  = 3
)

// Reset builds the starting state: a daemon-attached client with three tiled
// panes on workspace 1, the sidebar on the left, shared borders off, and
// animations off. Everything the run does starts from here, so a shrunk
// sequence replays exactly.
func (f *fuzzOS) Reset() error {
	f.releaseWindows()
	f.saved.restore()

	config.AnimationsEnabled = false
	config.SharedBorders = false
	config.UseASCIIOnly = false
	config.BorderStyle = "rounded"
	config.DockbarPosition = "bottom"
	config.SidebarEnabled = true
	config.SidebarPosition = "left"
	config.SidebarWidth = config.SidebarDefaultWidth
	config.SessionColors = true

	// Redirected for the life of the target, not just for this call. Actions
	// write the state file too - collapsing the rail persists it - and
	// XDG_STATE_HOME cannot be used to catch them, because the xdg package
	// resolves its paths once at init and t.Setenv comes far too late. A run that
	// left the redirect behind wrote a collapsed rail into the developer's own
	// state file, which then made every later sidebar test render a glyph strip.
	if f.prevStateDir == nil {
		f.prevStateDir = sidebarStateDir
	}
	sidebarStateDir = f.dir

	cfg := config.DefaultConfig()
	// No daemon client, on purpose. The panes are daemon-backed windows, which
	// is what gives them an emulator and a resize hook without forking a shell,
	// but the transport is left out: a client with no connection panics inside
	// its own send, which would be reported as a finding in tuios and is not
	// one. The daemon protocol is the PTY target's job, and that one runs a
	// real daemon. Everything this oracle reads is client-side, and the PTY
	// announcement it depends on arrives through DaemonResizeFunc per pane.
	m := NewOS(OSOptions{
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		NumWorkspaces:   9,
		Width:           fuzzWidth,
		Height:          fuzzHeight,
		SessionName:     "fuzz",
	})
	m.AutoTiling = true
	m.UseBSPLayout = true
	m.CurrentWorkspace = 1
	// The rail's collapsed state is persisted, and every seed and every shrink
	// replay shares one state directory, so a run that collapsed the rail left
	// the next one starting with it collapsed. That made a shrunk script wrong:
	// the shrinker kept verifying against a rail it had inherited, so it dropped
	// the action that collapsed it and printed a repro that no longer reproduced.
	m.SidebarCollapsed = false

	f.m, f.told = m, map[string]*toldSize{}
	f.seq = 0
	for range fuzzPanes {
		if err := f.addPane(); err != nil {
			return err
		}
	}
	m.FocusedWindow = 0
	m.TileAllWindows()
	f.drawable, f.calls = drawableSizes(m), callCounts(f.told)
	f.settledDrawable, f.settledCalls = drawableSizes(m), callCounts(f.told)
	f.movedSinceSettled = map[string]bool{}
	f.prevSignature, f.prevRail = "", ""
	f.closed = false
	return nil
}

// addPane attaches a live daemon window with its own emulator, which is what
// makes the guest-ownership and announced-size rules meaningful: a pane with no
// emulator cannot be asked what size it thinks it is.
func (f *fuzzOS) addPane() error {
	f.seq++
	id := fmt.Sprintf("fuzzpane%04d", f.seq)
	win := terminal.NewDaemonWindow(id, "fuzz", 0, 0, 60, 20, 0, "pty-"+id, f.m.PTYDataChan)
	if win == nil {
		return fmt.Errorf("NewDaemonWindow(%s) returned nil", id)
	}
	rec := &toldSize{}
	win.DaemonResizeFunc = func(w, h int) error {
		rec.w, rec.h, rec.calls = w, h, rec.calls+1
		return nil
	}
	win.Workspace = f.m.CurrentWorkspace
	f.told[id] = rec
	f.m.Windows = append(f.m.Windows, win)
	return nil
}

func (f *fuzzOS) releaseWindows() {
	if f.m == nil {
		return
	}
	for _, w := range f.m.Windows {
		w.Close()
	}
	f.m.Windows = nil
}

func (f *fuzzOS) Close() {
	if f.closed {
		return
	}
	f.closed = true
	f.releaseWindows()
	f.saved.restore()
	if f.prevStateDir != nil {
		sidebarStateDir = f.prevStateDir
		f.prevStateDir = nil
	}
}

// send pushes one message through the real Update. Update recovers panics
// internally, so a panic is not a crash here; the oracle notices it by the
// crash counter instead.
func (f *fuzzOS) send(msg tea.Msg) {
	f.m.Update(msg)
	// The PTY channel is buffered at one and nothing drains it in a test, so a
	// pane that produced output would eventually block its writer. Drain it.
	select {
	case <-f.m.PTYDataChan:
	default:
	}
}

// Apply performs one action. Everything that a user reaches through a key or a
// pointer goes through Update; the few actions with no keystroke behind them
// (creating a pane the daemon would have pushed, attaching, detaching) call the
// same entry points the daemon paths call.
func (f *fuzzOS) Apply(a fuzz.Action) error {
	f.lastAction = a
	f.drawable, f.calls = drawableSizes(f.m), callCounts(f.told)
	m := f.m

	switch a.Kind {
	case fuzz.Key:
		f.send(keyMsg(a.S))
	case fuzz.Chord:
		f.send(keyMsg(config.LeaderKey))
		f.send(keyMsg(a.S))
	case fuzz.Text:
		for _, r := range a.S {
			f.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	case fuzz.MousePress:
		f.send(tea.MouseClickMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseMotion:
		f.send(tea.MouseMotionMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseRelease:
		f.send(tea.MouseReleaseMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseWheel:
		b := tea.MouseWheelUp
		if a.C != 0 {
			b = tea.MouseWheelDown
		}
		f.send(tea.MouseWheelMsg{X: a.A, Y: a.B, Button: b})
	case fuzz.Resize:
		f.send(tea.WindowSizeMsg{Width: a.A, Height: a.B})
	case fuzz.NewPane:
		if len(m.Windows) < 12 {
			if err := f.addPane(); err != nil {
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
			delete(f.told, w.ID)
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
		m.FocusDirection([]string{"left", "right", "up", "down"}[a.A%4])
	case fuzz.SwitchWorkspace:
		m.SwitchToWorkspace(clampFuzzWorkspace(a.A, m.NumWorkspaces))
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
		// leaves them; writing the field alone left them tiled for the old band
		// and the rail painted over the pane beneath it, which is a finding
		// about this file rather than about tuios.
		m.SidebarSetCollapsed(!m.SidebarCollapsed)
	case fuzz.SidebarPosition:
		config.SidebarPosition = []string{"left", "right"}[a.A%2]
		m.applyAppearanceLive(true)
	case fuzz.OpenOverlay:
		f.openOverlay(a.A)
	case fuzz.CloseOverlay:
		f.send(keyMsg("esc"))
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
		applyFuzzSetting(m, a.A, a.B)
	case fuzz.Tick:
		// A tick is where time passes, and passing time is what ends a resize
		// deferral. Update returns the settle as a command and nothing here
		// runs commands, so the timer is delivered by hand; without it the
		// deferral stays fresh forever, every retile takes the visual-only
		// branch, and no pane is ever told its real size. Weighting Tick below
		// the actions that arm the deferral keeps the storm case reachable.
		if m.viewportResizing {
			f.send(ViewportResizeSettledMsg{Gen: m.viewportResizeGen})
		}
		f.send(TickerMsg(fuzzClock()))
	case fuzz.Guest:
		f.guestWrite(a.S)
	case fuzz.AltScreen:
		if a.A%2 == 1 {
			f.guestWrite("\x1b[?1049h\x1b[2J\x1b[H")
		} else {
			f.guestWrite("\x1b[?1049l")
		}
	case fuzz.Burst:
		f.guestWrite(burstText(a.A))
	case fuzz.SecondClient, fuzz.DaemonRestart:
		// Both need a daemon on the far end of a socket, and in process there
		// is none: the model owns its panes directly. They are carried in the
		// shared alphabet so a PTY finding replays here as far as it can, and
		// the PTY target is where they do their work.
	}
	return nil
}

// guestWrite feeds bytes to the focused pane's emulator the way its own program
// would.
func (f *fuzzOS) guestWrite(s string) {
	w := f.m.GetFocusedWindow()
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

// clampFuzzWorkspace keeps the index inside the model's own range, since the
// out-of-range case is the model's own clamp and has its own test.
func clampFuzzWorkspace(n, count int) int {
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
func (f *fuzzOS) openOverlay(n int) {
	m := f.m
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

// applyFuzzSetting is the runtime-settings surface: the flips a user makes from
// the settings panel or the command palette while panes are on screen. Each one
// changes what a pane's frame looks like, which is why they belong in the
// alphabet rather than in the fixture.
func applyFuzzSetting(m *OS, which, value int) {
	on := value&1 != 0
	switch which % 12 {
	case 0:
		setSharedBorders(m, on)
	case 1:
		config.UseASCIIOnly = on
		m.applyAppearanceLive(true)
	case 2:
		// Every style the settings page offers, not two of them: the divider
		// glyph rule is a statement about all nine, and a style swapped under a
		// live layout is the sequence the table test cannot reach.
		if n := len(config.BorderStyles); n > 0 {
			config.BorderStyle = config.BorderStyles[value%n]
		}
		m.applyAppearanceLive(true)
	case 3:
		config.DockbarPosition = map[bool]string{true: "top", false: "bottom"}[on]
		m.applyAppearanceLive(true)
	case 4:
		config.DockbarPosition = map[bool]string{true: "hidden", false: "bottom"}[on]
		m.applyAppearanceLive(true)
	case 5:
		config.SessionColors = on
	case 6:
		config.SidebarEnabled = on
		m.applyAppearanceLive(true)
	case 7:
		config.SidebarWidth = map[bool]int{true: config.SidebarDefaultWidth, false: 20}[on]
		m.applyAppearanceLive(true)
	case 8:
		m.SidebarAgentFilter = map[bool]string{true: sidebarAgentsSession, false: sidebarAgentsAll}[on]
	case 9:
		m.SidebarAgentSort = map[bool]string{true: sidebarAgentsRecent, false: sidebarAgentsPriority}[on]
	case 10:
		m.SidebarPeek = map[bool]string{true: "other", false: ""}[on]
	default:
		m.SidebarFocused = on
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
