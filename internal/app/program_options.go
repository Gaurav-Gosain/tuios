package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
)

// ProgramOptions is the one list of Bubble Tea options a tuios client runs
// with. Every entry point spreads it into tea.NewProgram: the local client,
// the attach client, tape playback, the SSH server and tuios-web.
//
// It exists so an option added to one entry point cannot be missing from the
// others. A motion filter on the local client only would let a served client
// compose a full frame for every pointer move, which is where a frame costs
// most.
//
// What is in here is what is true of every client regardless of transport:
//
//   - The frame rate cap. One number for every client.
//   - No signal handler. Every tuios process already owns its signals: the
//     local commands install a handler that sends QuitMsg, and the servers
//     cancel a context that quits each session. A second handler per program
//     is at best redundant, and in a server it is one per connection.
//   - The motion filter, which is decided by the model's state and not by how
//     the bytes arrive.
//
// What is not in here is what differs by transport, and only that: the output
// writer, which the local client wraps and the servers serialize, and the
// input, environment and window size a server takes from its session. A
// transport supplies those itself, and it appends this list AFTER them. The
// order matters for one option: WithFilter is a single slot, both wish and sip
// install a filter of their own, and the last one wins. The filter here does
// what theirs do (see FilterMouseMotion), so it has to be the last one set.
//
// A served client passes WithCurrentSize, so a window size that arrives late
// cannot undo a newer one. See sizeFilter.
//
// TestEveryProgramTakesTheSharedOptions holds every construction site to this.
func ProgramOptions(opts ...ProgramOption) []tea.ProgramOption {
	var setup programSetup
	for _, opt := range opts {
		opt(&setup)
	}
	topts := []tea.ProgramOption{
		tea.WithFPS(config.DefaultFPS),
		tea.WithoutSignalHandler(),
		tea.WithFilter(sizeFilter(setup.currentSize, FilterMouseMotion)),
	}
	if setup.ownScreen {
		topts = append(topts, tea.WithoutRenderer())
	}
	if setup.startW > 0 && setup.startH > 0 {
		topts = append(topts, tea.WithWindowSize(setup.startW, setup.startH))
	}
	return topts
}

// ProgramOption is a part of ProgramOptions that one transport supplies.
type ProgramOption func(*programSetup)

type programSetup struct {
	currentSize    func() (width, height int)
	ownScreen      bool
	startW, startH int
}

// WithStartSize is the terminal's size when the program starts, for a
// program with no renderer to ask: Bubble Tea otherwise starts at 0x0.
func WithStartSize(width, height int) ProgramOption {
	return func(s *programSetup) { s.startW, s.startH = width, height }
}

// WithOwnScreen runs the program without Bubble Tea's renderer, for a model
// that writes its own frames (OS.UseScreen). False leaves the renderer on.
func WithOwnScreen(on bool) ProgramOption {
	return func(s *programSetup) { s.ownScreen = on }
}

// WithCurrentSize names where a transport keeps the size its client's
// terminal has now. The transport must store a new size there before it
// sends the WindowSizeMsg for it.
func WithCurrentSize(size func() (width, height int)) ProgramOption {
	return func(s *programSetup) { s.currentSize = size }
}

func sizeFilter(current func() (width, height int), next func(tea.Model, tea.Msg) tea.Msg) func(tea.Model, tea.Msg) tea.Msg {
	if current == nil {
		return next
	}
	return func(model tea.Model, msg tea.Msg) tea.Msg {
		if ws, ok := msg.(tea.WindowSizeMsg); ok {
			if w, h := current(); w > 0 && h > 0 {
				ws.Width, ws.Height = w, h
			}
			msg = ws
		}
		return next(model, msg)
	}
}

var eventDebugLog = debuglog.EventsEnabled

func debugLogEvent(m *OS, msg tea.Msg) {
	if !eventDebugLog() {
		return
	}

	mouseMode := "unknown"

	modeStr := "WinMgmt"
	if m.Mode == TerminalMode {
		modeStr = "Terminal"
	}

	var logLine string
	switch e := msg.(type) {
	case tea.KeyPressMsg:
		logLine = fmt.Sprintf("[%s] KEY mode=%s mouse=%s: key=%q code=%d mod=%d text=%q\n",
			time.Now().Format("15:04:05.000"), modeStr, mouseMode,
			e.String(), e.Code, e.Mod, e.Text)
	case tea.MouseMotionMsg:
		if m.Mode != TerminalMode {
			return
		}
		logLine = fmt.Sprintf("[%s] MOUSE_MOTION mode=%s mouse=%s: x=%d y=%d\n",
			time.Now().Format("15:04:05.000"), modeStr, mouseMode, e.X, e.Y)
	default:
		return
	}

	f, err := debuglog.Open(debuglog.EventsLogPath())
	if err != nil {
		return
	}
	_, _ = f.WriteString(logLine)
	_ = f.Close()
}

