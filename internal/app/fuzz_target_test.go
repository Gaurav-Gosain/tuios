package app

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type fuzzOS struct {
	m                 *OS
	told              map[string]*toldSize
	saved             configSnapshot
	dir               func() string
	seq               int
	drawable          map[string][2]int
	calls             map[string]int
	lastAction        fuzz.Action
	prevSignature     string
	prevRail          string
	settledDrawable   map[string][2]int
	movedSinceSettled map[string]bool
	settledCalls      map[string]int
	prevStateDir      func() string
	unsandbox         func()
	closed            bool
}

type configSnapshot struct {
	shared, ascii, sessionColors     bool
	anim                             string
	sidebarOn                        bool
	borderStyle, dockPos, sidebarPos string
	sidebarWidth                     int
}

func snapshotConfig() configSnapshot {
	return configSnapshot{
		shared: config.Global.SharedBorders, anim: config.Global.Motion,
		ascii: config.Global.UseASCIIOnly, sessionColors: config.Global.SessionColors,
		sidebarOn: config.Global.SidebarEnabled, borderStyle: config.Global.BorderStyle,
		dockPos: config.Global.DockbarPosition, sidebarPos: config.Global.SidebarPosition,
		sidebarWidth: config.Global.SidebarWidth,
	}
}

func (c configSnapshot) restore() {
	config.Global.SharedBorders, config.Global.Motion = c.shared, c.anim
	config.Global.UseASCIIOnly, config.Global.SessionColors = c.ascii, c.sessionColors
	config.Global.SidebarEnabled, config.Global.BorderStyle = c.sidebarOn, c.borderStyle
	config.Global.DockbarPosition, config.Global.SidebarPosition = c.dockPos, c.sidebarPos
	config.Global.SidebarWidth = c.sidebarWidth
}

func newFuzzTarget(stateDir string) (fuzz.Target, error) {
	return &fuzzOS{saved: snapshotConfig(), dir: func() string { return stateDir }}, nil
}

const (
	fuzzWidth  = 120
	fuzzHeight = 40
	fuzzPanes  = 3
)

func (f *fuzzOS) Reset() error {
	f.releaseWindows()
	f.saved.restore()

	config.Global.Motion = config.MotionNone
	config.Global.SharedBorders = false
	config.Global.UseASCIIOnly = false
	config.Global.BorderStyle = "rounded"
	config.Global.DockbarPosition = "bottom"
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	config.Global.SessionColors = true

	if f.prevStateDir == nil {
		f.prevStateDir = sidebarStateDir
	}
	sidebarStateDir = f.dir

	if fuzzInput == nil {
		return errors.New("no input handler: fuzz_input_test.go hands it over at init")
	}
	if f.unsandbox == nil {
		undo, err := fuzz.Sandbox(f.dir())
		if err != nil {
			return err
		}
		f.unsandbox = undo
	}

	cfg := config.DefaultConfig()
	m := NewOS(OSOptions{
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		NumWorkspaces:   9,
		Width:           fuzzWidth,
		Height:          fuzzHeight,
		SessionName:     "fuzz",
		ConfigReadOnly:  true,
	})
	SetInputHandler(fuzzInput)
	m.AutoTiling = true
	m.UseBSPLayout = true
	m.CurrentWorkspace = 1
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

func (f *fuzzOS) addPane() error {
	f.seq++
	id := fmt.Sprintf("fuzzpane%04d", f.seq)
	win := terminal.NewDaemonWindow(id, "fuzz", 0, 0, 60, 20, 0, "pty-"+id, f.m.PTYDataChan, config.DefaultScrollbackLines)
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
	f.m.Cleanup()
}

func (f *fuzzOS) Close() {
	if f.closed {
		return
	}
	f.closed = true
	f.releaseWindows()
	f.saved.restore()
	SetInputHandler(nil)
	if f.unsandbox != nil {
		f.unsandbox()
		f.unsandbox = nil
	}
	if f.prevStateDir != nil {
		sidebarStateDir = f.prevStateDir
		f.prevStateDir = nil
	}
}

func (f *fuzzOS) send(msg tea.Msg) {
	f.m.Update(msg)
	select {
	case <-f.m.PTYDataChan:
	default:
	}
}

func (f *fuzzOS) Apply(a fuzz.Action) error {
	f.lastAction = a
	if f.m.QuitRequested {
		return nil
	}
	f.drawable, f.calls = drawableSizes(f.m), callCounts(f.told)
	m := f.m

	switch a.Kind {
	case fuzz.Key:
		f.send(keyMsg(a.S))
	case fuzz.Chord:
		f.send(keyMsg(m.Settings.LeaderKey))
		f.noteMoves()
		f.send(keyMsg(a.S))
	case fuzz.Text:
		for _, r := range a.S {
			f.send(tea.KeyPressMsg{Code: r, Text: string(r)})
			f.noteMoves()
		}
	case fuzz.MousePress:
		f.send(tea.MouseClickMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseMotion:
		f.send(tea.MouseMotionMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
	case fuzz.MouseRelease:
		f.send(tea.MouseReleaseMsg{X: a.A, Y: a.B, Button: mouseButton(a.C)})
		f.send(InteractionSettledMsg{})
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
		m.applyAppearanceLive(true)
	case fuzz.OpenOverlay:
		f.openOverlay(a.A)
	case fuzz.CloseOverlay:
		f.send(keyMsg("esc"))
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
		applyFuzzSetting(m, a.A, a.B)
	case fuzz.Tick:
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
	}
	return nil
}

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

func burstText(n int) string {
	n = min(max(n, 0), 5000)
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "burst %d\r\n", i)
	}
	return b.String()
}

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

func applyFuzzSetting(m *OS, which, value int) {
	on := value&1 != 0
	switch which % 12 {
	case 0:
		setSharedBorders(m, on)
	case 1:
		m.Settings.UseASCIIOnly = on
		m.applyAppearanceLive(true)
	case 2:
		if n := len(config.BorderStyles); n > 0 {
			m.Settings.BorderStyle = config.BorderStyles[value%n]
		}
		m.applyAppearanceLive(true)
	case 3:
		m.Settings.DockbarPosition = map[bool]string{true: "top", false: "bottom"}[on]
		m.applyAppearanceLive(true)
	case 4:
		m.Settings.DockbarPosition = map[bool]string{true: "hidden", false: "bottom"}[on]
		m.applyAppearanceLive(true)
	case 5:
		m.Settings.SessionColors = on
	case 6:
		m.Settings.SidebarEnabled = on
		m.applyAppearanceLive(true)
	case 7:
		m.Settings.SidebarWidth = map[bool]int{true: config.SidebarDefaultWidth, false: 20}[on]
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
