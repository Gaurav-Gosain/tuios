package app

import (
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TickerMsg represents a periodic tick event for maintenance tasks
// (animations, dock stats, script playback). NOT for PTY-driven rendering.
type TickerMsg time.Time

// PTYDataMsg signals that one or more PTY readers have new output.
// This triggers re-rendering. Sent from the PTYDataChan listener.
type PTYDataMsg struct{}

// AutoScrollTickMsg triggers continuous scrolling while dragging outside content area.
type AutoScrollTickMsg struct{}

// WindowExitMsg signals that a terminal window process has exited.
// This is exported so it can be used by the input package.
type WindowExitMsg struct {
	WindowID string
}

// ClipboardSetMsg carries clipboard content from a guest app (OSC 52) to bubbletea.
type ClipboardSetMsg struct {
	Text string
}

// SessionCreatedMsg carries the result of creating a detached session off the
// Update goroutine. Name is the session that was asked for; Err is why it did
// not happen.
type SessionCreatedMsg struct {
	Name string
	Err  error
}

// SessionKilledMsg carries the result of killing a session this client is not
// attached to, off the Update goroutine. Label is what the session was called
// on screen, captured before the kill, since afterwards there is nothing left
// to look the name up from.
type SessionKilledMsg struct {
	Label string
	Err   error
}

// ListenForSessionKill waits for a kill of another session to finish.
func ListenForSessionKill(ch chan SessionKilledMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return nil
		}
		return res
	}
}

// ListenForSessionCreate waits for a detached-session creation to finish.
func ListenForSessionCreate(ch chan SessionCreatedMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return nil
		}
		return res
	}
}

// ListenForClipboardSet creates a command that listens for OSC 52 clipboard set events.
func ListenForClipboardSet(ch chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return nil
		}
		return ClipboardSetMsg{Text: text}
	}
}

// ScriptCommandMsg represents a command from a tape script to be executed.
// This allows tape commands to be processed through the normal message handling flow.
type ScriptCommandMsg struct {
	Command *tape.Command
}

// RemoteCommandMsg represents a remote command from the CLI.
// This allows remote commands to be processed through the normal message handling flow.
type RemoteCommandMsg struct {
	CommandType  string   // "tape_command", "send_keys", "set_config", "tape_script"
	TapeCommand  string   // For tape commands (single command)
	TapeArgs     []string // Arguments for tape command
	TapeScript   string   // For tape_script (full script content)
	Keys         string   // For send_keys
	Literal      bool     // For send_keys (send to PTY)
	Raw          bool     // For send_keys (no splitting on space/comma)
	WindowTarget string   // For send_keys (target window by name or ID)
	ConfigPath   string   // For set_config
	ConfigValue  string   // For set_config
	RequestID    string   // For response tracking
}

// RemoteKeyMsg represents a single key to be processed from a remote send-keys command.
// Keys are sent one at a time to allow proper sequential processing.
type RemoteKeyMsg struct {
	Key           tea.KeyPressMsg   // The key to process
	RemainingKeys []tea.KeyPressMsg // Keys still to be processed
	RequestID     string            // For response tracking on last key
}

// RemoteKeysDoneMsg signals that all remote keys have been processed.
// This triggers a final cleanup/retile.
type RemoteKeysDoneMsg struct {
	RequestID string
}

// RemoteTapeCommandMsg represents a single tape command from a remote script.
// Commands are processed one at a time to allow proper sequential execution.
type RemoteTapeCommandMsg struct {
	Command           tape.Command   // The command to execute
	RemainingCommands []tape.Command // Commands still to be processed
	RequestID         string         // For response tracking on last command
	CommandIndex      int            // 0-based index of current command (for progress display)
	TotalCommands     int            // Total number of commands in script
}

// RemoteTapeScriptDoneMsg signals that all tape commands have been processed.
type RemoteTapeScriptDoneMsg struct {
	RequestID string
}

// Multi-client message types for daemon mode

// StateSyncMsg is sent when another client updates session state.
type StateSyncMsg struct {
	State       *session.SessionState
	TriggerType string
	SourceID    string
}

// ClientJoinedMsg is sent when another client joins the session.
type ClientJoinedMsg struct {
	ClientID    string
	ClientCount int
	Width       int
	Height      int
}

// ClientLeftMsg is sent when another client leaves the session.
type ClientLeftMsg struct {
	ClientID    string
	ClientCount int
}

// ClientEvent represents a multi-client notification delivered to the Bubble Tea
// event loop so the work happens on the program goroutine instead of the daemon
// read-loop goroutine.
type ClientEvent struct {
	Type        string // "joined", "left", "resize", or "refresh"
	ClientID    string
	ClientCount int
	Width       int    // "joined" and "resize"
	Height      int    // "joined" and "resize"
	Reason      string // "refresh"
	// Reserve is the session's agreed chrome reserve, on "resize".
	Reserve session.LayoutReserve
}

// SessionResizeMsg is sent when the effective session size changes (min of all clients).
type SessionResizeMsg struct {
	Width       int
	Height      int
	ClientCount int
	// Reserve is the chrome reserve every client of this session lays its panes
	// out around, settled by the daemon as the largest any client asks for. It
	// arrives with the size because the panes' box is the size less this.
	Reserve session.LayoutReserve
}

// ForceRefreshMsg is sent to force all clients to re-render.
type ForceRefreshMsg struct {
	Reason string
}

// DaemonDisconnectedMsg is sent when the daemon connection is lost unexpectedly
// (crash, reset, or framing desync). The app cannot recover the session, so it
// surfaces the reason and quits cleanly instead of hanging.
type DaemonDisconnectedMsg struct {
	Err error
}

// SessionEndedMsg is sent when the daemon reports that the attached session was
// terminated (killed from another client, from the CLI, or over the control
// plane). The session no longer exists, so there is nothing to detach from and
// nothing to reconnect to: the client must exit.
type SessionEndedMsg struct {
	// SessionName is the session that ended, as the daemon named it.
	SessionName string
	// Reason is the daemon's short explanation, when it gave one.
	Reason string
}

// ExitReason explains why the program stopped, so the caller can print an
// accurate message and choose an exit status. A client that quits because its
// session was destroyed must not report a normal detach.
type ExitReason int

const (
	// ExitNormal is a user-initiated quit or detach.
	ExitNormal ExitReason = iota
	// ExitSessionKilled means the attached session was terminated.
	ExitSessionKilled
	// ExitDaemonLost means the daemon connection was lost unrecoverably.
	ExitDaemonLost
)

// InputHandler is a function type that handles input messages.
// This allows the Update method to delegate to the input package without creating a circular dependency.
type InputHandler func(msg tea.Msg, o *OS) (tea.Model, tea.Cmd)

// inputHandler is the registered input handler function.
// This will be set by the main package to break the circular dependency.
// Atomic because every SSH connection handler registers it (with the same
// function) while other sessions' update loops are reading it.
var inputHandler atomic.Pointer[InputHandler]

// SetInputHandler registers the input handler function.
// This must be called during initialization before the Update loop runs.
func SetInputHandler(handler InputHandler) {
	inputHandler.Store(&handler)
}

// getInputHandler returns the registered input handler, or nil if none is set.
func getInputHandler() InputHandler {
	if h := inputHandler.Load(); h != nil {
		return *h
	}
	return nil
}

// Init initializes the TUIOS application and returns initial commands to run.
// It starts the tick timer and listens for window exits.
// Note: Mouse tracking, bracketed paste, and focus reporting are now configured
// in the View() method as per bubbletea v2.0.0-beta.5 API changes.
// reportConfigWarnings puts the config problems found at load time in front of
// the user. They are written to the in-app log (leader D l) rather than to
// stdout, because loading happens before the alternate screen is entered and
// anything printed then is wiped by the first frame. A notification points at
// the log so the problems are noticed rather than merely recorded.
func (m *OS) reportConfigWarnings() {
	if len(m.ConfigWarnings) == 0 {
		return
	}
	for _, warning := range m.ConfigWarnings {
		m.LogWarn("Config: %s", warning)
	}
	m.ShowNotification(
		fmt.Sprintf("%d config problem(s), see the log viewer", len(m.ConfigWarnings)),
		"warning",
		5*time.Second,
	)
}