// FilterMouseMotion is the tea.WithFilter every client runs. It drops the
// mouse motion nothing on screen would react to, so a pointer sweep across an
// idle desktop composes no frames.
//
// It is a whitelist: a motion event reaches Update only through a clause here
// that names the feature waiting for it. A hover or a drag added downstream
// is dead until it has a clause.
//
// The filter is also the last one set on the program (see ProgramOptions), so
// it carries the one thing the transports' own filters do: a served client
// cannot be suspended, and wish and sip both turn SuspendMsg into ResumeMsg.
// That is done here for every remote client so replacing their filter loses
// nothing.
func FilterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	m, ok := model.(*OS)
	if !ok {
		return msg
	}

	if msg = m.dropConPTYShiftNUL(msg); msg == nil {
		return nil
	}

	if msg = m.rejoinSplitMouse(msg); msg == nil {
		return nil
	}

	msg = stripTouchReport(msg)

	msg = m.convertHostPixelMouse(msg)

	if _, ok := msg.(tea.SuspendMsg); ok && m.RemoteClient {
		return tea.ResumeMsg{}
	}
	if _, ok := msg.(tea.SuspendMsg); ok && m.screen != nil {
		m.screen.Release()
	}

	mm, ok := msg.(tea.MouseMotionMsg)
	if !ok {
		debugLogEvent(m, msg)
		return msg
	}

	debugLogEvent(m, msg)

	mouse := mm.Mouse()
	m.NotePointerSeen(mouse.X, mouse.Y)

	if m.Dragging || m.Resizing {
		return msg
	}

	if m.Inbox.ck.mouse.Armed || m.quickLook.mouse.Armed {
		return msg
	}

	if m.DnDActive() {
		return msg
	}

	if m.CtrlDragPending {
		return msg
	}

	movedCell := mouse.X != m.LastMouseX || mouse.Y != m.LastMouseY

	if m.Settings.ZenMode == config.ZenModeMouse && movedCell {
		return msg
	}

	if m.ShakeGestureOn() && mouse.X != m.LastMouseX {
		return msg
	}

	if m.SpotlightOn() && movedCell && m.spotlightConfig().FollowMode() == config.SpotlightFollowMouse {
		return msg
	}

	if m.ContextMenuActive() || m.PathPickerOpen() {
		return msg
	}

	if m.quickLook.barDrag && movedCell {
		return msg
	}

	if m.OverlayDragActive() {
		return msg
	}

	if m.DockWorkspaceDragActive() {
		return msg
	}

	if m.SidebarDragActive() || m.SidebarEdgeActive() || m.SidebarSplitActive() || m.FileDragActive() {
		return msg
	}
	if m.SidebarActive() {
		if m.SidebarHoverActive || m.SidebarBandContains(mouse.X, mouse.Y) {
			return msg
		}
	}

	if m.CaptureActive() {
		return msg
	}

	paneX, paneY, _ := m.PointerToLayout(mouse.X, mouse.Y)
	if movedCell {
		if m.LinkHoverActive() || m.PointerOverLink(paneX, paneY) {
			return msg
		}
	}

	if m.ShowScrollbackBrowser {
		return msg
	}

	if movedCell && (m.InDockBand(mouse.Y) || m.DockHoverActive()) && m.dockHoverChangesAt(mouse.X, mouse.Y) {
		return msg
	}

	if m.WindowButtonHoverActive() {
		return msg
	}
	if m.Settings.WindowButtonStyle == config.WindowButtonStyleDots && !m.Settings.HideWindowButtons {
		if m.WindowButtonContains(paneX, paneY) {
			return msg
		}
	}

	if m.Mode == TerminalMode {
		focusedWindow := m.GetFocusedWindow()
		if focusedWindow != nil && focusedWindow.Terminal != nil {
			if focusedWindow.Terminal.HasMouseMode() {
				return msg
			}
		}
	}

	if m.Settings.FocusFollowsMouse || m.AnyOverlayOpen() {
		return msg
	}

	return nil
}
