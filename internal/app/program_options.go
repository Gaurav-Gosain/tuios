package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// ProgramOptions is the one list of Bubble Tea options a tuios client runs
// with. Every entry point spreads it into tea.NewProgram: the local client,
// the attach client, tape playback, the SSH server and tuios-web.
//
// It exists because the list used to be typed out at five sites, and an
// option added to one was silently missing from the others. The motion filter
// below shipped on the local client only, so a served client composed a full
// frame for every pointer move, which is where a frame costs most.
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
// what theirs did (see FilterMouseMotion), so it has to be the last one set.
//
// TestEveryProgramTakesTheSharedOptions holds every construction site to this.
func ProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithFPS(config.MaxFPSCap),
		tea.WithoutSignalHandler(),
		tea.WithFilter(FilterMouseMotion),
	}
}

// eventDebugLog says whether TUIOS_DEBUG_INTERNAL=1 asked for the event log,
// read once on the first event rather than per event. The local command sets
// the variable at startup before the program runs, so the first event sees it.
var eventDebugLog = sync.OnceValue(func() bool {
	return os.Getenv("TUIOS_DEBUG_INTERNAL") == "1"
})

// debugLogEvent logs events to /tmp/tuios-events.log when TUIOS_DEBUG_INTERNAL=1.
// Only logs KeyPressMsg, MouseMotionMsg, and unknown events in TerminalMode
// to diagnose phantom keypresses (issue #78).
func debugLogEvent(m *OS, msg tea.Msg) {
	if !eventDebugLog() {
		return
	}

	// Note: we intentionally don't check HasMouseMode() here because
	// accessing the VT emulator's modes map from this goroutine causes
	// unrecoverable concurrent map read/write panics.
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
		// Only log in TerminalMode to avoid flooding
		if m.Mode != TerminalMode {
			return
		}
		logLine = fmt.Sprintf("[%s] MOUSE_MOTION mode=%s mouse=%s: x=%d y=%d\n",
			time.Now().Format("15:04:05.000"), modeStr, mouseMode, e.X, e.Y)
	default:
		return
	}

	f, err := os.OpenFile("/tmp/tuios-events.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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
// is dead until it has a clause, which is how the context menu's hover, the
// workspace pill drag and the shake gesture each shipped broken once.
//
// The filter is also the last one set on the program (see ProgramOptions), so
// it carries the one thing the transports' own filters did: a served client
// cannot be suspended, and wish and sip both turned SuspendMsg into ResumeMsg.
// That is done here for every remote client so replacing their filter loses
// nothing.
func FilterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	m, ok := model.(*OS)
	if !ok {
		return msg
	}

	if _, ok := msg.(tea.SuspendMsg); ok && m.RemoteClient {
		// There is no process at the far end to suspend. wish and sip answer
		// this the same way, and this filter stands in their slot.
		return tea.ResumeMsg{}
	}

	mm, ok := msg.(tea.MouseMotionMsg)
	if !ok {
		// Debug: log non-motion events (KeyPressMsg) before they reach Update
		debugLogEvent(m, msg)
		return msg
	}

	// Debug: log motion events
	debugLogEvent(m, msg)

	if m.Dragging || m.Resizing {
		return msg
	}

	mouse := mm.Mouse()
	movedCell := mouse.X != m.LastMouseX || mouse.Y != m.LastMouseY

	// A shake of the pointer toggles the spotlight, and the detector reads it
	// off bare motion. This filter is a whitelist, so the gesture is dead until
	// it has a clause here however well the detector works: that is the fault
	// the context menu's hover shipped with.
	//
	// Only a motion that reaches a different column is passed. The detector
	// reads the column and nothing else, so an event that moves the pointer
	// down a row carries it no news. The clause is false for every client that
	// has not asked for the gesture, which is how it ships, and the guard is
	// then exactly what it was.
	if m.ShakeGestureOn() && mouse.X != m.LastMouseX {
		return msg
	}

	// A beam that follows the pointer reads the pointer off LastMouseX/Y, which
	// only a motion that reached Update has set. It used to ride the link
	// clause below, so it stopped following over chrome and with links off.
	// Only a motion that reaches a different cell is passed, for the reason
	// the link clause gives: the same cell composes the same frame.
	if m.SpotlightOn() && movedCell && m.spotlightConfig().FollowMode() == config.SpotlightFollowMouse {
		return msg
	}

	// An open context menu highlights the row under the pointer, which it can
	// only do if it is told the pointer moved. This filter is a whitelist that
	// drops every motion event it does not recognise, so a hover handler added
	// anywhere downstream is dead until the event is allowed through here.
	if m.ContextMenuActive() {
		return msg
	}

	// Allow motion events while a floating overlay panel is being dragged.
	// Overlay drags don't set m.Dragging, so without this the motion events
	// that move the panel are filtered out and the drag never tracks.
	if m.OverlayDragActive() {
		return msg
	}

	// A workspace pill drag rides motion for the same reason a sidebar drag
	// does, and is whitelisted here for the same reason: the filter drops every
	// motion event it does not recognise, so the reorder would never see the
	// pointer leave the pill it was pressed on and every drag would arrive as a
	// plain click.
	if m.DockWorkspaceDragActive() {
		return msg
	}

	// A sidebar session drag rides motion the same way an overlay drag does,
	// and hover in the sidebar band needs motion to track the row under the
	// pointer. HoverActive keeps one more event flowing after the pointer
	// leaves the band, which is the event that clears the stale highlight.
	if m.SidebarDragActive() {
		return msg
	}
	if m.SidebarActive() {
		if m.SidebarHoverActive || m.SidebarBandContains(mouse.X, mouse.Y) {
			return msg
		}
	}

	// Capture mode hovers a window under the bare pointer and rubber-bands with
	// the button down, so it needs every motion event, held or not.
	if m.CaptureActive() {
		return msg
	}

	// A link in pane content underlines itself under the pointer, which needs
	// the motion that crosses it.
	//
	// This is the one clause whose target is not a rectangle the chrome drew.
	// Any cell a program printed may carry a link, so the test is the pane's
	// content box, and that is most of the screen. It therefore relaxes the CPU
	// guard the clauses above keep, and it is the only thing in this feature
	// that costs anything on a machine nobody is pointing at a link on. Two
	// things bound it:
	//
	//   - appearance.links = off makes both tests below false, and the filter
	//     then drops exactly what it dropped before any of this existed. That is
	//     the setting for anyone who would rather have the guard than the links.
	//   - Only a motion that reaches a different cell is passed. A pointer
	//     nudged inside one cell resolves to the same run and would compose an
	//     identical frame, so a sweep costs one event per cell crossed rather
	//     than one per event the host chose to send.
	//
	// LinkHoverActive keeps one more event flowing after the pointer leaves a
	// link, and that is the event that clears the underline.
	if movedCell {
		if m.LinkHoverActive() || m.PointerOverPaneContent(mouse.X, mouse.Y) {
			return msg
		}
	}

	// Allow motion events for scrollback browser drag-to-select
	if m.ShowScrollbackBrowser {
		return msg
	}

	// The dots window-control style draws three unlabelled discs and names them
	// by revealing their symbols under the pointer, so that style is dead
	// without its motion. Like the sidebar's, one more event is let through
	// after the pointer leaves the controls, and that is the event that clears
	// the reveal.
	if m.WindowButtonHoverActive() {
		return msg
	}
	if m.Settings.WindowButtonStyle == config.WindowButtonStyleDots && !m.Settings.HideWindowButtons {
		if m.WindowButtonContains(mouse.X, mouse.Y) {
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

	// Focus-follows-mouse moves pane focus from bare motion over a pane, and
	// every overlay menu highlights the row under the pointer as it moves. Both
	// live downstream of this whitelist, so both are dead unless their motion is
	// let through here. Without this, the opted-in focus-follows setting simply
	// does nothing and non-context menus never track the cursor.
	if m.Settings.FocusFollowsMouse || m.AnyOverlayOpen() {
		return msg
	}

	return nil
}