func (m *OS) Init() tea.Cmd {
	m.reportConfigWarnings()

	cmds := []tea.Cmd{
		TickCmd(&m.Settings),
		ListenForWindowExits(m.WindowExitChan),
		ListenForPTYData(m.PTYDataChan),
		ListenForClipboardSet(m.PendingClipboardSet),
		ListenForSessionCreate(m.sessionCreateChan()),
		ListenForSessionKill(m.sessionKillChan()),
		ListenForNotification(m.ensureNotificationChan()),
		ListenForCwdChange(m.ensureCwdChangeChan()),
	}

	// The dock's components. Everything that used to hold the maintenance tick
	// at the normal frame rate for a clock is here instead, on its own deadline.
	if cmd := m.InitDockComponents(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Arm the screen saver's idle timer for a session nobody has typed in yet.
	// Without this a tuios left alone from the moment it opened would never
	// start one, because arming otherwise hangs off input.
	if cmd := m.armScreensaver(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Listen for state sync from other clients (daemon/SSH/web mode)
	if m.StateSyncChan != nil {
		cmds = append(cmds, ListenForStateSync(m.StateSyncChan))
	}

	// Listen for client join/leave events (daemon/SSH/web mode)
	if m.ClientEventChan != nil {
		cmds = append(cmds, ListenForClientEvents(m.ClientEventChan))
	}

	// Listen for the daemon events that end this client (SSH and web mode; the
	// local attach client Sends the same two messages into its program).
	if cmd := ListenForDaemonExit(m.daemonExitChan()); cmd != nil {
		cmds = append(cmds, cmd)
	}

	// Listen for verbs the daemon routed here (SSH and web mode; the local
	// attach client Sends into the program instead).
	if m.RemoteCommandChan != nil {
		cmds = append(cmds, ListenForRemoteCommands(m.RemoteCommandChan))
	}

	// If this is a restored daemon session, enable callbacks after a delay
	// This allows buffered PTY output to settle before callbacks start tracking changes
	if m.IsDaemonSession && m.RestoredFromState {
		cmds = append(cmds, EnableCallbacksAfterDelay())
		// Trigger alt screen redraws immediately to force apps like btop to redraw
		cmds = append(cmds, TriggerAltScreenRedrawCmd())
	}

	// Keep the foreign-session window cache warm so the sidebar can expand other
	// sessions. Kick once on attach, then repeat on an interval.
	if m.DaemonClient != nil {
		after, refresh := m.foreignSessionRefreshPlan()
		if refresh {
			cmds = append(cmds, refreshForeignSessionsCmd(m.DaemonClient))
		}
		cmds = append(cmds, foreignSessionRefreshTick(after))

		// One poll for the federated hosts. The answer says whether any are
		// configured, and a daemon with none never gets asked again.
		m.federationPolling = true
		cmds = append(cmds, refreshFederationCmd())
	}

	return tea.Batch(cmds...)
}

// ListenForWindowExits creates a command that listens for window process exits.
// It safely reads from the exit channel and converts exit signals to messages.
func ListenForWindowExits(exitChan chan string) tea.Cmd {
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		windowID, ok := <-exitChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		return WindowExitMsg{WindowID: windowID}
	}
}

// ListenForStateSync creates a command that listens for state sync from other clients.
// It safely reads from the sync channel and converts state to messages for the update loop.
func ListenForStateSync(syncChan chan *session.SessionState) tea.Cmd {
	if syncChan == nil {
		return nil
	}
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		state, ok := <-syncChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		return StateSyncMsg{State: state}
	}
}

// ListenForClientEvents creates a command that listens for client join/leave events.
// It safely reads from the event channel and converts events to messages for the update loop.
//
// It reads one event and stops, so every one of the four messages it can produce
// has to re-arm it. Two of them did not, and since all four share this one
// channel, the first session resize or force refresh stopped the others as well:
// a phone turned sideways got the columns it had before, because the effective
// size the daemon recalculated was sitting in a channel nobody was reading.
func ListenForClientEvents(eventChan chan ClientEvent) tea.Cmd {
	if eventChan == nil {
		return nil
	}
	return func() tea.Msg {
		// Safe channel read with protection against closed channel
		event, ok := <-eventChan
		if !ok {
			// Channel closed, return nil to stop listening
			return nil
		}
		switch event.Type {
		case "joined":
			return ClientJoinedMsg{
				ClientID:    event.ClientID,
				ClientCount: event.ClientCount,
				Width:       event.Width,
				Height:      event.Height,
			}
		case "resize":
			return SessionResizeMsg{
				Width:       event.Width,
				Height:      event.Height,
				ClientCount: event.ClientCount,
				Reserve:     event.Reserve,
			}
		case "refresh":
			return ForceRefreshMsg{Reason: event.Reason}
		default:
			return ClientLeftMsg{
				ClientID:    event.ClientID,
				ClientCount: event.ClientCount,
			}
		}
	}
}

// TickCmd creates a maintenance tick for animations, dock stats, and script playback.
// This runs at a low rate and does NOT drive PTY rendering.
func TickCmd(s *config.Settings) tea.Cmd {
	return tea.Tick(time.Second/time.Duration(s.NormalFPS), func(t time.Time) tea.Msg {
		return TickerMsg(t)
	})
}

// tickNeedsWork reports whether this maintenance tick has anything to do. It is
// the idle gate: false means no animation, interaction, script, notification,
// dock stat, marquee, or settling title is live, and one cheap atomic pass over
// the windows found no exited process, no unflushed output, and no stranded
// manipulation. Everything it checks is an O(1) flag or an atomic load, so the
// gate stays bounded no matter how many idle shells are open.
func (m *OS) tickNeedsWork() bool {
	if len(m.Animations) > 0 || m.InteractionMode || m.Dragging || m.Resizing ||
		m.PrefixActive || m.ScriptMode || len(m.Notifications) > 0 ||
		m.SidebarMarqueeActive() || m.TooltipPending() || m.sidebarTitlePending ||
		len(m.pendingAgentAlerts) > 0 || m.spotlightMotionPending {
		return true
	}
	// Zen mode (mouse): the borders melt away once the pointer sits still past
	// the reveal window, and come back the instant it moves again. The motion
	// event forces its own frame, but the timeout crossing has no event, so a
	// work tick that lands on the far side of the threshold must repaint.
	if m.Settings.ZenMode == config.ZenModeMouse && m.zenHidden != m.zenBordersHidden(false) {
		return true
	}
	// A moved daemon listing can carry a new title for a window this client
	// stopped watching. The per-window drift check below cannot see that, because
	// the local title it compares against is the one that froze. One atomic load.
	if m.DaemonClient != nil && m.DaemonClient.CacheGen() != m.sidebarTitleGen {
		return true
	}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		if w.ProcessExited() || w.HasNewOutput.Load() || w.IsBeingManipulated {
			return true
		}
		// A title that has drifted from what the rail shows needs a work tick to
		// adopt it. Keying the wake on HasNewOutput misses an isolated title-only
		// change on the focused pane: the render consumes that flag before any
		// tick observes it, so the rail would hold the stale title until the next
		// output. The compare is a cheap string check per window.
		if windowRowTitle(w) != m.railTitleShown(w) {
			return true
		}
	}
	return false
}

// IdleTickCmd creates a command that generates tick messages at 10 FPS.
// Used when the terminal has been idle for a sustained period to reduce CPU.
func IdleTickCmd() tea.Cmd {
	return tea.Tick(time.Second/time.Duration(config.IdleFPS), func(t time.Time) tea.Msg {
		return TickerMsg(t)
	})
}

// ListenForPTYData returns a Cmd that blocks until a PTY reader signals
// new data, then sends a PTYDataMsg to trigger re-rendering.
func autoScrollTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return AutoScrollTickMsg{}
	})
}

func ListenForPTYData(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return PTYDataMsg{}
	}
}

// EnableCallbacksMsg is sent after a delay to re-enable VT emulator callbacks
// after restoring a daemon session.
type EnableCallbacksMsg struct{}

// EnableCallbacksAfterDelay returns a command that waits briefly then sends
// a message to re-enable callbacks after buffered output has settled.
func EnableCallbacksAfterDelay() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return EnableCallbacksMsg{}
	})
}

// viewportResizeSettleDelay is how long the model waits for a terminal-resize
// storm to stop before it does the expensive half of a resize.
//
// Long enough that a drag of the terminal's own edge, which delivers one size
// per frame for as long as the pointer is down, never pays it mid-gesture;
// short enough that letting go feels immediate.
const viewportResizeSettleDelay = 120 * time.Millisecond

// ViewportResizeSettledMsg says no new terminal size has arrived for
// [viewportResizeSettleDelay], so the sizes recorded during the storm can be
// pushed through to the emulators, the PTYs and the daemon.
type ViewportResizeSettledMsg struct {
	// Gen is the resize generation this settle was armed for. A later resize
	// bumps the generation, which retires every settle already in flight.
	Gen uint64
}

func viewportResizeSettleCmd(gen uint64) tea.Cmd {
	return tea.Tick(viewportResizeSettleDelay, func(time.Time) tea.Msg {
		return ViewportResizeSettledMsg{Gen: gen}
	})
}

// interactionSettleDelay is how long content polling stays parked after a drag
// or resize ends. Shells redraw their prompt when the SIGWINCH lands, not when
// the pointer comes up, so resuming the moment the button is released polls a
// prompt that is still being written.
const interactionSettleDelay = 150 * time.Millisecond

// InteractionSettledMsg says that delay has passed and the interaction mode the
// gesture borrowed can go back. It travels as a message rather than as a
// goroutine that writes the model directly: two gestures inside the delay had
// two of those writing the same field from off the update loop.
type InteractionSettledMsg struct{}

// InteractionSettleCmd waits out [interactionSettleDelay]. One shot, armed by a
// gesture ending, so nothing is left ticking at idle.
func InteractionSettleCmd() tea.Cmd {
	return tea.Tick(interactionSettleDelay, func(time.Time) tea.Msg {
		return InteractionSettledMsg{}
	})
}

// TriggerAltScreenRedrawMsg triggers alt screen apps to redraw.
type TriggerAltScreenRedrawMsg struct{}

// TriggerAltScreenRedrawCmd returns a command that immediately triggers
// alt screen apps (vim, htop, btop) to redraw via SIGWINCH.
func TriggerAltScreenRedrawCmd() tea.Cmd {
	return func() tea.Msg {
		return TriggerAltScreenRedrawMsg{}
	}
}

// Session poll cadences. The client re-fetches the daemon's session list so a
// non-attached session's window tree, and the titles of its own windows it no
// longer subscribes to, stay current in the sidebar. The fast cadence runs while
// a consumer (sidebar or switcher) can show the result; otherwise the slow
// cadence is a fallback that keeps the cache from going stale without costing
// anything at true idle, where a lone-session client refreshes nothing at all.
const (
	foreignSessionRefreshActive = 3 * time.Second
	foreignSessionRefreshIdle   = 30 * time.Second
)

// ForeignSessionRefreshTickMsg fires to kick a background session-list refresh.
type ForeignSessionRefreshTickMsg struct{}

func foreignSessionRefreshTick(after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg {
		return ForeignSessionRefreshTickMsg{}
	})
}

// foreignSessionRefreshPlan decides whether the next poll should hit the daemon
// and how long to wait before re-arming. A consumer is on screen when the
// sidebar reserves columns or the session switcher is open; poll fast then, even
// for a lone session, because the listing is where the rail reads the titles of
// windows this client has unsubscribed from. Off screen, fall back to a slow
// cache-warming poll while foreign sessions exist, and do nothing at all for a
// lone session, which is what keeps a hidden sidebar free at idle.
func (m *OS) foreignSessionRefreshPlan() (after time.Duration, refresh bool) {
	if m.DaemonClient == nil {
		return foreignSessionRefreshIdle, false
	}
	if m.SidebarActive() || m.ShowSessionSwitcher {
		return foreignSessionRefreshActive, true
	}
	if m.DaemonClient.SessionCount() <= 1 {
		return foreignSessionRefreshIdle, false
	}
	return foreignSessionRefreshIdle, true
}

// refreshForeignSessionsCmd refreshes the cached session list off the UI
// goroutine so BuildSessionTree, which must never block, can read foreign
// sessions' windows from the cache. A blocking refresh on the UI goroutine once
// froze the client while the daemon was busy; a Cmd runs in its own goroutine,
// and TryRefreshSessionList drops the request if one is already in flight.
func refreshForeignSessionsCmd(client *session.TUIClient) tea.Cmd {
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		client.TryRefreshSessionList()
		return nil
	}
}

// Update handles all incoming messages and updates the application state.
//
// It is a thin wrapper over handleMsg so that one thing can be asked after
// every message rather than at the end of each of the fifty handlers: does the
// rail's files section still agree with the focused pane's directory. The focus
// moves from key handlers, mouse handlers, session switches, window closes and
// state sync, and a hook in each of those would be a hook missing from the next
// one somebody writes. One comparison, once, is the whole cost, and it answers
// nil without allocating for a client whose rail has no files section.
func (m *OS) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.applyScrollAnchors()
	model, cmd := m.handleMsg(msg)
	m.recordScrollAnchors()
	// Asked again after the handler, not only before it, because the handler
	// itself is one of the things that lengthens a pane's history: a workspace
	// switch primes every pane it shows from the daemon's copy. Deriving only
	// on the next message would draw one frame with the viewport already slid.
	m.applyScrollAnchors()
	sync := m.FilesSyncCmd()
	if sync == nil {
		return model, cmd
	}
	if cmd == nil {
		return model, sync
	}
	return model, tea.Batch(cmd, sync)
}

// handleMsg is Update's body: one switch over every message the client can see.
func (m *OS) handleMsg(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	// The crash overlay owns the keyboard while it is up, and it is answered
	// here rather than in internal/input because everything the input package
	// consults to route a key is model state, and the model is what has just
	// failed. Three keys, read before any of it. See crash_overlay.go.
	if m.crash != nil {
		if key, ok := msg.(tea.KeyPressMsg); ok {
			if c, consumed := m.handleCrashKey(key); consumed {
				m.renderSkipped = false
				return m, c
			}
		}
	}

	// Per-event panic isolation. A panic in a single message handler (reachable
	// from malformed guest input, a bad tape/state-sync payload, or a rarely-hit
	// UI branch) must not tear down every window: bubbletea only recovers at the
	// top of Program.Run, where it restores the terminal and exits. Recover here,
	// put the crash overlay up, and return the model unchanged so the other
	// windows survive the bad event. Named returns let the deferred recover set
	// them.
	//
	// It used to recover, log and say nothing. LogError draws nothing on its own
	// (the log ring is behind leader D l), so the whole of what a user saw when
	// tuios hit an impossible state was a frame that did not update, and a bug
	// nobody can see is a bug nobody reports. The overlay is what changed.
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			m.NoteCrash("handling an event", r, stack)
			m.LogError("recovered panic in Update: %v (crash log: %s)\n%s", r, m.crashLogPath(), stack)
			model = m
			cmd = nil
		}
	}()

	// Any non-tick message invalidates the render cache
	if _, isTick := msg.(TickerMsg); !isTick {
		m.renderSkipped = false
	}

	switch msg := msg.(type) {
	case PTYDataMsg:
		// PTY output arrived  - mark dirty terminals and re-render immediately.
		// This is the primary render trigger, replacing tick-driven rendering.
		// Graphics refresh (kitty/sixel) happens in GetCanvas during View().
		m.MarkTerminalsWithNewContent()
		m.renderSkipped = false
		return m, ListenForPTYData(m.PTYDataChan)

	case PendingCopyMsg:
		return m, m.HandlePendingCopy(msg.Seq)

	case PasteTimeoutMsg:
		// The terminal never answered the clipboard query. Say so, because the
		// alternative is a paste key that looks broken. See clipboard_paste.go.
		if m.pasteTimedOut(msg) {
			m.ShowNotification("The terminal did not send the clipboard. Use your terminal's paste key.",
				"warning", m.Settings.NotificationDuration)
		}
		return m, nil

	case AutoScrollTickMsg:
		if !m.AutoScrollActive || m.AutoScrollDir == 0 {
			return m, nil
		}
		// Find the target window for auto-scrolling
		var w *terminal.Window
		if m.DraggedWindowIndex >= 0 && m.DraggedWindowIndex < len(m.Windows) {
			w = m.Windows[m.DraggedWindowIndex]
		}
		if w == nil {
			w = m.GetFocusedWindow()
		}
		if w != nil && w.CopyMode != nil && w.CopyMode.Active {
			cm := w.CopyMode
			for range 2 {
				if m.AutoScrollDir < 0 {
					// Scroll up: use same logic as moveUp (keep cursor mid-screen)
					midPoint := w.Height / 2
					if cm.CursorY > midPoint {
						cm.CursorY--
					} else if w.Terminal != nil && cm.ScrollOffset < w.Terminal.ScrollbackLen() {
						cm.ScrollOffset++
						w.ScrollbackOffset = cm.ScrollOffset
					} else if cm.CursorY > 0 {
						cm.CursorY--
					}
				} else {
					// Scroll down: use same logic as moveDown
					midPoint := w.Height / 2
					if cm.CursorY < midPoint {
						cm.CursorY++
					} else if cm.ScrollOffset > 0 {
						cm.ScrollOffset--
						w.ScrollbackOffset = cm.ScrollOffset
					} else if cm.CursorY < w.Height-3 {
						cm.CursorY++
					}
				}
			}
			// Update visual selection end
			if cm.State == terminal.CopyModeVisualChar || cm.State == terminal.CopyModeVisualLine {
				scrollbackLen := 0
				if w.Terminal != nil {
					scrollbackLen = w.Terminal.ScrollbackLen()
				}
				absY := scrollbackLen - cm.ScrollOffset + cm.CursorY
				cm.VisualEnd = terminal.Position{X: cm.CursorX, Y: absY}
			}
			w.Dirty = true
			w.ContentDirty = true
			w.InvalidateCache()
		}
		m.renderSkipped = false
		return m, autoScrollTick()

	case screensaverArmMsg:
		// The deferred idle timer fired. It is not proof of idleness, so the
		// handler re-checks the elapsed time and re-arms for the remainder.
		// Nothing renders here unless the saver actually starts.
		return m, m.handleScreensaverArm()

	case effectPreviewFrameMsg:
		// The effect picker's preview asking for its next frame. Like the saver
		// it drives itself, so the maintenance tick never reads picker state and
		// the idle path is the same whether a picker has ever been opened.
		return m, m.handleEffectPreviewFrame(msg)

	case screensaverFrameMsg:
		// The running saver asking for its next frame. It drives itself rather
		// than riding the maintenance tick, which is what keeps the idle path
		// untouched while it is merely armed.
		return m, m.handleScreensaverFrame()

	case TickerMsg:
		// Maintenance tick: animations, dock stats, script playback, process cleanup.
		// Does NOT trigger rendering unless animations/interactions are active.
		m.tickStats.Ticks++

		// Idle diet: when nothing periodic needs attention the per-tick scans have
		// no work, so skip them, hold the frame, and re-arm the slow tick. Process
		// exits and PTY output wake the loop through their own channels; the gate
		// itself does one cheap atomic pass to catch a pending exit, unflushed
		// output, or a stranded manipulation before deciding to sleep.
		if !m.tickNeedsWork() {
			m.renderSkipped = true
			return m, IdleTickCmd()
		}
		m.tickStats.Work++

		// Agent alerts whose settle window has closed. Done before the window
		// sweep below so an alert about a pane that exited this tick is dropped
		// by its own re-validation rather than by a nil window.
		m.flushDueAgentAlerts(time.Time(msg))

		// This ensures windows close even if the exit channel message was missed
		for i := len(m.Windows) - 1; i >= 0; i-- {
			if m.Windows[i].ProcessExited() {
				m.DeleteWindow(i)
			}
		}

		// Update animations. Whether any were running is captured BEFORE the
		// update, because the tick that finishes the last one is the tick that
		// matters most: Animation.Update leaves the VT alone while a transition
		// is in flight and only resizes it on the final tick, so that tick is
		// where the panes first render at the size they actually settled at. Ask
		// HasActiveAnimations afterwards and it answers "none", the frame-skip
		// below decides nothing needs drawing, and View serves the previous
		// frame: the last thing the user sees is the second-to-last animation
		// step, with every pane still drawn to its pre-animation size. Nothing
		// dirties the model after that, so the wrong frame is final.
		hadAnimations := m.HasActiveAnimations()
		m.UpdateAnimations()

		m.endGestureWithoutButton()

		// Retire interaction state no gesture is holding any more. A mouse
		// release is the only thing that clears IsBeingManipulated, and it is
		// lost whenever the pointer leaves the surface the events come from
		// mid-drag; the pane it was set on then renders its cached frame
		// forever.
		m.clearStaleManipulation()

		// Leave script mode once a finished script's completion indicator has
		// been shown. This re-arms Ctrl+P (the palette binding), which is
		// intercepted for script pause/resume while ScriptMode is set. It also
		// takes the indicator off screen, so the tick that does it has to draw:
		// nothing else is guaranteed to follow it.
		leftScriptMode := m.maybeExitFinishedScript()

		// Handle script playback if in script mode
		cmds := []tea.Cmd{TickCmd(&m.Settings)}
		if m.ScriptMode && !m.ScriptPaused && m.ScriptPlayer != nil {
			player := m.ScriptPlayer
			if !player.IsFinished() {
				// Wait for animations to complete before executing next command
				// This ensures visual consistency during script playback
				if m.HasActiveAnimations() {
					return m, TickCmd(&m.Settings)
				}

				// Hold the next command until a pane the previous one asked for
				// actually exists. In a daemon session Split and NewWindow only
				// send the request; the pane arrives later on a state push, and
				// until it does GetFocusedWindowID still names the pane the tape
				// was splitting away from, so the next Type would be typed into
				// the wrong pane.
				if !m.scriptPaneReady() {
					return m, TickCmd(&m.Settings)
				}

				// Check if we're blocking on a WaitUntilRegex condition from a
				// previously dispatched command.
				if m.ScriptWaitRegex != nil && !m.checkScriptWaitRegex() {
					// Condition not met and not timed out yet, keep waiting.
					return m, TickCmd(&m.Settings)
				}

				// Check if we're waiting for a sleep to finish
				if !m.ScriptSleepUntil.IsZero() && time.Now().Before(m.ScriptSleepUntil) {
					// Still waiting, don't advance yet
					return m, TickCmd(&m.Settings)
				}
				// Sleep finished or wasn't waiting, clear the sleep time
				m.ScriptSleepUntil = time.Time{}

				nextCmd := player.NextCommand()
				if nextCmd != nil {
					switch {
					// Sleep and its Wait alias both just delay playback.
					case (nextCmd.Type == tape.CommandTypeSleep || nextCmd.Type == tape.CommandTypeWait) && nextCmd.Delay > 0:
						// Set the sleep deadline
						m.ScriptSleepUntil = time.Now().Add(nextCmd.Delay)
						// Advance to next command but don't execute anything yet
						player.Advance()
					case nextCmd.Type == tape.CommandTypeWaitUntilRegex:
						// Arm the wait; playback blocks above until it resolves.
						// Don't dispatch it to the executor.
						m.startScriptWaitRegex(nextCmd)
						player.Advance()
					default:
						// Queue the command as a message instead of executing directly
						cmds = append(cmds, func() tea.Msg {
							return ScriptCommandMsg{Command: nextCmd}
						})
						// Advance to next command
						player.Advance()
					}
				}
			} else if player.IsFinished() {
				// Script just finished - record the time if not already set
				if m.ScriptFinishedTime.IsZero() {
					m.ScriptFinishedTime = time.Now()
					// A tape that builds a layout creates panes whose early output
					// (a split pane's shell prompt, an echo) can land before the
					// client subscribed, leaving an unfocused pane blank on screen
					// while the daemon holds the real content. Refresh every pane
					// from the daemon a beat after playback, once output has settled.
					if m.DaemonClient != nil {
						cmds = append(cmds, tea.Tick(tapeFinishRefreshDelay, func(time.Time) tea.Msg {
							return tapeLayoutRefreshMsg{}
						}))
					}
				}
			}
		}

		// Tick handles animations, interactions, whichkey, dock stats, and scripts.
		// PTY content changes are handled by PTYDataMsg (event-driven).
		hasAnimations := m.HasActiveAnimations()

		// Debounce rail titles: a burst of title changes adopts at most one per
		// interval. railTitleChanged means the rail must redraw; sidebarTitlePending
		// keeps the tick running until the final title settles.
		railTitleChanged := m.updateRailTitles()

		// Messages expire here, on the tick, and not inside render composition.
		//
		// They used to be retired by the renderer, which meant expiry could only
		// happen on a frame that was already being drawn for some other reason.
		// Once a session went quiet the last frame was served from the render
		// cache with the expired toast still painted on it, for as long as
		// nothing else happened; seventeen seconds was the recorded case, and a
		// project tape finishing correctly while its own banner covered the pane
		// it had just built was the symptom that found it.
		//
		// The tick that retires something draws one more frame so the message
		// actually leaves the screen, which is what notifExpired carries. A live
		// message is a reason to keep drawing regardless, because the hairline
		// under it is burning down and that is a per-frame change.
		notifExpired := m.CleanupNotifications()
		hasNotifications := len(m.Notifications) > 0
		needsScriptFrame := m.ScriptMode || leftScriptMode

		// Determine next tick rate
		var nextTick tea.Cmd
		if m.InteractionMode {
			// Motion events are coalesced to a frame budget in the input path,
			// so this tick is what flushes the most recent skipped position.
			// It runs at the normal rate: dropping it to a lower one used to
			// cost smoothness without limiting the motion flood, since motion
			// events drove their own renders regardless of the tick rate.
			nextTick = TickCmd(&m.Settings)
		} else if hasAnimations || m.PrefixActive || needsScriptFrame || hasNotifications || m.SidebarMarqueeActive() || m.TooltipPending() || m.sidebarTitlePending {
			nextTick = TickCmd(&m.Settings) // Normal FPS when things need periodic updates
		} else {
			nextTick = IdleTickCmd() // Slow idle tick (process cleanup, etc.)
		}
		cmds[0] = nextTick

		// Sync background windows that have accumulated output.
		// This catches windows whose HasNewOutput flag was preserved by
		// the throttling logic, ensuring they eventually render.
		hasBackgroundChanges := m.MarkTerminalsWithNewContent()

		// Zen mode (mouse): the borders melt once the pointer sits still past
		// the reveal window. tickNeedsWork already wakes this tick for the
		// crossing, but needsRender below has no zen term, so without this the
		// frame would be marked skippable and View() would serve the cached
		// frame with the borders still painted: zenHidden would never converge
		// and the tick would keep doing work at 10fps forever with nothing
		// visible to show for it. Marking the affected windows dirty also
		// rebuilds their CachedLayer, which still holds the bordered render
		// and is reused until the window is dirty.
		zenCrossed := m.Settings.ZenMode == config.ZenModeMouse && m.zenHidden != m.zenBordersHidden(false)
		if zenCrossed {
			m.markZenDirty()
		}

		// Render on tick if something periodic needs visual updates OR background windows changed
		needsRender := hadAnimations || hasAnimations || m.InteractionMode || m.PrefixActive ||
			hasBackgroundChanges || hasNotifications || notifExpired || leftScriptMode ||
			m.SidebarMarqueeActive() || m.TooltipPending() || railTitleChanged || zenCrossed ||
			m.spotlightMotionPending
		if !needsRender {
			m.renderSkipped = true
			if len(cmds) > 1 {
				return m, tea.Batch(cmds...)
			}
			return m, nextTick
		}
		m.renderSkipped = false
		m.spotlightMotionPending = false
		m.tickStats.Render++
		// This tick is about to draw, so it counts against the interaction frame
		// budget too. Without this a motion event landing just after a tick
		// would draw again immediately and the budget would not hold.
		if m.InteractionMode {
			m.lastInteractionRender = time.Now()
		}
		// Graphics refresh (kitty/sixel) happens in GetCanvas during View().

		if len(cmds) > 1 {
			return m, tea.Batch(cmds...)
		}
		return m, nextTick

	case dockComponentMsg:
		// A component said something. The frame is drawn only if what it said
		// changed the bar: an interval component polling a value that has not
		// moved costs an execution and no render at all, which is the whole
		// difference between this and the sixty-frames-a-second clock it
		// replaced.
		changed := m.handleDockComponent(msg)
		m.renderSkipped = !changed
		return m, ListenForDockComponents(m.dockEngine.Updates())

	case SessionCreatedMsg:
		cmd := ListenForSessionCreate(m.sessionCreateChan())
		if msg.Err != nil {
			m.ShowNotification("Create failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, cmd
		}
		if err := m.SwitchToSession(msg.Name); err != nil {
			m.ShowNotification("Switch failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, cmd
		}
		// A session the daemon just built carries AutoTiling false, and the switch
		// has already stamped that onto the client. Only a session created here is
		// tiled from the config: switching to one that already existed must keep
		// whatever layout the user left it in.
		m.applyStartupTiling()
		// The rail relays out around a session that did not exist last frame;
		// follow it by name so the cursor lands on it rather than on whatever
		// took its index.
		m.sidebarFollowSession = msg.Name
		return m, cmd

	case SessionKilledMsg:
		cmd := ListenForSessionKill(m.sessionKillChan())
		if msg.Err != nil {
			m.ShowNotification("Kill failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, cmd
		}
		m.ShowNotification("Killed session: "+msg.Label, "success", m.Settings.NotificationDuration)
		// The switcher lists a snapshot, so the row of a session that no longer
		// exists would sit there until the overlay was reopened.
		if m.ShowSessionSwitcher {
			m.SessionSwitcherItems = m.BuildSessionTree().Sessions
			if m.SessionSwitcherSelected >= len(m.SessionSwitcherItems) && m.SessionSwitcherSelected > 0 {
				m.SessionSwitcherSelected--
			}
		}
		return m, cmd

	case ClipboardSetMsg:
		// Propagate clipboard from guest app to host terminal
		return m, tea.Batch(
			tea.SetClipboard(msg.Text),
			ListenForClipboardSet(m.PendingClipboardSet),
		)

	case NotificationMsg:
		// Guest desktop notification or bell delivered off the PTY goroutine;
		// apply it here on the Bubble Tea goroutine where notification state is
		// owned, and where the pane it came from can be named safely.
		if msg.WindowID != "" {
			m.ShowNotificationFrom(msg.Message, msg.Type, msg.Duration,
				NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: msg.WindowID})
		} else {
			m.ShowNotification(msg.Message, msg.Type, msg.Duration)
		}
		return m, ListenForNotification(m.PendingNotification)

	case CwdChangedMsg:
		// OSC 7 working-directory change delivered off the PTY goroutine. Filter
		// to the focused window and schedule a debounced project-tape check. This
		// never executes anything; it only decides whether to look.
		cmd := m.onCwdChange(msg)
		return m, tea.Batch(cmd, ListenForCwdChange(m.PendingCwdChange))

	case fileListMsg:
		// A directory read that finished on its own goroutine. The handler drops
		// a reply whose generation has been superseded, so a slow mount cannot
		// overwrite the listing the user is looking at now.
		m.HandleFileList(msg)
		m.renderSkipped = false
		return m, nil

	case fileOpMsg:
		// A create, rename, delete or paste that finished on its own goroutine.
		// The handler says what happened and asks for the listing again through
		// the generation-stamped read, rather than editing the rows in memory.
		cmd := m.HandleFileOp(msg)
		m.renderSkipped = false
		return m, cmd

	case tapeDebounceMsg:
		// The focused cwd held still long enough; evaluate it for a project tape.
		m.handleTapeDebounce(msg.gen)
		return m, nil

	case WindowExitMsg:
		windowID := msg.WindowID
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
				m.DeleteWindow(i)
				break
			}
		}
		// Ensure we're in window management mode if no windows remain
		if len(m.Windows) == 0 {
			m.Mode = WindowManagementMode
		}
		return m, ListenForWindowExits(m.WindowExitChan)

	case EnableCallbacksMsg:
		// Re-enable VT emulator callbacks after buffered output has settled
		// This prevents the race condition where buffered PTY output overwrites
		// the restored IsAltScreen state
		m.LogInfo("[CALLBACKS] Re-enabling callbacks for all windows")
		for _, w := range m.Windows {
			if w.DaemonMode {
				w.EnableCallbacks()
				m.LogInfo("[CALLBACKS] Enabled for window %s (IsAltScreen=%v)", shortID(w.ID), w.IsAltScreen())
			}
		}
		return m, nil

	case ForeignSessionRefreshTickMsg:
		// The listing this tick refreshes is also the only thing that knows which
		// windows still exist anywhere, so the client's window-keyed state is
		// pruned here, against the listing the last refresh left behind.
		m.pruneWindowKeyedState()
		after, refresh := m.foreignSessionRefreshPlan()
		if !refresh {
			return m, foreignSessionRefreshTick(after)
		}
		return m, tea.Batch(refreshForeignSessionsCmd(m.DaemonClient), foreignSessionRefreshTick(after))

	case FederationHostsMsg:
		// Storing a snapshot is the whole handler. The network work happened in
		// the Cmd that produced this message, which is the rule this feature is
		// most at risk of breaking.
		m.applyFederationSnapshot(msg)
		m.sidebarCache.invalidate()
		if after, refresh := m.federationRefreshPlan(); refresh {
			return m, federationRefreshTick(after)
		}
		return m, nil

	case FederationRefreshTickMsg:
		after, refresh := m.federationRefreshPlan()
		if !refresh {
			return m, nil
		}
		return m, tea.Batch(refreshFederationCmd(), federationRefreshTick(after))

	case TriggerAltScreenRedrawMsg:
		// Force alt screen apps to redraw by sending resize (fake then real)
		// This triggers SIGWINCH which makes apps like vim/htop/btop redraw
		m.LogInfo("[REDRAW] Triggering alt screen redraws")
		for _, w := range m.Windows {
			if w.DaemonMode && w.IsAltScreen() && w.DaemonResizeFunc != nil {
				termWidth := w.ContentWidth()
				termHeight := w.ContentHeight()

				// Do a fake resize to slightly smaller, then back to real size
				// This ensures SIGWINCH is sent even if size "hasn't changed"
				fakeWidth := max(termWidth-1, 1)
				fakeHeight := max(termHeight-1, 1)

				_ = w.DaemonResizeFunc(fakeWidth, fakeHeight)
				_ = w.DaemonResizeFunc(termWidth, termHeight)
				// The PTY now carries this size, whatever the announcement
				// record said before. Record it: a record naming a size the PTY
				// does not have is what makes Resize skip the one announcement
				// that would have corrected the shell.
				w.SeedAnnouncedSize(termWidth, termHeight)

				w.InvalidateCache()
				w.MarkContentDirty()
				m.LogInfo("[REDRAW] Sent resize to window %s (%dx%d)", shortID(w.ID), termWidth, termHeight)
			}
		}
		m.MarkAllDirty()
		return m, nil

	case tea.KeyPressMsg, tea.KeyReleaseMsg, tea.MouseClickMsg, tea.MouseMotionMsg,
		tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.ClipboardMsg,
		tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		// Reset idle counter on any user input to restore full tick rate
		// A resize drag is live only while the pointer is still reporting. This
		// is what lets the deferral in resize_deferral.go expire when a mouse
		// release is lost, without a timeout that would cut short a slow drag:
		// any further motion refreshes it.
		switch mm := msg.(type) {
		case tea.MouseWheelMsg:
			m.notePointerEvent(time.Now())
		case tea.MouseClickMsg:
			m.notePointerEvent(time.Now())
			m.pointerDown = true
		case tea.MouseReleaseMsg:
			m.notePointerEvent(time.Now())
			m.pointerDown = false
		case tea.MouseMotionMsg:
			m.notePointerEvent(time.Now())
			// Motion names the buttons still held, so it is the most current
			// answer to whether one is, and it corrects a press whose own event
			// never arrived.
			m.pointerDown = mm.Button != tea.MouseNone
			// Motion with no button held while a drag is supposedly in
			// progress means the release happened somewhere we never heard
			// about, which is what a pointer leaving the surface the events
			// come from does: the button comes up out of reach and what comes
			// back is motion reporting no buttons. Left alone the pane stays
			// glued to the pointer and is dragged around a tiled layout with
			// nothing pressed, which is exactly how tiled panes end up at
			// overlapping positions.
			if (m.Dragging || m.Resizing) && mm.Button == tea.MouseNone {
				m.endLostGesture()
			}
		}
		// Any user input must produce a fresh frame. Without this a tick that
		// marked the frame skippable would make View return the cached content,
		// so state changed by this event (overlay selection, drag offset, etc.)
		// would not be drawn until some other redraw happened.
		m.renderSkipped = false

		// The screen saver eats the input that dismisses it. Anything else and
		// the first keystroke after walking back to the desk lands in a shell
		// nobody can see yet. The pointer bookkeeping above still ran, so a
		// button state that changed while the saver was up is not lost.
		if m.screensaver.active {
			return m, m.dismissScreensaver()
		}
		// Input restarts the idle countdown. The time is taken now, before the
		// handler runs, so a slow handler does not shorten the next wait.
		m.screensaver.lastInput = time.Now()

		// Delegate to the registered input handler
		handler := getInputHandler()
		if handler == nil {
			return m, m.armScreensaver()
		}
		newModel, cmd := handler(msg, m)
		// Arming happens after the handler, not before, because the keystroke
		// that switches the saver on in the settings page is itself an input
		// event: arming first would read the old setting and leave a session
		// that was just enabled with no timer until someone typed again.
		// armScreensaver starts one only when none is in flight, so holding a
		// key down does not queue a timer per repeat.
		if saverCmd := m.armScreensaver(); saverCmd != nil {
			cmd = tea.Batch(cmd, saverCmd)
		}

		// Motion events during a drag or resize arrive far faster than a frame
		// can be composed: the pointer emits one per cell it crosses, while a
		// frame costs milliseconds. Rendering every one builds a backlog that
		// grows for as long as the drag lasts, which is why the layout trails
		// the pointer instead of merely being a frame behind it.
		//
		// Only the redundant intermediate frames are dropped, never the input.
		// The handler above has already applied this event's geometry, so the
		// model always reflects the newest pointer position; skipping the draw
		// just means the next draw shows a later position. The interaction tick
		// forces a render while InteractionMode is set, so a skipped motion is
		// always flushed within one tick, and mouse release is not a motion
		// event so the final position is drawn unconditionally.
		// The mouse-anchored spotlight is throttled for the same reason a drag
		// is: the beam moves with the pointer, so every motion event makes the
		// frame differ from the last one and the compose stops being thrown
		// away. Without this a fast swipe composes once per cell crossed.
		beamFollowsMouse := m.spotlight.on &&
			m.spotlightConfig().FollowMode() == config.SpotlightFollowMouse
		if _, isMotion := msg.(tea.MouseMotionMsg); isMotion && (m.InteractionMode || beamFollowsMouse) {
			now := time.Now()
			if now.Sub(m.lastInteractionRender) < time.Second/time.Duration(m.Settings.NormalFPS) {
				m.renderSkipped = true
				// A drag has the interaction tick to flush the position it
				// skipped. A beam following the pointer has no tick of its own,
				// and adding one would cost every idle client a wake-up for a
				// setting it is not using. This flag is the flush instead: it
				// is true only between a skipped move and the next frame, so a
				// client with the beam off, or with the pointer at rest, never
				// sees it and the idle tick stays idle.
				m.spotlightMotionPending = beamFollowsMouse
			} else {
				m.lastInteractionRender = now
			}
		}
		return newModel, cmd

	case tea.WindowSizeMsg:
		oldWidth, oldHeight := m.Width, m.Height
		// Clamp our widths and heights to 1, as to avoid any unforseen 'resolution' errors
		m.Width = max(1, msg.Width)
		m.Height = max(1, msg.Height)
		m.MarkAllDirty()
		// A resize is drawn immediately and finished later. Everything below
		// lays the panes out at the new size; the expensive half - resizing each
		// emulator's backing store for real, telling the PTY and the daemon,
		// and asking every guest to redraw - waits until the sizes stop
		// arriving. Without this a drag of the terminal's own edge pays that
		// whole bill once per delivered size.
		m.viewportResizing = true
		m.renderSkipped = false
		m.viewportResizeGen++
		// The timestamp, not the flag, is what keeps the deferral alive. The
		// settle below is the normal way it ends; noteResizeStep is what makes
		// sure it ends at all if the settle never arrives - which it does not
		// when a panic in this handler is recovered, since the recovery returns
		// a nil command and takes the settle with it.
		m.noteResizeStep(time.Now())
		settle := viewportResizeSettleCmd(m.viewportResizeGen)

		// Apply the one-shot [startup] preferences now that the real terminal
		// size is known: NewOS runs before the first WindowSizeMsg, so opening a
		// window or tiling there would place them against a zero-sized screen.
		if !m.startupApplied {
			m.startupApplied = true
			m.applyStartupPreferences()
			// Said once, on the first frame that has a screen to say it on.
			m.warnOnBuildMismatch()
		}

		// Notify daemon of our terminal size for multi-client size calculation
		// This allows the daemon to compute effective size = min(all clients)
		//
		// The chrome reserve rides the same message: the sidebar's breakpoints
		// are measured against the render width, so a viewport that moved can
		// have moved what this client keeps for itself as well.
		if m.IsDaemonSession && m.DaemonClient != nil {
			m.DaemonClient.SetOwnLayoutReserve(m.OwnLayoutReserve())
			_ = m.DaemonClient.NotifyTerminalSize(msg.Width, msg.Height)
		}

		// When restored from state, we need to retile if tiling is enabled
		// to properly fit windows to the new terminal size.
		// The BSP tree structure is preserved, only positions/sizes are recalculated.
		// However, if the size is the same (e.g., web reload), skip retiling to preserve layout.
		if m.RestoredFromState {
			m.RestoredFromState = false
			sizeChanged := oldWidth != msg.Width || oldHeight != msg.Height
			if sizeChanged {
				// In daemon mode, the previous implementation waited for
				// SessionResizeMsg before tiling. That broke when the effective
				// size didn't change (e.g. a web client reattaches to a session
				// whose cached min-of-all-clients matches or is larger than
				// the browser viewport)  - no SessionResizeMsg ever arrives and
				// the restored layout stays at the stale saved dimensions.
				// Tile using the browser's actual size now; if the daemon
				// later reports a different effective size via SessionResizeMsg,
				// the handler there will re-tile anyway.
				if m.IsDaemonSession && m.AutoTiling {
					m.LogInfo("[RESIZE] Daemon mode restore: tiling to %dx%d (was %dx%d)",
						msg.Width, msg.Height, oldWidth, oldHeight)
					// Fall back to this client's own viewport only when the
					// daemon has not said what the session's size is. It used to
					// be forced to the viewport unconditionally, on the reasoning
					// that a SessionResizeMsg would correct it - but the daemon
					// only announces a size that changed, so a client whose join
					// left the minimum where it was never heard anything, and
					// went on rendering at its own width inside a session sized
					// for someone smaller. The attach reply carries the effective
					// size, which is the answer here.
					if m.EffectiveWidth <= 0 || m.EffectiveHeight <= 0 {
						m.EffectiveWidth = msg.Width
						m.EffectiveHeight = msg.Height
					}
					m.TileAllWindows()
				} else if m.AutoTiling {
					// Non-daemon mode: tile immediately
					m.LogInfo("[RESIZE] Retiling restored session to fit new terminal size (%dx%d -> %dx%d)",
						oldWidth, oldHeight, msg.Width, msg.Height)
					m.TileAllWindows()
				} else {
					// In floating mode, scale windows proportionally if dimensions changed
					if oldWidth > 0 && oldHeight > 0 {
						m.LogInfo("[RESIZE] Scaling restored windows from %dx%d -> %dx%d",
							oldWidth, oldHeight, msg.Width, msg.Height)
						m.ScaleWindowsToTerminal(oldWidth, oldHeight, msg.Width, msg.Height)
					} else {
						// No previous size, just clamp to current size
						m.ClampWindowsToView()
					}
				}
			} else {
				m.LogInfo("[RESIZE] Restored session, same size (%dx%d), preserving layout", msg.Width, msg.Height)
			}

			// Flush PTY buffers for restored session resize
			m.FlushPTYBuffersAfterResize()

			// Clear and re-place kitty/sixel images after restore resize
			if m.KittyPassthrough != nil {
				m.KittyPassthrough.HideAllPlacements()
			}

			return m, settle
		}

		// Retile windows if in tiling mode
		if m.AutoTiling {
			m.TileAllWindows()
		} else if msg.Width < oldWidth || msg.Height < oldHeight {
			// Terminal got smaller in floating mode - clamp windows back into view
			m.ClampWindowsToView()
		}

		// NOTE: Don't HideAllPlacements on kitty here  - the delete+re-place cycle
		// can lose image data on some terminals. RefreshAllPlacements runs every
		// render and will reposition in place via `a=p` (the image data persists
		// across `d=i` deletes per the kitty protocol).

		// The PTY resize, the SIGWINCH it carries and the whole-screen redraw
		// every guest answers with land in ViewportResizeSettledMsg instead.
		return m, settle

	case ViewportResizeSettledMsg:
		if msg.Gen != m.viewportResizeGen {
			// A newer resize has already superseded this one; its own settle
			// will end the deferral, and ending it here would pay the expensive
			// half in the middle of the storm this exists to coalesce.
			return m, nil
		}
		// Deliberately not conditional on viewportResizing: this is the settle
		// for the newest resize, so whatever state the flag is in, the deferred
		// work is due now. Draining an empty PendingResizes costs nothing.
		m.endResizeDeferral()
		// A taller terminal shows more launcher rows, and the new ones have had
		// no icon asked for. The settle is the right moment for that rather than
		// every delivered size.
		return m, m.LauncherIconWork()

	case InteractionSettledMsg:
		// A gesture started inside the delay owns the mode now, and its own
		// release will hand it back.
		if !m.Dragging && !m.Resizing {
			m.InteractionMode = false
		}
		return m, nil

	case tea.MouseMsg:
		// Catch-all for any other mouse events to prevent them from leaking
		return m, nil

	case tea.FocusMsg:
		// Terminal gained focus
		// Could be used to refresh or resume operations
		return m, nil

	case tea.BlurMsg:
		// Terminal lost focus. A key held when the window went away will never
		// report its release, so the hold ends here rather than outliving it.
		m.EndHold()
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		// The host answered the Kitty keyboard protocol query, so tuios now knows
		// which of the things it asked for it actually got.
		//
		// Success is silent. A protocol handshake succeeding is not news to the
		// person using the terminal, and a notification for it spends the one
		// channel that exists for things they have to act on.
		first := m.KeyboardFlags == 0
		m.NoteKeyboardEnhancements(msg)
		// A hold key the terminal cannot support does have to say so, or the user
		// presses a key that does nothing and has no way to find out why.
		if reason := m.HoldModeUnsupportedReason(); reason != "" && first {
			m.ShowNotification(reason, "warning", m.Settings.NotificationDuration)
		}
		return m, nil

	// Multi-client daemon messages
	case StateSyncMsg:
		// Another client updated state - apply incrementally
		if msg.State != nil {
			// Track what changed for notifications
			oldWindowCount := len(m.Windows)
			oldWorkspace := m.CurrentWorkspace

			if err := m.ApplyStateSync(msg.State); err != nil {
				m.LogError("Failed to apply state sync: %v", err)
			} else {
				// The daemon-created startup window arrives here; if the user asked
				// to start in terminal mode, enter it now that there is a focused
				// window to type into.
				m.maybeEnterPendingTerminalMode()

				// Show notifications for significant changes
				newWindowCount := len(m.Windows)
				newWorkspace := m.CurrentWorkspace

				// Window count change notification
				if newWindowCount > oldWindowCount {
					m.ShowNotification(fmt.Sprintf("Window created (%d total)", newWindowCount), "info", 2*time.Second)
				} else if newWindowCount < oldWindowCount {
					m.ShowNotification(fmt.Sprintf("Window closed (%d remaining)", newWindowCount), "info", 2*time.Second)
				}

				// Workspace change notification
				if oldWorkspace != newWorkspace {
					m.ShowNotification(fmt.Sprintf("Switched to workspace %d", newWorkspace), "info", 2*time.Second)
				}

				// A rename by this client or any other arrives on this push, so
				// an open switcher follows it without being reopened.
				m.refreshSwitcherItems()

				// The rail travels with the session, so a sync can have folded
				// or widened this client's own rail, and the rail is chrome the
				// session's reserve is settled from. Said here rather than
				// inside the sync, where nothing may speak at all.
				m.AnnounceLayoutReserve()
			}
		}
		// Continue listening for more state syncs
		return m, ListenForStateSync(m.StateSyncChan)

	case RenameAppliedMsg:
		if msg.Err != nil {
			what := msg.What
			if what == "" {
				what = "Rename"
			}
			m.ShowNotification(what+" failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, nil
		}
		// The attached session's new label rides the state push. A session this
		// client is not attached to gets no push, so its listing is refreshed
		// instead, off this goroutine.
		m.refreshSwitcherItems()
		return m, refreshForeignSessionsCmd(m.DaemonClient)

	case ClientJoinedMsg:
		// Another client joined the session
		m.ShowNotification(fmt.Sprintf("Client joined (%d connected)", msg.ClientCount), "info", 2*time.Second)
		// Continue listening for more client events
		return m, ListenForClientEvents(m.ClientEventChan)

	case ClientLeftMsg:
		// Another client left the session
		m.ShowNotification(fmt.Sprintf("Client left (%d connected)", msg.ClientCount), "info", 2*time.Second)
		// Continue listening for more client events
		return m, ListenForClientEvents(m.ClientEventChan)

	case SessionResizeMsg:
		// Effective session size changed (min of all clients)
		// Set the effective size - GetRenderWidth/Height will use min(terminal, effective)
		//
		// The agreed chrome reserve is half of the same answer and moves the
		// panes' box exactly as the size does, so a change to either one is a
		// re-layout. Before it was here a client folded its own chrome into the
		// box privately, and two clients with different chrome laid the same
		// panes out in different boxes.
		if m.EffectiveWidth != msg.Width || m.EffectiveHeight != msg.Height || m.SessionReserve != msg.Reserve {
			oldWidth, oldHeight := m.GetRenderWidth(), m.GetRenderHeight()
			// Only a size change is worth telling the user about. The reserve
			// moves when somebody opens a rail, which is a thing they can see
			// happening and not a thing to announce as a resize.
			sizeChanged := m.EffectiveWidth != msg.Width || m.EffectiveHeight != msg.Height
			m.EffectiveWidth = msg.Width
			m.EffectiveHeight = msg.Height
			m.SessionReserve = msg.Reserve
			m.MarkAllDirty()
			// Retile if the effective render size changed
			if m.AutoTiling {
				m.TileAllWindows()
			} else if m.GetRenderWidth() < oldWidth || m.GetRenderHeight() < oldHeight {
				// Floating panes keep their own geometry, so a session that
				// shrank around this client leaves them hanging over an edge
				// that has moved in. The same clamp the host terminal's own
				// resize does, for the resize this client did not cause.
				m.ClampWindowsToView()
			}
			// CRITICAL: Force sync all daemon PTY dimensions after tiling
			// This ensures PTYs match the new window dimensions even if no animation was created
			// (e.g., when window was already at target position but PTY had stale dimensions)
			m.SyncDaemonPTYDimensions()
			if sizeChanged {
				m.ShowNotification(fmt.Sprintf("Session size: %dx%d (%d clients)", msg.Width, msg.Height, msg.ClientCount), "info", 2*time.Second)
			}
		}
		// A session that changed size can have changed what this client keeps
		// for itself, because the sidebar's breakpoints are measured against the
		// render width. Announcing it here is what closes the loop, and it
		// closes: this client's own reserve is a function of the render width
		// alone, so the second round finds it unmoved and sends nothing.
		m.AnnounceLayoutReserve()
		// Continue listening for more client events
		return m, ListenForClientEvents(m.ClientEventChan)

	case ForceRefreshMsg:
		// Force re-render
		m.MarkAllDirty()
		// Continue listening for more client events
		return m, ListenForClientEvents(m.ClientEventChan)

	case DaemonDisconnectedMsg:
		// The daemon connection was lost and cannot be recovered; quit cleanly
		// so the user is not left staring at a frozen, unresponsive session.
		// After a deliberate quit the drop is the expected consequence of
		// killing the session, not a failure, so leave the reason alone.
		if !m.QuitRequested {
			m.ExitReason = ExitDaemonLost
		}
		return m, tea.Quit

	case SessionEndedMsg:
		// The session was destroyed underneath this client. Its windows are
		// gone and its PTYs are closed, so there is nothing left to render and
		// nothing to sync back. Record why and quit; the caller reports it and
		// exits non-zero.
		//
		// Unless this client asked for it: quitting a daemon session kills it,
		// and the daemon announces that back to us. Reporting the user's own
		// quit as an unexpected termination is what made a deliberate exit
		// print an error.
		if !m.QuitRequested {
			m.ExitReason = ExitSessionKilled
		}
		return m, tea.Quit

	case ConfigReloadedMsg:
		// Apply the appearance config parsed by the watcher goroutine here, on
		// the Bubble Tea goroutine, so the render loop never reads this
		// session's settings mid-write.
		if msg.Config != nil {
			config.ApplyAppearanceConfig(msg.Config, &m.Settings)
			// Retiled, not just repainted. The sidebar's width and side, the
			// dock's position and the pane gap all change how much room the
			// panes have, and this path had only ever repainted them: a gap
			// edited in the file moved the global and left the rectangles where
			// they were. The other two ways in already retile.
			m.applyAppearanceLive(true)
			// The file can move the sidebar and the dock, which is the chrome
			// the session's reserve is settled from.
			m.AnnounceLayoutReserve()
			// The dock section is rebuilt here too rather than only at startup.
			// A feature whose distribution story is "copy a file" that then
			// needed a restart to see the file would be most of the story
			// missing. Retile first: a component list that changed the dock's
			// height has to land before the panes are measured.
			cmd := m.ReloadDockComponents(msg.Config)
			m.MarkAllDirty()
			return m, cmd
		}
		return m, nil

	case screenshotResultMsg:
		// The render happened off this goroutine; the panel and the
		// notification are opened here, which is the only place they may be.
		cmd := m.HandleScreenshotResult(msg)
		m.MarkAllDirty()
		return m, cmd

	case screenshotCopiedMsg:
		// The clipboard helper answered. Nothing waited for it, which is the
		// point: this only decides where to say what it said.
		m.HandleScreenshotCopied(msg)
		m.MarkAllDirty()
		return m, nil

	case launcherIconsMsg:
		// Icons decoded off the Update goroutine, filed away here so the store
		// is only ever written on this one.
		m.applyLauncherIcons(msg)
		if m.ShowLauncher {
			m.MarkAllDirty()
		}
		return m, nil

	case PathAppsMsg:
		// A finished $PATH scan, handed over here so the launcher's rows are
		// only ever built on this goroutine.
		m.applyPathApps(msg.Entries)
		if !m.ShowLauncher {
			return m, nil
		}
		m.MarkAllDirty()
		// The rows changed, so the icons the visible ones want changed with
		// them.
		return m, m.LauncherIconWork()

	case settingsSaveFailedMsg:
		// The write happens in a command now, so a failure has to come back here
		// to be said out loud: the change is live either way, and the user needs
		// to know it will not outlive the session.
		m.ShowNotification("Could not save settings: "+msg.err.Error(), "error", 0)
		return m, nil

	case tapeLayoutRefreshMsg:
		// Fired a beat after a project tape finished. Re-fetch every pane's
		// content from the daemon and repaint, so panes created during the tape
		// (splits whose early output the client subscribed too late to catch)
		// show what actually ran.
		m.refreshAllPanesAfterTape()
		return m, nil

	case ScriptCommandMsg:
		// Execute tape command through the executor
		if executor := m.ScriptExecutor; executor != nil {
			if err := executor.Execute(msg.Command); err != nil {
				// Log error but continue playback
				m.ShowNotification(fmt.Sprintf("Script error: %v", err), "error", m.Settings.NotificationDuration)
			} else {
				// Tape playback mutates the model outside the input handler, so
				// it has to push the result like any other mutation would.
				m.SyncStateToDaemon()
			}
		}
		return m, nil

	case RemoteCommandMsg:
		// Execute remote command from CLI. The listener is re-armed on every
		// path out of this case, including the early returns, or a channel-fed
		// host would take exactly one routed verb per session.
		relisten := ListenForRemoteCommands(m.RemoteCommandChan)
		var err error
		// persistCmd writes a routed config change to disk, off this goroutine.
		var persistCmd tea.Cmd
		var cmd tea.Cmd
		var notificationMsg string
		var resultData map[string]any // Rich data to return

		switch msg.CommandType {
		case "tape_command":
			// Show what command is being run
			if len(msg.TapeArgs) > 0 {
				notificationMsg = fmt.Sprintf("Remote: %s %s", msg.TapeCommand, msg.TapeArgs[0])
			} else {
				notificationMsg = fmt.Sprintf("Remote: %s", msg.TapeCommand)
			}

			// Handle query/inspection commands first (these are read-only, no side effects)
			switch msg.TapeCommand {
			case "ListWindows":
				// Return list of all windows (read-only, no notification)
				resultData = m.GetWindowListData()
				// Send result directly and return early to avoid side effects
				if m.DaemonClient != nil && msg.RequestID != "" {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
				}
				return m, relisten
			case "GetSessionInfo":
				// Return session information (read-only, no notification)
				resultData = m.GetSessionInfoData()
				if m.DaemonClient != nil && msg.RequestID != "" {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
				}
				return m, relisten
			case "GetWindow":
				// Return info about a specific window (read-only, no notification)
				if len(msg.TapeArgs) > 0 {
					resultData, err = m.GetWindowData(msg.TapeArgs[0])
				} else {
					// Return focused window
					resultData, err = m.GetFocusedWindowData()
				}
				if m.DaemonClient != nil && msg.RequestID != "" {
					if err != nil {
						_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, err.Error())
					} else {
						_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
					}
				}
				return m, relisten
			default:
				// NewWindow and CloseWindow never arrive here: they are daemon
				// owned, so the daemon runs them itself and the client hears the
				// result as a state push like any other.
				tapeCmd := &tape.Command{
					Type: tape.CommandType(msg.TapeCommand),
					Args: msg.TapeArgs,
				}
				executor := tape.NewCommandExecutor(m)
				err = executor.Execute(tapeCmd)
			}
			// Retile if in tiling mode after command execution
			if m.AutoTiling {
				m.TileAllWindows()
			}
		case "send_keys":
			// Show what keys are being sent
			notificationMsg = fmt.Sprintf("Remote: send-keys %s", msg.Keys)

			// Parse keys and start sequential processing
			cmd, err = m.startRemoteSendKeys(msg.Keys, msg.Literal, msg.Raw, msg.WindowTarget, msg.RequestID)
			if err == nil {
				// Keys will be processed sequentially via RemoteKeyMsg
				// Show notification now, result will be sent after all keys processed
				m.ShowNotification(notificationMsg, "info", m.Settings.NotificationDuration)
				return m, tea.Batch(cmd, relisten)
			}
		// capture_pane never arrives here: the daemon renders the pane from its
		// own VT emulator whether or not a client is attached, because that is
		// the same rendering of the same PTY and routing it only added a way for
		// the two to disagree.
		case "set_config":
			// Show what config is being changed
			notificationMsg = fmt.Sprintf("Remote: set %s=%s", msg.ConfigPath, msg.ConfigValue)

			err = m.SetConfig(msg.ConfigPath, msg.ConfigValue)
			// Write it down, the way the settings panel does. Without this the
			// routed path changed the running session and nothing else, so
			// set-config reported success, get-config read the value back from
			// memory, and the file never grew the section. Every value set this
			// way was lost on the next restart, in every section, which is why
			// this reuses persistSettings rather than fixing one of them: it
			// carries the read-only gating for remote clients and the
			// stale-write sequencing with it.
			if err == nil {
				persistCmd = m.persistSettings()
			}
			// Retile if in tiling mode after config change
			if m.AutoTiling {
				m.TileAllWindows()
			}
		case "list_dock_components":
			// Read-only: what the bar is made of, what each cell last said, and
			// what its command last did. The last two are the debugging story
			// for a component that is not drawing, and the reason an agent can
			// verify a cell it just wrote rather than guess at it.
			resultData = map[string]any{
				"type":       "dock_component_list",
				"components": m.DockComponentsData(),
			}
			if m.DaemonClient != nil && msg.RequestID != "" {
				if sendErr := m.DaemonClient.SendCommandResultWithData(
					msg.RequestID, true, "command executed", resultData); sendErr != nil {
					// A discarded encode error here is a ten second timeout at
					// the caller with nothing to go on, which is exactly how
					// this was found.
					m.LogError("dock: could not answer list-dock-components: %v", sendErr)
				}
			}
			return m, relisten
		case "list_hooks":
			// Read-only: what this client's hook table holds and what each
			// command last did. The daemon merges it with its own table, so
			// list-hooks answers for both sides at once.
			resultData = map[string]any{
				"type":  "hook_list",
				"hooks": m.HookRows(),
			}
			if m.DaemonClient != nil && msg.RequestID != "" {
				if sendErr := m.DaemonClient.SendCommandResultWithData(
					msg.RequestID, true, "command executed", resultData); sendErr != nil {
					m.LogError("hooks: could not answer list-hooks: %v", sendErr)
				}
			}
			return m, relisten
		case "refresh_dock":
			// Re-run one component now, or every one when unnamed.
			name := ""
			if len(msg.TapeArgs) > 0 {
				name = msg.TapeArgs[0]
			}
			if err = m.RefreshDockComponent(name); err == nil {
				notificationMsg = "Remote: refresh-dock " + name
			}
		case "tape_script":
			// Execute a full tape script
			notificationMsg = "Remote: running a tape"

			// Parse and execute the tape script
			cmd, err = m.executeTapeScript(msg.TapeScript, msg.RequestID)
			if err == nil {
				// Script will be processed via RemoteTapeCommandMsg
				m.ShowNotification(notificationMsg, "info", m.Settings.NotificationDuration)
				return m, tea.Batch(cmd, relisten)
			}
		default:
			err = fmt.Errorf("unknown remote command type: %s", msg.CommandType)
		}

		m.MarkAllDirty()

		// A routed command mutates this model without going through the input
		// handler, which is the only other place a daemon session pushes state.
		// Without this the mutation lives on the client alone and the daemon,
		// which answers every read verb, keeps reporting the pre-command state.
		// Push before the result is sent, so a caller that reads back
		// immediately after a successful command sees what it just did.
		if err == nil {
			m.SyncStateToDaemon()
		}

		// Show notification for the remote command
		if err != nil {
			m.ShowNotification(fmt.Sprintf("Remote error: %v", err), "error", m.Settings.NotificationDuration)
		} else if notificationMsg != "" {
			m.ShowNotification(notificationMsg, "info", m.Settings.NotificationDuration)
		}

		// Send result back if we have a daemon client
		if m.DaemonClient != nil && msg.RequestID != "" {
			if err != nil {
				_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, err.Error())
			} else {
				_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
			}
		}

		return m, tea.Batch(cmd, persistCmd, relisten)

	case RemoteKeyMsg:
		// Process a single key from a remote send-keys command
		var cmd tea.Cmd

		// Process this key through the input handler
		if handler := getInputHandler(); handler != nil {
			newModel, keyCmd := handler(msg.Key, m)
			if newOS, ok := newModel.(*OS); ok {
				m = newOS
			}
			cmd = keyCmd
		}

		// If there are more keys, schedule the next one
		if len(msg.RemainingKeys) > 0 {
			nextKey := msg.RemainingKeys[0]
			remaining := msg.RemainingKeys[1:]
			nextCmd := func() tea.Msg {
				return RemoteKeyMsg{
					Key:           nextKey,
					RemainingKeys: remaining,
					RequestID:     msg.RequestID,
				}
			}
			// Use Sequence to ensure keys are processed in order, not concurrently
			if cmd != nil {
				return m, tea.Sequence(cmd, nextCmd)
			}
			return m, nextCmd
		}

		// Last key - schedule cleanup
		doneCmd := func() tea.Msg {
			return RemoteKeysDoneMsg{RequestID: msg.RequestID}
		}
		if cmd != nil {
			return m, tea.Sequence(cmd, doneCmd)
		}
		return m, doneCmd

	case RemoteKeysDoneMsg:
		// All remote keys have been processed - do final cleanup
		// Re-enable animations
		m.ProcessingRemoteKeys = false
		m.Settings.AnimationsSuppressed = false

		if m.AutoTiling {
			// Clear the BSP tree for current workspace to force a full rebuild
			// This ensures consistent state after multiple rapid operations
			if m.WorkspaceTrees != nil {
				m.WorkspaceTrees[m.CurrentWorkspace] = nil
			}
			m.TileAllWindows()
		}
		m.MarkAllDirty()

		// Send result back
		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "keys sent")
		}

		return m, nil

	case RemoteTapeCommandMsg:
		// Process a single tape command from a remote script

		// Update progress tracking for display
		m.RemoteScriptIndex = msg.CommandIndex
		m.RemoteScriptTotal = msg.TotalCommands

		// Handle Sleep commands specially - they just wait
		if msg.Command.Type == tape.CommandTypeSleep && msg.Command.Delay > 0 {
			// For remote execution, we use tea.Tick to wait
			nextIndex := msg.CommandIndex + 1
			waitCmd := tea.Tick(msg.Command.Delay, func(t time.Time) tea.Msg {
				// After sleep, continue with remaining commands or done
				if len(msg.RemainingCommands) > 0 {
					nextCmd := msg.RemainingCommands[0]
					remaining := msg.RemainingCommands[1:]
					return RemoteTapeCommandMsg{
						Command:           nextCmd,
						RemainingCommands: remaining,
						RequestID:         msg.RequestID,
						CommandIndex:      nextIndex,
						TotalCommands:     msg.TotalCommands,
					}
				}
				return RemoteTapeScriptDoneMsg{RequestID: msg.RequestID}
			})
			return m, waitCmd
		}

		// Execute the tape command
		executor := tape.NewCommandExecutor(m)
		if err := executor.Execute(&msg.Command); err != nil {
			// Log error but continue with remaining commands
			m.ShowNotification(fmt.Sprintf("Script error: %v", err), "error", m.Settings.NotificationDuration)
		}

		// Retile if in tiling mode after command execution
		if m.AutoTiling {
			m.TileAllWindows()
		}

		// If there are more commands, schedule the next one with a delay
		// The delay allows the UI to render the current command's effects before moving on
		if len(msg.RemainingCommands) > 0 {
			nextCmd := msg.RemainingCommands[0]
			remaining := msg.RemainingCommands[1:]
			nextIndex := msg.CommandIndex + 1
			// Use tea.Tick with a delay to allow rendering to catch up
			// 50ms gives enough time for window creation and basic rendering
			nextCmdFunc := tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
				return RemoteTapeCommandMsg{
					Command:           nextCmd,
					RemainingCommands: remaining,
					RequestID:         msg.RequestID,
					CommandIndex:      nextIndex,
					TotalCommands:     msg.TotalCommands,
				}
			})
			return m, nextCmdFunc
		}

		// Last command - schedule cleanup with a delay for final render
		doneCmd := tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
			return RemoteTapeScriptDoneMsg{RequestID: msg.RequestID}
		})
		return m, doneCmd

	case RemoteTapeScriptDoneMsg:
		// All tape commands have been processed - do final cleanup
		// Re-enable animations
		m.ProcessingRemoteKeys = false
		m.Settings.AnimationsSuppressed = false

		// Mark script finish time for progress display
		m.ScriptFinishedTime = time.Now()

		// Update progress to show completion
		m.RemoteScriptIndex = m.RemoteScriptTotal

		if m.AutoTiling {
			// Clear the BSP tree for current workspace to force a full rebuild
			if m.WorkspaceTrees != nil {
				m.WorkspaceTrees[m.CurrentWorkspace] = nil
			}
			m.TileAllWindows()
		}
		m.MarkAllDirty()

		// Send result back
		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "script executed")
		}

		return m, nil

	}

	return m, nil
}
