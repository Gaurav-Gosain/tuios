package app

import (
	"fmt"
	"math"
	"runtime/debug"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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
	// WindowID is the pane that wrote it. Whether the write reaches the host
	// clipboard can depend on whether that pane has the focus.
	WindowID string
}

// SessionCreatedMsg carries the result of creating a detached session off the
// Update goroutine. Name is the session that was asked for; Err is why it did
// not happen.
type SessionCreatedMsg struct {
	Name string
	// Global says the session was created as a global one, which has no
	// windows: its first pane is the one the user picks a machine for, so the
	// picker is opened once the switch has landed.
	Global bool
	Err    error
	// Client and State are set when the session was made on this machine's
	// daemon from a client attached elsewhere (makeSessionHere). The
	// connection is already open and attached, so the handler adopts it
	// rather than switching over the current one.
	Client *session.TUIClient
	State  *session.SessionState
	// Host is the machine Client is connected to, for a switch-session to
	// another machine (switchToHostAsync). Empty means this machine.
	Host string
	// RequestID is the routed switch-session request this result answers.
	// The answer goes over the connection being left, before it is closed.
	RequestID string
	// Switched marks a switch to a session that may already exist, rather
	// than a session made here: [startup] applies only while nobody has
	// arranged it.
	Switched bool
	// Create says the switch asked for the session to be made. A session an
	// attach makes is empty, and switch-session's sessions get a first
	// window, as they do on this machine (finishHostSwitch).
	Create bool
	// OpenGen is the rail click this dial answers, or 0. See host_open.go.
	OpenGen uint64
}

// SessionKilledMsg carries the result of killing a session this client is not
// attached to, off the Update goroutine. Label is what the session was called
// on screen, captured before the kill, since afterwards there is nothing left
// to look the name up from.
type SessionKilledMsg struct {
	Label string
	Err   error
}

func listenOnce[T any](stop <-chan struct{}, ch <-chan T, wrap func(T) tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case v, ok := <-ch:
			if !ok {
				return nil
			}
			return wrap(v)
		case <-stop:
			return nil
		}
	}
}

func listenSessionKillUntil(stop <-chan struct{}, ch chan SessionKilledMsg) tea.Cmd {
	return listenOnce(stop, ch, func(res SessionKilledMsg) tea.Msg { return res })
}

func listenSessionCreateUntil(stop <-chan struct{}, ch chan SessionCreatedMsg) tea.Cmd {
	return listenOnce(stop, ch, func(res SessionCreatedMsg) tea.Msg { return res })
}

// ListenForClipboardSet creates a command that listens for OSC 52 clipboard set events.
func ListenForClipboardSet(ch chan ClipboardSetMsg) tea.Cmd {
	return listenClipboardSetUntil(nil, ch)
}

func listenClipboardSetUntil(stop <-chan struct{}, ch chan ClipboardSetMsg) tea.Cmd {
	return listenOnce(stop, ch, func(msg ClipboardSetMsg) tea.Msg { return msg })
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
	ConfigNoSave bool     // For set_config: apply without saving (set-config --no-save)
	RequestID    string   // For response tracking
	afterSync    uint64
	released     bool
}

type remoteSwitchSessionMsg struct{ name string }

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

// StateSyncMsg is a session state arriving from the daemon. SourceID names the
// peer whose push it carries, and is empty for a state the daemon sent on its
// own account: a mutation of its own, or the reconcile answer to this client's
// push.
type StateSyncMsg struct {
	State       *session.SessionState
	TriggerType string
	SourceID    string
	// Attach is the client's attach generation when the state arrived. See
	// TUIClient.AttachGeneration and QueueStateSync.
	Attach  uint64
	arrival uint64
}

// ClientJoinedMsg is sent when another client joins the session.
type ClientJoinedMsg struct {
	ClientID    string
	ClientCount int
	Width       int
	Height      int
	Attach      uint64 // see ClientEvent.Attach
}

// ClientLeftMsg is sent when another client leaves the session.
type ClientLeftMsg struct {
	ClientID    string
	ClientCount int
	Attach      uint64 // see ClientEvent.Attach
}

// PasteRefusedMsg says a paste did not reach its pane. Message is one of the
// session.PasteRefused texts. See session/paste_retry.go.
type PasteRefusedMsg struct{ Message string }

// ClientEvent represents a multi-client notification delivered to the Bubble Tea
// event loop so the work happens on the program goroutine instead of the daemon
// read-loop goroutine.
type ClientEvent struct {
	Type        string // "joined", "left", "resize", "refresh", "agent-mail", "agent-mail-load", "inbox-rewatch", "hosts-changed" or "paste-refused"
	ClientID    string
	ClientCount int
	Width       int    // "joined" and "resize"
	Height      int    // "joined" and "resize"
	Reason      string // why: a "refresh" reason, or the "paste-refused" text
	// Reserve is the session's agreed chrome reserve, on "resize".
	Reserve session.LayoutReserve
	// Generation is the layout generation of a "resize". See
	// session/layout_gen.go.
	Generation uint64
	// Mail is the push behind an "agent-mail" event.
	Mail session.AgentMailPayload
	// PaneMark is the push behind a "pane-mark" event.
	PaneMark session.PaneMarkPayload
	// PluginAsks is the push behind a "plugin-approvals" event.
	PluginAsks []session.PluginApprovalAsk
	// PluginUI is the push behind a "plugin-ui" event.
	PluginUI session.PluginUIPayload
	// Prompt is the push behind a "prompt" event.
	Prompt session.PromptPayload
	// Attach is the client's attach generation when a session's own event
	// arrived: "joined", "left", "resize" and "agent-mail". Zero is an event
	// with no stamp. See TUIClient.AttachGeneration and staleAttach.
	Attach uint64
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
	// Generation numbers the daemon's answer, newer answers higher. Zero is
	// a daemon that does not number them. See session/layout_gen.go.
	Generation uint64
	// Attach is the attach generation the answer arrived under. See
	// ClientEvent.Attach.
	Attach uint64
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
	// ExitHostLost means the connection through a host ended and there was no
	// session on this machine to come back to. The session on the host keeps
	// running.
	ExitHostLost
	// ExitNestedRefused means the daemon took this client off its session,
	// because the client's output reached a pane of that session. The
	// session keeps running.
	ExitNestedRefused
	// ExitDetached means the daemon took this client off its session because
	// another client attached with -d, single_client is on, or detach-client
	// named it. The session keeps running, and this is not a failure.
	ExitDetached
	// ExitDaemonUpgraded means the daemon closed this client's connection
	// because a live upgrade replaced its image, and the client did not take
	// the session back from the new image: it is not a local client, the new
	// image does not speak its protocol, or none answered in time. Every pane
	// keeps running, and attaching again continues the session. The version
	// it became is the client's UpgradedTo.
	ExitDaemonUpgraded
	// ExitRestarted means the person restarted the daemon from the app and
	// the new one answers. The entry point runs RestartExec to come back.
	ExitRestarted
	// ExitRestartFailed means a restart from the app failed after the old
	// daemon stopped. RestartFailure says what to do next.
	ExitRestartFailed
	// ExitUpgradeFollowed means a live upgrade gave the daemon a newer build
	// and this local client stops to run it. The entry point runs
	// UpgradeExec. See upgrade_follow.go.
	ExitUpgradeFollowed
)

// InputHandler is a function type that handles input messages.
// This allows the Update method to delegate to the input package without creating a circular dependency.
type InputHandler func(msg tea.Msg, o *OS) (tea.Model, tea.Cmd)

var inputHandler atomic.Pointer[InputHandler]

// SetInputHandler registers the input handler function.
// This must be called during initialization before the Update loop runs.
func SetInputHandler(handler InputHandler) {
	inputHandler.Store(&handler)
}

func getInputHandler() InputHandler {
	if h := inputHandler.Load(); h != nil {
		return *h
	}
	return nil
}

func (m *OS) reportConfigWarnings() {
	if caps := m.hostCaps(); caps != nil {
		for _, w := range caps.Warnings {
			if !slices.Contains(m.ConfigWarnings, w) {
				m.ConfigWarnings = append(m.ConfigWarnings, w)
			}
		}
	}
	for _, notice := range m.ConfigNotices {
		m.Log("INFO", "Config: %s", notice)
	}
	if len(m.ConfigWarnings) == 0 {
		if n := len(m.ConfigNotices); n > 0 {
			m.ShowNotification(
				plural.Count(n, "setting")+" "+plural.Word(n, "works", "work")+
					" differently here, see the log viewer",
				"info",
				5*time.Second,
			)
		}
		return
	}
	for _, warning := range m.ConfigWarnings {
		m.LogWarn("Config: %s", warning)
	}
	m.ShowNotification(
		plural.Count(len(m.ConfigWarnings), "config problem")+", see the log viewer",
		"warning",
		5*time.Second,
	)
}

// Init initializes the TUIOS application and returns initial commands to run.
// It starts the tick timer and listens for window exits.
// Note: Mouse tracking, bracketed paste, and focus reporting are now configured
// in the View() method as per bubbletea v2.0.0-beta.5 API changes.
func (m *OS) Init() tea.Cmd {
	m.reportConfigWarnings()
	if m.sidebarSessionEvents == nil {
		m.sidebarSessionEvents = make(chan sidebarSessionEvent, 64)
		m.sidebarSessionDone = make(chan struct{})
		for edge := range m.sidebarSessionStateEvents {
			m.sidebarSessionStateEvents[edge] = make(chan sidebarSessionEvent, 1)
		}
	}

	cmds := []tea.Cmd{
		m.listenSidebarSession(),
		m.refreshSidebarSessions(),
		m.armTick(m.framePeriod()),
		listenWindowExitsUntil(m.stopSignal(), m.WindowExitChan),
		listenPTYDataUntil(m.stopSignal(), m.PTYDataChan),
		listenClipboardSetUntil(m.stopSignal(), m.PendingClipboardSet),
		listenSessionCreateUntil(m.stopSignal(), m.sessionCreateChan()),
		listenAttachedHostsUntil(m.stopSignal(), m.attachedHostsChan()),
		listenSessionKillUntil(m.stopSignal(), m.sessionKillChan()),
		listenNotificationUntil(m.stopSignal(), m.ensureNotificationChan()),
		listenCwdChangeUntil(m.stopSignal(), m.ensureCwdChangeChan()),
		listenForFileChange(m.stopSignal(), m.fileWatchChan(), time.Time{}),
		listenNvimNavigationUntil(m.stopSignal(), m.ensureNvimNavigationChan()),
		listenDnDUntil(m.stopSignal(), m.ensureDnDInbox()),
	}

	if cmd := m.sixelProbe(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.hostProgramStatusProbe(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.hostColorQueries(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.InitDockComponents(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.armScreensaver(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.startConfigWatch(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.startOmarchyFollow(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if m.StateSyncChan != nil {
		cmds = append(cmds, listenStateSyncUntil(m.stopSignal(), m.StateSyncChan))
	}

	if m.ClientEventChan != nil {
		cmds = append(cmds, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan))
	}

	cmds = append(cmds, m.listenForTML())

	if m.pluginCellsIn != nil {
		cmds = append(cmds, m.listenPluginCells())
	}

	if cmd := m.agentMailLoad(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.checkAgentIntegrationCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.startInboxWatch(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := m.startTransfersWatch(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if cmd := listenDaemonExitUntil(m.stopSignal(), m.daemonExitChan()); cmd != nil {
		cmds = append(cmds, cmd)
	}

	if m.RemoteCommandChan != nil {
		cmds = append(cmds, listenRemoteCommandsUntil(m.stopSignal(), m.RemoteCommandChan))
	}

	if m.IsDaemonSession && m.RestoredFromState {
		cmds = append(cmds, EnableCallbacksAfterDelay())
		cmds = append(cmds, TriggerAltScreenRedrawCmd())
	}

	if m.DaemonClient != nil {
		after, refresh := m.foreignSessionRefreshPlan()
		if refresh {
			cmds = append(cmds, refreshForeignSessionsCmd(m.DaemonClient))
		}
		cmds = append(cmds, m.foreignSessionRefreshTick(after))

		m.federationPolling = true
		cmds = append(cmds, refreshFederationCmd())
	}

	return tea.Batch(cmds...)
}

func listenWindowExitsUntil(stop <-chan struct{}, exitChan chan string) tea.Cmd {
	return listenOnce(stop, exitChan, func(windowID string) tea.Msg { return WindowExitMsg{WindowID: windowID} })
}

func listenStateSyncUntil(stop <-chan struct{}, syncChan chan StateSyncMsg) tea.Cmd {
	return listenOnce(stop, syncChan, func(sync StateSyncMsg) tea.Msg {
		if hold := stateSyncTestHold(); hold > 0 {
			time.Sleep(hold)
		}
		return sync
	})
}

func listenClientEventsUntil(stop <-chan struct{}, eventChan chan ClientEvent) tea.Cmd {
	return listenOnce(stop, eventChan, clientEventMsg)
}

func clientEventMsg(event ClientEvent) tea.Msg {
	switch event.Type {
	case "joined":
		return ClientJoinedMsg{
			ClientID:    event.ClientID,
			ClientCount: event.ClientCount,
			Width:       event.Width,
			Height:      event.Height,
			Attach:      event.Attach,
		}
	case "resize":
		return SessionResizeMsg{
			Width:       event.Width,
			Height:      event.Height,
			ClientCount: event.ClientCount,
			Reserve:     event.Reserve,
			Generation:  event.Generation,
			Attach:      event.Attach,
		}
	case "refresh":
		return ForceRefreshMsg{Reason: event.Reason}
	case "agent-mail":
		return AgentMailMsg{Payload: event.Mail, Attach: event.Attach}
	case "agent-mail-load":
		return AgentMailLoadMsg{}
	case "inbox-rewatch":
		return InboxRewatchMsg{}
	case "hosts-changed":
		return HostsChangedMsg{}
	case "sessions-changed":
		return SessionsChangedMsg{}
	case "pane-mark":
		return PaneMarkMsg{Mark: event.PaneMark}
	case "plugin-approvals":
		return PluginApprovalsMsg{Asks: event.PluginAsks}
	case "plugin-ui":
		return PluginUIMsg{Payload: event.PluginUI}
	case "prompt":
		return PromptMsg{Payload: event.Prompt}
	case "paste-refused":
		return PasteRefusedMsg{Message: event.Reason}
	case "agent-mail-mark":
		var thread uint64
		if len(event.Mail.ReadIDs) > 0 {
			thread = event.Mail.ReadIDs[0]
		}
		return AgentMailMarkMsg{Thread: thread}
	default:
		return ClientLeftMsg{
			ClientID:    event.ClientID,
			ClientCount: event.ClientCount,
			Attach:      event.Attach,
		}
	}
}

func (m *OS) tickNeedsWork() bool {
	return m.tickAtFrameRate() || len(m.Notifications) > 0 ||
		len(m.pendingAgentAlerts) > 0 || m.programAlertHeld() || m.pointerMotionPending ||
		m.cockpitHoldDue(time.Now()) || m.tickPolledWork()
}

func (m *OS) tickAtFrameRate() bool {
	return m.InteractionMode || m.HasActiveAnimations() || m.PrefixActive || m.ScriptMode ||
		m.SidebarMarqueeActive() || m.TooltipPending() || m.sidebarTitlePending
}

func (m *OS) tickPolledWork() bool {
	if len(m.Animations) > 0 || m.Dragging || m.Resizing {
		return true
	}
	if m.staleAnnounceHold() {
		return true
	}
	if m.Settings.ZenMode == config.ZenModeMouse && m.zenHidden != m.zenBordersHidden(false) {
		return true
	}
	if m.DaemonClient != nil && m.DaemonClient.CacheGen() != m.sidebarTitleGen {
		return true
	}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		if w.ProcessExited() || w.HasNewOutput.Load() || w.HasGraphicsOutput.Load() || w.IsBeingManipulated {
			return true
		}
		if windowRowTitle(w) != m.railTitleShown(w) {
			return true
		}
	}
	return false
}

const notifTickSlack = time.Millisecond

func (m *OS) notifTickWait(now time.Time) time.Duration {
	frame := m.framePeriod()
	wait, ok := m.notifNextMove(now)
	if !ok {
		return frame
	}
	if wait >= notifNoMove {
		return notifNoMove
	}
	return max(wait+notifTickSlack, frame)
}

const notifNoMove = time.Duration(math.MaxInt64)

func autoScrollTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return AutoScrollTickMsg{}
	})
}

func listenPTYDataUntil(stop <-chan struct{}, ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ch:
			return PTYDataMsg{}
		case <-stop:
			return nil
		}
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

// TriggerAltScreenRedrawMsg redraws the panes on the alternate screen.
type TriggerAltScreenRedrawMsg struct{}

// TriggerAltScreenRedrawCmd returns a command that immediately redraws the
// panes on the alternate screen from the screens the attach restored.
func TriggerAltScreenRedrawCmd() tea.Cmd {
	return func() tea.Msg {
		return TriggerAltScreenRedrawMsg{}
	}
}

const (
	foreignSessionRefreshActive = 3 * time.Second
	foreignSessionRefreshIdle   = 30 * time.Second
)

// ForeignSessionRefreshTickMsg fires to kick a background session-list refresh.
type ForeignSessionRefreshTickMsg struct {
	// Gen is the timer generation this tick was armed under. A tick from an
	// older generation is dropped, which is what lets a re-plan retire a slow
	// timer without ending up with two of them.
	Gen uint64
}

// SessionsChangedMsg is the daemon's push that the session listing changed.
// A daemon that pushes it is not polled. See session/daemon_listing_push.go.
type SessionsChangedMsg struct{}

func (m *OS) foreignSessionRefreshTick(after time.Duration) tea.Cmd {
	m.foreignTickGen++
	if m.DaemonClient != nil {
		return nil
	}
	gen := m.foreignTickGen
	return m.tickUntilStop(after, func(time.Time) tea.Msg {
		return ForeignSessionRefreshTickMsg{Gen: gen}
	})
}

func (m *OS) foreignSessionReplanCmd() tea.Cmd {
	if !m.foreignSessionReplan {
		return nil
	}
	m.foreignSessionReplan = false
	if m.DaemonClient == nil {
		return nil
	}
	after, refresh := m.foreignSessionRefreshPlan()
	if !refresh {
		return m.foreignSessionRefreshTick(after)
	}
	return tea.Batch(refreshForeignSessionsCmd(m.DaemonClient), m.foreignSessionRefreshTick(after))
}

func (m *OS) foreignSessionRefreshPlan() (after time.Duration, refresh bool) {
	if m.DaemonClient == nil {
		return foreignSessionRefreshIdle, false
	}
	if m.SidebarActive() || m.gotoPicker.open {
		return foreignSessionRefreshActive, true
	}
	if m.DaemonClient.SessionCount() <= 1 {
		return foreignSessionRefreshIdle, false
	}
	return foreignSessionRefreshIdle, true
}

func refreshForeignSessionsCmd(client *session.TUIClient) tea.Cmd {
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		before := client.CacheGen()
		client.TryRefreshSessionList()
		if client.CacheGen() == before {
			return nil
		}
		return foreignSessionsChangedMsg{}
	}
}

type foreignSessionsChangedMsg struct{}

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
	defer m.settleNest()
	defer m.settleWindowModeOff()
	if _, ok := msg.(flushMsg); ok {
		m.handleFlush()
		m.renderSkipped = true
		return m, m.frameIdleTickCmd()
	}
	if _, ok := msg.(tickStatsDumpMsg); ok {
		m.DumpTickStats()
		m.renderSkipped = true
		return m, nil
	}
	if dt, ok := msg.(deadlineTickMsg); ok {
		if dt.gen != m.tick.gen {
			m.renderSkipped = true
			return m, nil
		}
		m.tick.due = time.Time{}
		msg = TickerMsg(dt.at)
	}
	model, cmd := m.update(msg)
	m.machineSheetAfter()
	if qc := m.quickLookPicture(); qc != nil {
		cmd = tea.Batch(cmd, qc)
	}
	if pc := m.pastePreviewPicture(); pc != nil {
		cmd = tea.Batch(cmd, pc)
	}
	if qc := m.quickLookWindow(); qc != nil {
		cmd = tea.Batch(cmd, qc)
	}
	if qc := m.quickLookPageRead(); qc != nil {
		cmd = tea.Batch(cmd, qc)
	}
	if qc := m.quickLookFilmRead(); qc != nil {
		cmd = tea.Batch(cmd, qc)
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseReleaseMsg, tea.WindowSizeMsg:
		cmd = tea.Batch(cmd, m.refreshSidebarSessions())
	}
	if hc := m.hostProgramStatusAfter(); hc != nil {
		cmd = tea.Batch(cmd, hc)
	}
	m.dndHostSync()
	if hc := m.dndHostFlush(); hc != nil {
		cmd = tea.Batch(cmd, hc)
	}
	if dc := m.dndDropDeadline(); dc != nil {
		cmd = tea.Batch(cmd, dc)
	}
	if m.frameRate.program != nil && (!m.renderSkipped || isPersonInput(msg) || m.flushWhileIdle(msg)) {
		cmd = tea.Batch(cmd, flushCmd)
	}
	return model, cmd
}

func (m *OS) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.applyScrollAnchors()
	switch {
	case isRemoteKey(msg):
		prev := m.ProcessingRemoteKeys
		m.ProcessingRemoteKeys = true
		defer func() { m.ProcessingRemoteKeys = prev }()
	case m.ProcessingRemoteKeys && isPersonInput(msg):
		m.ProcessingRemoteKeys = false
		defer func() { m.ProcessingRemoteKeys = true }()
	}
	if isPersonInput(msg) {
		m.commandFocusMoved = false
	} else if _, routed := msg.(RemoteCommandMsg); routed || m.ProcessingRemoteKeys {
		before := m.focusedWindowID()
		m.commandDriving = true
		defer func() {
			m.commandDriving = false
			if m.focusedWindowID() != before {
				m.commandFocusMoved = true
			}
		}()
	}
	m.msgClock = time.Now()
	m.quietFrame = false
	m.reportActivity(msg)
	m.rendererRestarted(msg)
	if isPersonInput(msg) {
		m.noteFrame()
		m.noteAnsweredInput(msg)
	} else if _, raw := msg.(tea.RawMsg); raw {
		m.noteFrame()
	}
	noteCmd := m.noteHostPixelMouse(msg)
	model, cmd := m.handleMsg(msg)
	if pixelCmd := m.hostPixelMouseCmd(msg); pixelCmd != nil || noteCmd != nil {
		cmd = tea.Batch(cmd, noteCmd, pixelCmd)
	}
	m.msgClock = time.Time{}
	m.settleChrome()
	m.reportFocusChange()
	m.reconcilePrevFocus()
	m.markSwitchSeen()
	m.noteGotoPlace()
	m.recordScrollAnchors()
	m.applyScrollAnchors()
	sync := m.FilesSyncCmd()
	gitSync := m.GitSyncCmd()
	var secondFiles, secondGit tea.Cmd
	if m.secondarySidebarWidth() > 0 {
		m.withSecondaryRail(func() bool {
			secondFiles, secondGit = m.FilesSyncCmd(), m.GitSyncCmd()
			return true
		})
	}
	dockSync := m.DockMetersSyncCmd()
	railSync := m.RailCustomSyncCmd()
	if pane := m.RailPaneSyncCmd(); pane != nil {
		railSync = tea.Batch(railSync, pane)
	}
	replan := m.foreignSessionReplanCmd()
	loading := m.loadingFrameCmd()
	if hide := m.subagentHideCmd(); hide != nil {
		loading = tea.Batch(loading, hide)
	}
	motion := m.motionCmd()
	agents := m.agentsSyncCmd()
	m.CockpitReplyCheck()
	m.CockpitCopyCheck()
	if m.ShowInbox || m.Inbox.ck.lastTop != CockpitBase {
		m.CockpitLayerCheck()
	}
	if side := m.CockpitSideWanted(); side != nil {
		agents = tea.Batch(agents, side)
	}
	if arts := m.CockpitArtifactsWanted(); arts != nil {
		agents = tea.Batch(agents, arts)
	}
	if cmp := m.cockpitCompareStale(); cmp != nil {
		agents = tea.Batch(agents, cmp)
	}
	if plan := m.restartPlanCmd(); plan != nil {
		agents = tea.Batch(agents, plan)
	}
	tick := m.tickWakeCmd()
	if sync == nil && replan == nil && gitSync == nil && secondFiles == nil && secondGit == nil && dockSync == nil && railSync == nil && loading == nil && motion == nil && agents == nil && tick == nil {
		return model, cmd
	}
	return model, tea.Batch(cmd, sync, replan, gitSync, secondFiles, secondGit, dockSync, railSync, loading, motion, agents, tick)
}

func (m *OS) handleMsg(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	if m.crash != nil {
		if key, ok := msg.(tea.KeyPressMsg); ok {
			if c, consumed := m.handleCrashKey(key); consumed {
				m.renderSkipped = false
				return m, c
			}
		}
	}

	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			m.NoteCrash("handling an event", r, stack)
			m.LogError("recovered panic in Update: %v (crash log: %s)\n%s", r, m.crashLogPath(), stack)
			model = m
			cmd = nil
		}
	}()

	if _, isTick := msg.(TickerMsg); !isTick {
		m.renderSkipped = false
	}

	if c, ok := m.handleHostColorMsg(msg); ok {
		return m, c
	}
	if m.handleHostVersion(msg) {
		return m, nil
	}
	if m.handleSixelProbe(msg) {
		return m, nil
	}
	if c, ok := m.handleHostProgramStatusMsg(msg); ok {
		return m, c
	}
	if m.handleHostDnD(msg) {
		return m, nil
	}

	switch msg := msg.(type) {
	case sidebarSessionNotice:
		cmd := m.handleSidebarSessionEvent(msg.sidebarSessionEvent)
		return m, tea.Batch(cmd, m.listenSidebarSession())
	case sidebarSessionEvent:
		return m, m.handleSidebarSessionEvent(msg)
	case PTYDataMsg:
		listen := listenPTYDataUntil(m.stopSignal(), m.PTYDataChan)
		open, changed, due := m.takePaneOutput(time.Now())
		gesture := m.InteractionMode || m.Dragging || m.Resizing
		m.renderSkipped = !changed && !(open && gesture)
		if due != nil {
			return m, tea.Batch(listen, due)
		}
		return m, listen

	case screenReleaseMsg:
		m.screen.Release()
		return m, nil

	case screenBackMsg:
		m.takeScreenBack()
		return m.Update(msg.inner)

	case frameDueMsg:
		m.frameRate.dueArmed = false
		_, changed, due := m.takePaneOutput(time.Now())
		m.renderSkipped = !changed
		return m, due

	case GitStateMsg:
		m.ApplyGitState(msg)
		return m, nil

	case PendingCopyMsg:
		return m, m.HandlePendingCopy(msg.Seq)

	case imageProbeMsg:
		return m, m.applyImageProbe(msg)

	case ImagePastedMsg:
		return m, m.applyImagePasted(msg)

	case pastePreviewExpireMsg:
		m.applyPastePreviewExpire(msg)
		return m, nil

	case replyImageMsg:
		m.applyReplyImage(msg)
		return m, m.replyPreviewCmd()

	case inboxAgentsTickMsg:
		return m, m.applyInboxAgentsTick(msg)

	case threadPageMsg:
		return m, m.applyThreadPage(msg)

	case threadRecapMsg:
		m.applyThreadRecap(msg)
		return m, nil

	case threadImageMsg:
		return m, m.applyThreadImage(msg)

	case threadGrewMsg:
		return m, m.applyThreadGrew(msg)

	case LearnTranscriptGrewMsg:
		return m, m.applyLearnTranscriptGrew(msg)

	case threadWatchEnded:
		m.applyThreadWatchEnded(msg)
		return m, m.InboxThreadFetch()

	case mentionFolderMsg:
		m.applyMentionFolder(msg)
		return m, nil

	case mermaidPicMsg:
		return m, m.applyMermaidPic(msg)

	case replyPreviewMsg:
		m.applyReplyPreview(msg)
		if m.mermaidCellsWait {
			m.mermaidCellsWait = false
			m.blockView++
			m.blocksChanged()
		}
		if ql := &m.quickLook; ql.open && len(ql.mdImgs) > 0 {
			ql.mdGen++
		}
		if m.kbPreviewShown() {
			m.cockpitTouch()
		}
		return m, nil

	case kbFileMsg:
		return m, m.applyKBFile(msg)

	case pathStatMsg:
		m.applyPathStats(msg)
		return m, nil

	case quickLookMsg:
		cmd := m.applyQuickLook(msg)
		m.renderSkipped = false
		return m, cmd

	case quickLookWindowMsg:
		cmd := m.applyQuickLookWindow(msg)
		m.renderSkipped = false
		return m, cmd

	case quickLookPageMsg:
		cmd := m.applyQuickLookPage(msg)
		m.renderSkipped = false
		return m, cmd

	case quickLookDetailsMsg:
		m.applyQuickLookDetails(msg)
		m.renderSkipped = false
		return m, nil

	case quickLookMDImageMsg:
		cmd := m.applyQuickLookMDImage(msg)
		m.renderSkipped = false
		return m, cmd

	case quickLookFilmMsg:
		cmd := m.applyQuickLookFilm(msg)
		m.renderSkipped = false
		return m, cmd

	case ckCheckpointsMsg:
		m.applyCheckpoints(msg)
		return m, nil
	case ckTaskMsg:
		m.applyCard(msg)
		return m, nil
	case ckTakeMsg:
		m.applyTake(msg)
		return m, nil
	case ckShipMsg:
		m.applyShip(msg)
		return m, nil
	case ckFarCardsMsg:
		m.applyFarCards(msg)
		return m, nil
	case ckDispatchMsg:
		m.applyDispatch(msg)
		return m, nil
	case ckCardMsg:
		m.applyCardReply(msg)
		return m, nil
	case ckCompareMsg:
		return m, m.applyCompare(msg)
	case ckFanActMsg:
		return m, m.applyFanAct(msg)
	case tmlPlacedMsg:
		m.applyTMLPlaced(msg)
		return m, nil
	case ckArtifactsMsg:
		m.applyCockpitArtifacts(msg)
		return m, nil
	case ckLivePrimeMsg:
		m.applyLivePrime(msg)
		return m, nil

	case PasteTimeoutMsg:
		if m.pasteTimedOut(msg) {
			m.ShowNotification("The terminal did not send the clipboard. Use your terminal's paste key.",
				"warning", m.Settings.NotificationDuration)
		}
		return m, nil

	case AutoScrollTickMsg:
		if m.ShowInbox && m.cockpitAutoScroll() {
			return m, autoScrollTick()
		}
		if !m.AutoScrollActive || m.AutoScrollDir == 0 {
			return m, nil
		}
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
					midPoint := w.Height / 2
					if cm.CursorY < midPoint {
						cm.CursorY++
					} else if cm.ScrollOffset > 0 {
						cm.ScrollOffset--
						w.ScrollbackOffset = cm.ScrollOffset
					} else if cm.CursorY < w.LastContentRow() {
						cm.CursorY++
					}
				}
			}
			if cm.State.InVisual() {
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
		return m, m.handleScreensaverArm()

	case effectPreviewFrameMsg:
		return m, m.handleEffectPreviewFrame(msg)

	case AgentReportMsg:
		if err := m.ReportAgentState(AgentReport(msg)); err != nil {
			m.LogWarn("agent report: %v", err)
		}
		return m, nil

	case PlayTapeMsg:
		cmd, err := m.PlayTape(msg.Name, msg.Script)
		if err != nil {
			m.ShowNotification(err.Error(), "warning", m.Settings.NotificationDuration)
		}
		return m, cmd

	case learnQuitMsg:
		m.handleLearnQuit()
		return m, nil

	case motionFrameMsg:
		return m, m.handleMotionFrame(msg)

	case ckHoldMsg:
		m.cockpitSettleHold(time.Now())
		return m, nil

	case TickerMsg:
		m.tickStats.Ticks++
		m.idleFrameTicker(time.Time(msg))
		m.releaseEmulatorScratch(time.Time(msg))

		if !m.tickNeedsWork() {
			m.renderSkipped = true
			return m, nil
		}
		m.tickStats.Work++

		m.cockpitSettleHold(time.Time(msg))

		m.flushDueAgentAlerts(time.Time(msg))
		m.flushProgramAlerts(time.Time(msg))

		for i := len(m.Windows) - 1; i >= 0; i-- {
			if m.Windows[i].ProcessExited() {
				m.DeleteWindow(i)
			}
		}

		hadAnimations := m.HasActiveAnimations()
		m.UpdateAnimations()

		m.endGestureWithoutButton()

		m.releaseStaleAnnounceHold()

		m.clearStaleManipulation()

		leftScriptMode := m.maybeExitFinishedScript()

		var cmds []tea.Cmd
		if cmd := m.stepScript(); cmd != nil {
			cmds = append(cmds, cmd)
		}

		hasAnimations := m.HasActiveAnimations()

		railTitleChanged := m.updateRailTitles()

		notifExpired := m.CleanupNotifications()

		_, hasBackgroundChanges, frameDue := m.takePaneOutput(time.Time(msg))
		if frameDue != nil {
			cmds = append(cmds, frameDue)
		}

		zenCrossed := m.Settings.ZenMode == config.ZenModeMouse && m.zenHidden != m.zenBordersHidden(false)
		if zenCrossed {
			m.markZenDirty()
		}

		needsRender := hadAnimations || hasAnimations || m.InteractionMode || m.PrefixActive ||
			hasBackgroundChanges || m.notifBurnMoved() || notifExpired || leftScriptMode ||
			m.SidebarMarqueeActive() || m.TooltipPending() || railTitleChanged || zenCrossed ||
			m.pointerMotionPending
		if !needsRender {
			m.renderSkipped = true
			return m, tea.Batch(cmds...)
		}
		m.quietFrame = !(hadAnimations || hasAnimations || m.InteractionMode || m.PrefixActive ||
			hasBackgroundChanges || leftScriptMode || m.SidebarMarqueeActive() || m.TooltipPending() ||
			railTitleChanged || zenCrossed || m.pointerMotionPending)
		m.renderSkipped = false
		m.pointerMotionPending = false
		m.tickStats.Render++
		if m.InteractionMode {
			m.lastInteractionRender = time.Now()
		}

		return m, tea.Batch(cmds...)

	case dockComponentMsg:
		if msg.From != m.dockEngine {
			m.renderSkipped = true
			return m, nil
		}
		changed := m.handleDockComponent(msg)
		m.renderSkipped = !changed
		m.quietFrame = changed
		return m, ListenForDockComponents(m.dockEngine)

	case AttachedHostsMsg:
		m.applyAttachedHosts(msg)
		return m, listenAttachedHostsUntil(m.stopSignal(), m.attachedHostsChan())

	case SessionCreatedMsg:
		cmd := listenSessionCreateUntil(m.stopSignal(), m.sessionCreateChan())
		if msg.OpenGen != 0 {
			return m, tea.Batch(cmd, m.finishHostOpen(msg))
		}
		if msg.Switched {
			m.finishHostSwitch(msg)
			return m, cmd
		}
		if msg.Err != nil {
			m.ShowNotification("Create failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, cmd
		}
		if msg.Client != nil {
			m.adoptHostClient(msg.Client, msg.State, federation.LocalHostName)
		} else if err := m.SwitchToSession(msg.Name); err != nil {
			m.ShowNotification("Switch failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, cmd
		}
		m.applyStartupToUnarranged()
		m.sidebarFollowSession = msg.Name
		if msg.Global {
			m.NewWindowHere()
		}
		return m, cmd

	case SessionKilledMsg:
		cmd := listenSessionKillUntil(m.stopSignal(), m.sessionKillChan())
		if msg.Err != nil {
			m.pendingWorktreeKill = false
			m.ShowNotification("Kill failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, cmd
		}
		if m.pendingWorktreeKill {
			m.pendingWorktreeKill = false
		} else {
			m.ShowNotification("Killed session: "+msg.Label, "success", m.Settings.NotificationDuration)
		}
		m.refreshGotoRows()
		return m, cmd

	case ClipboardSetMsg:
		return m, tea.Batch(
			m.paneClipboardWrite(msg),
			listenClipboardSetUntil(m.stopSignal(), m.PendingClipboardSet),
		)

	case linkOpenFailedMsg:
		return m, m.handleLinkOpenFailed(msg)

	case fileOpenSystemMsg:
		cmd := m.handleFileOpenSystem(msg)
		m.renderSkipped = false
		return m, cmd

	case farOpenPreparedMsg:
		cmd := m.handleFarOpenPrepared(msg)
		m.renderSkipped = false
		return m, cmd

	case farOpenEndedMsg:
		cmd := m.handleFarOpenEnded(msg)
		m.renderSkipped = false
		return m, cmd

	case fileOpenFailedMsg:
		m.handleFileOpenFailed(msg)
		m.renderSkipped = false
		return m, nil

	case NotificationMsg:
		if msg.Bell {
			if msg.WindowID != "" {
				m.noteWindowBell(msg.WindowID)
			}
			if !m.ringBell() {
				return m, listenNotificationUntil(m.stopSignal(), m.PendingNotification)
			}
		}
		if msg.WindowID != "" {
			m.ShowNotificationFrom(msg.Message, msg.Type, msg.Duration,
				NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: msg.WindowID})
		} else {
			m.ShowNotification(msg.Message, msg.Type, msg.Duration)
		}
		return m, listenNotificationUntil(m.stopSignal(), m.PendingNotification)

	case CwdChangedMsg:
		cmd := m.onCwdChange(msg)
		return m, tea.Batch(cmd, listenCwdChangeUntil(m.stopSignal(), m.PendingCwdChange))

	case fileDirChangedMsg:
		root, dirs := m.fileWatch.takeDirty()
		cmd := m.refreshChangedTree(dirs, root)
		if root || len(dirs) == 0 {
			cmd = tea.Batch(m.refreshChangedFolder(), cmd)
		}
		return m, tea.Batch(cmd, listenForFileChange(m.stopSignal(), m.fileWatchChan(), msg.at))
	case LearnFilesChangedMsg:
		return m, m.learnFilesChanged()
	case fileTreeListMsg:
		cmd := m.HandleFileTreeList(msg)
		m.renderSkipped = false
		return m, cmd
	case NvimNavigationMsg:
		m.onNvimNavigation(msg)
		return m, listenNvimNavigationUntil(m.stopSignal(), m.PendingNvimNavigation)
	case dndDropTimeoutMsg:
		live := m.dndDrag
		cmd := m.onDnDDropTimeout(msg)
		m.renderSkipped = m.dndDrag == live
		return m, cmd
	case dndHostWrittenMsg:
		m.dndHost.writing = false
		m.renderSkipped = true
		return m, nil
	case dndWakeMsg:
		m.handleDnDWake()
		m.renderSkipped = false
		return m, listenDnDUntil(m.stopSignal(), m.ensureDnDInbox())

	case fileListMsg:
		m.HandleFileList(msg)
		m.renderSkipped = false
		return m, nil

	case pathPickerListMsg:
		m.HandlePathPickerList(msg)
		m.renderSkipped = false
		return m, nil

	case fileSearchMsg:
		m.handleFileSearch(msg)
		m.renderSkipped = false
		return m, nil

	case fileEditMsg:
		cmd := m.handleFileEdit(msg)
		m.renderSkipped = false
		return m, cmd

	case fileOpMsg:
		cmd := m.HandleFileOp(msg)
		m.renderSkipped = false
		return m, cmd

	case spawnInfoMsg:
		return m, m.applySpawnInfo(msg)
	case spawnPlacedMsg:
		return m, m.applySpawnPlaced(msg)
	case spawnStartedMsg:
		m.applySpawnStarted(msg)
		return m, nil
	case worktreeCreatedMsg:
		return m, m.handleWorktreeCreated(msg)

	case worktreeRemovedMsg:
		return m, m.handleWorktreeRemoved(msg)

	case worktreeTollMsg:
		if m.ShowSessionClose && m.SessionCloseWindow == "" && m.SessionCloseWorkspace == 0 &&
			m.sessionCloseTarget() == msg.Name {
			t := msg.Toll
			m.SessionCloseWorktreeToll = &t
		}
		return m, nil

	case worktreeRemoveQuitMsg:
		if m.SessionName == msg.Name {
			m.QuitSession()
			return m, m.QuitCmd()
		}
		m.KillOtherSession(msg.Name)
		return m, nil

	case tapeDebounceMsg:
		m.handleTapeDebounce(msg.gen)
		return m, nil

	case WindowExitMsg:
		windowID := msg.WindowID
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.noteLocalScratchExit(w)
				m.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
				m.DeleteWindow(i)
				break
			}
		}
		if len(m.Windows) == 0 {
			m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
		}
		return m, listenWindowExitsUntil(m.stopSignal(), m.WindowExitChan)

	case EnableCallbacksMsg:
		m.LogInfo("[CALLBACKS] Re-enabling callbacks for all windows")
		for _, w := range m.Windows {
			if w.DaemonMode {
				w.EnableCallbacks()
				m.LogInfo("[CALLBACKS] Enabled for window %s (IsAltScreen=%v)", shortID(w.ID), w.IsAltScreen())
			}
		}
		return m, nil

	case ForeignSessionRefreshTickMsg:
		if msg.Gen != m.foreignTickGen {
			return m, nil
		}
		m.pruneWindowKeyedState()
		m.renderSkipped = true
		after, refresh := m.foreignSessionRefreshPlan()
		if !refresh {
			return m, m.foreignSessionRefreshTick(after)
		}
		return m, tea.Batch(refreshForeignSessionsCmd(m.DaemonClient), m.foreignSessionRefreshTick(after))

	case SessionsChangedMsg:
		m.pruneWindowKeyedState()
		m.renderSkipped = true
		listen := listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)
		if _, refresh := m.foreignSessionRefreshPlan(); refresh {
			return m, tea.Batch(refreshForeignSessionsCmd(m.DaemonClient), listen)
		}
		return m, listen

	case windowLinkListedMsg:
		cmd := m.followWindowLink(msg.target, msg.rawURL, msg.sessions)
		if m.IsDaemonSession {
			m.SyncStateToDaemon()
		}
		return m, cmd

	case foreignSessionsChangedMsg:
		m.pruneWindowKeyedState()
		m.renderSkipped = false
		return m, m.threadRetryOnState()

	case FederationHostsMsg:
		m.applyFederationSnapshot(msg)
		m.sidebarCache.invalidate()
		open := tea.Batch(m.advanceHostOpen(), m.resumeAfterHostOpen())
		signIn := tea.Batch(open, m.takePendingSignIns())
		if after, refresh := m.federationRefreshPlan(); refresh {
			return m, tea.Batch(signIn, m.federationRefreshTick(after))
		}
		return m, signIn

	case hostRetryMsg:
		return m, m.applyHostRetry(msg)

	case NewWindowOnHostMsg:
		m.ApplyNewWindowOnHost(msg)
		return m, nil

	case PasteRefusedMsg:
		m.ShowNotification(msg.Message, "error", m.Settings.NotificationDuration)
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case TMLInputFailedMsg:
		m.ShowNotification(msg.Text, "warning", m.Settings.NotificationDuration)
		return m, nil

	case tmlRefreshFailedMsg:
		if v := m.tml.views[msg.id]; v != nil {
			v.refreshing = false
		}
		return m, nil

	case TMLClockMsg:
		return m, m.handleTMLClock()

	case TMLViewFlushMsg:
		return m, m.handleTMLViewFlush(msg.id)

	case TMLActionFailedMsg:
		m.ShowNotification(msg.Text, "warning", m.Settings.NotificationDuration)
		return m, nil

	case TMLWakeMsg:
		return m, m.handleTMLWake()

	case PluginApprovalsMsg:
		m.setPluginAsks(msg.Asks)
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case userActionAskedMsg:
		return m, m.userActionAsked(msg)

	case PromptMsg:
		m.setPrompt(msg.Payload)
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case missingDiagramMsg:
		m.noteMissingDiagram(msg.typ)
		return m, nil

	case sketchDrawnMsg:
		m.redrawDiagrams()
		return m, nil

	case PluginUIMsg:
		m.handlePluginUI(msg.Payload)
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case pluginCellsMsg:
		m.handlePluginUI(msg.Payload)
		return m, m.listenPluginCells()

	case pluginApprovalDoneMsg:
		m.pluginApprovalDone(msg)
		return m, nil

	case PaneMarkMsg:
		if m.notePaneMark(msg.Mark) {
			m.MarkAllDirty()
		} else {
			m.renderSkipped = true
		}
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case HostsChangedMsg:
		m.federationPolling = true
		return m, tea.Batch(refreshFederationCmd(), listenClientEventsUntil(m.stopSignal(), m.ClientEventChan))

	case tcAgentsMsg:
		m.applyTCAgents(msg)
		return m, nil

	case tcProjectMsg:
		m.applyTCProject(msg)
		return m, nil

	case tcFiledMsg:
		return m, m.applyTCFiled(msg)

	case tcStartedMsg:
		m.applyTCStarted(msg)
		return m, nil

	case machineSheetMsg:
		return m, m.applyMachineSheetMsg(msg)

	case HostTestDoneMsg:
		m.applyHostTest(msg)
		return m, nil

	case FederationRefreshTickMsg:
		if msg.Gen != m.federationTickGen {
			return m, nil
		}
		after, refresh := m.federationRefreshPlan()
		if !refresh {
			return m, nil
		}
		return m, tea.Batch(refreshFederationCmd(), m.federationRefreshTick(after))

	case subagentHideMsg:
		m.sidebarCache.invalidate()
		m.secondarySidebarCache.invalidate()
		m.renderSkipped = false
		return m, nil

	case dropZoneDueMsg:
		m.showDropZone(msg)
		return m, nil

	case loadingShownMsg:
		m.renderSkipped = false
		return m, nil

	case TriggerAltScreenRedrawMsg:
		m.TriggerAltScreenRedraws()
		return m, nil

	case tea.KeyPressMsg, tea.KeyReleaseMsg, tea.MouseClickMsg, tea.MouseMotionMsg,
		tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.ClipboardMsg,
		tea.PasteMsg, tea.PasteStartMsg, tea.PasteEndMsg, StripTouchMsg:
		switch mm := msg.(type) {
		case tea.KeyPressMsg:
			m.NotifHoldEnd()
			m.hideSetupCard()
			m.hidePastePreview()
		case tea.MouseWheelMsg:
			m.notePointerEvent(time.Now())
		case tea.MouseClickMsg:
			m.hidePastePreview()
			m.notePointerEvent(time.Now())
			m.dndHostMouseSeen()
			m.pointerDown = true
			m.holdGestureAnnouncements()
		case tea.MouseReleaseMsg:
			m.notePointerEvent(time.Now())
			m.dndHostMouseSeen()
			m.pointerDown = false
		case tea.MouseMotionMsg:
			m.notePointerEvent(time.Now())
			m.dndHostMouseSeen()
			m.pointerDown = mm.Button != tea.MouseNone
			if (m.Dragging || m.Resizing) && mm.Button == tea.MouseNone {
				m.endLostGesture()
			}
		}
		m.renderSkipped = false

		if m.screensaver.active {
			return m, m.dismissScreensaver()
		}
		m.screensaver.lastInput = time.Now()

		var shakeCmd tea.Cmd
		if mm, isMotion := msg.(tea.MouseMotionMsg); isMotion {
			shakeCmd = m.noteShakeMotion(mm, time.Now())
		}

		input := m.fixHostKeyMods(msg)
		if m.holdKeyForPaste(input) {
			return m, shakeCmd
		}

		handler := getInputHandler()
		if handler == nil {
			return m, tea.Batch(shakeCmd, m.armScreensaver())
		}
		newModel, cmd := handler(input, m)
		if shakeCmd != nil {
			cmd = tea.Batch(cmd, shakeCmd)
		}
		if saverCmd := m.armScreensaver(); saverCmd != nil {
			cmd = tea.Batch(cmd, saverCmd)
		}

		if _, isMotion := msg.(tea.MouseMotionMsg); isMotion {
			now := time.Now()
			if now.Sub(m.motionBudgetFrom()) < m.framePeriod() {
				m.renderSkipped = true
				m.pointerMotionPending = true
			} else {
				m.lastInteractionRender = now
			}
		}
		return newModel, cmd

	case tea.WindowSizeMsg:
		oldWidth, oldHeight := m.Width, m.Height
		m.Width = max(1, msg.Width)
		m.Height = max(1, msg.Height)
		m.MarkAllDirty()
		m.SixelPassthrough.Invalidate()
		m.viewportResizing = true
		m.renderSkipped = false
		m.viewportResizeGen++
		m.noteResizeStep(time.Now())
		settle := viewportResizeSettleCmd(m.viewportResizeGen)

		if !m.startupApplied {
			m.startupApplied = true
			m.applyStartupPreferences()
			m.syncScratchView()
			m.warnOnBuildMismatch()
			m.announceRestart()
			m.announceUpgradeFollowed()
		}

		if m.IsDaemonSession && m.DaemonClient != nil {
			m.DaemonClient.SetOwnLayoutReserve(m.OwnLayoutReserve())
			_ = m.DaemonClient.NotifyTerminalSize(msg.Width, msg.Height)
		}

		if m.RestoredFromState {
			m.RestoredFromState = false
			sizeChanged := oldWidth != msg.Width || oldHeight != msg.Height
			if sizeChanged {
				if m.IsDaemonSession && m.AutoTiling {
					m.LogInfo("[RESIZE] Daemon mode restore: tiling to %dx%d (was %dx%d)",
						msg.Width, msg.Height, oldWidth, oldHeight)
					if m.EffectiveWidth <= 0 || m.EffectiveHeight <= 0 {
						m.EffectiveWidth = msg.Width
						m.EffectiveHeight = msg.Height
					}
					m.TileAllWindows()
				} else if m.AutoTiling {
					m.LogInfo("[RESIZE] Retiling restored session to fit new terminal size (%dx%d -> %dx%d)",
						oldWidth, oldHeight, msg.Width, msg.Height)
					m.TileAllWindows()
				} else {
					if oldWidth > 0 && oldHeight > 0 {
						m.LogInfo("[RESIZE] Scaling restored windows from %dx%d -> %dx%d",
							oldWidth, oldHeight, msg.Width, msg.Height)
						m.ScaleWindowsToTerminal(oldWidth, oldHeight, msg.Width, msg.Height)
					} else {
						m.ClampWindowsToView()
					}
				}
			} else {
				m.LogInfo("[RESIZE] Restored session, same size (%dx%d), preserving layout", msg.Width, msg.Height)
			}

			m.FlushPTYBuffersAfterResize()

			if m.KittyPassthrough != nil {
				m.KittyPassthrough.HideAllPlacements()
			}

			return m, settle
		}

		if m.AutoTiling {
			m.TileAllWindows()
		} else if msg.Width < oldWidth || msg.Height < oldHeight {
			m.ClampWindowsToView()
		}

		if cmd := tea.Batch(m.CockpitImagePicture(), m.cockpitLiveFollow(), m.QuickLookSync()); cmd != nil {
			return m, tea.Batch(settle, cmd)
		}
		return m, settle

	case ViewportResizeSettledMsg:
		if msg.Gen != m.viewportResizeGen {
			return m, nil
		}
		m.endResizeDeferral()
		return m, m.LauncherIconWork()

	case InteractionSettledMsg:
		if !m.Dragging && !m.Resizing {
			m.InteractionMode = false
		}
		return m, nil

	case tea.MouseMsg:
		return m, nil

	case tea.FocusMsg:
		m.dockEngine.Replan()
		return m, m.noteHostFocus(true)

	case tea.BlurMsg:
		m.EndHold()
		m.NotifHoldEnd()
		m.ResetPointerShape()
		return m, m.noteHostFocus(false)

	case tea.ColorProfileMsg:
		if m.screen != nil {
			m.screen.SetColorProfile(msg.Profile)
		}
		if theme.ColorProfile() != msg.Profile {
			depth := theme.Depth()
			theme.SetColorProfile(msg.Profile)
			if theme.Depth() != depth {
				m.UpdateAllWindowThemes()
			}
			m.refreshImageSymbols()
			m.MarkAllDirty()
		}
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		first := m.KeyboardFlags == 0
		m.NoteKeyboardEnhancements(msg)
		if reason := m.HoldModeUnsupportedReason(); reason != "" && first {
			m.ShowNotification(reason, "warning", m.Settings.NotificationDuration)
		}
		return m, nil

	case StateSyncMsg:
		if m.staleAttach(msg.Attach) {
			return m, tea.Batch(listenStateSyncUntil(m.stopSignal(), m.StateSyncChan), m.noteStateSyncApplied(msg.arrival))
		}
		if msg.State != nil {
			oldWindowCount := m.windowCountForNotice()
			oldWorkspace := m.CurrentWorkspace
			oldWindows := m.windowLabelsForNotice()
			oldMade := m.panesMadeForNotice()

			if err := m.ApplyStateSyncFrom(msg.State, msg.SourceID); err != nil {
				m.LogError("Failed to apply state sync: %v", err)
			} else {
				m.maybeEnterPendingTerminalMode()
				m.syncScratchView()
				m.maybeFocusScratch()
				m.leaveEmptyScratchView()

				newWindowCount := m.windowCountForNotice()
				newMade := m.panesMadeForNotice()
				newWorkspace := m.CurrentWorkspace

				if note, ok := m.windowMoveNotice(msg.State, oldWindows); ok {
					m.ShowNotification(note, "info", 2*time.Second)
				} else if newMade == oldMade {
				} else if newWindowCount > oldWindowCount {
					m.showAmbientNotification(fmt.Sprintf("Window created (%d total)", newWindowCount), "info", 2*time.Second)
				} else if newWindowCount < oldWindowCount {
					m.showAmbientNotification(fmt.Sprintf("Window closed (%d remaining)", newWindowCount), "info", 2*time.Second)
				}

				if oldWorkspace != newWorkspace && !session.IsScratchWorkspace(oldWorkspace) && !session.IsScratchWorkspace(newWorkspace) {
					m.ShowNotification(fmt.Sprintf("Switched to workspace %d", m.workspaceDisplayNumber(newWorkspace)), "info", 2*time.Second)
				}
				m.flushPiPNote()
				if note := m.heldNotice; note != "" {
					m.heldNotice = ""
					m.ShowNotification(note, "info", m.Settings.NotificationDuration)
				}

				m.refreshGotoRows()
			}
		}
		return m, tea.Batch(listenStateSyncUntil(m.stopSignal(), m.StateSyncChan), m.refreshSidebarSessions(), m.noteStateSyncApplied(msg.arrival), m.threadRetryOnState())

	case heldRemoteTimeoutMsg:
		if msg.gen != m.heldRemoteGen {
			return m, nil
		}
		return m, m.releaseHeldRemote(true)

	case ScratchOpenedMsg:
		m.handleScratchOpened(msg)
		return m, nil

	case RailPaneOpenedMsg:
		m.handleRailPaneOpened(msg)
		return m, nil

	case CommandRanMsg:
		m.handleCommandRan(msg)
		return m, nil

	case CopyPipeDoneMsg:
		return m, m.handleCopyPipeDone(msg)

	case PasteBufferSaveFailedMsg:
		m.handlePasteBufferSaveFailed(msg)
		return m, nil

	case PasteBufferFetchedMsg:
		cmd := m.handlePasteBufferFetched(msg)
		if msg.held {
			cmd = tea.Batch(cmd, m.releasePasteHold())
		}
		return m, cmd

	case PasteBuffersLoadedMsg:
		m.handlePasteBuffersLoaded(msg)
		return m, nil

	case PasteBufferDeletedMsg:
		m.handlePasteBufferDeleted(msg)
		return m, nil

	case WorkspaceRenumberedMsg:
		return m, m.handleWorkspaceRenumbered(msg)

	case RenameAppliedMsg:
		if msg.Err != nil {
			what := msg.What
			if what == "" {
				what = "Rename"
			}
			m.ShowNotification(what+" failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
			return m, nil
		}
		m.refreshGotoRows()
		return m, m.refreshListingCmd()

	case SessionOrderAppliedMsg:
		return m, m.applySessionOrderResult(msg)

	case LayoutTemplateDoneMsg:
		m.handleLayoutTemplateDone(msg)
		return m, nil

	case layoutTemplatesListedMsg:
		return m, m.handleLayoutTemplatesListed(msg)

	case StripSnapMsg:
		if m.SnapStrip(msg) && m.IsDaemonSession {
			m.SyncStateToDaemon()
		}
		return m, nil

	case listingRefreshedMsg:
		m.refreshGotoRows()
		return m, nil

	case ClientJoinedMsg:
		if m.staleAttach(msg.Attach) {
			return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)
		}
		m.sessionClients = msg.ClientCount
		m.ShowNotification(fmt.Sprintf("Client joined (%d connected)", msg.ClientCount), "info", 2*time.Second)
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case AgentMailMsg:
		if m.staleAttach(msg.Attach) {
			return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)
		}
		m.noteAgentMail(msg.Payload)
		m.noteAgentsSeen()
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case agentIntegrationMsg:
		m.agentIntegrationInstalled = true
		return m, nil

	case agentsOverviewMsg:
		m.applyAgentsOverview(msg)
		return m, nil

	case agentsActionMsg:
		return m, m.applyAgentAction(msg)

	case InboxRewatchMsg:
		return m, tea.Batch(m.restartInboxWatch(), listenClientEventsUntil(m.stopSignal(), m.ClientEventChan))

	case AgentMailLoadMsg:
		return m, tea.Batch(m.agentMailLoad(), listenClientEventsUntil(m.stopSignal(), m.ClientEventChan))

	case AgentMailMarkMsg:
		return m, tea.Batch(m.agentMailMarkRead(msg.Thread), listenClientEventsUntil(m.stopSignal(), m.ClientEventChan))

	case AgentMailLoadedMsg:
		m.applyAgentMailLoaded(msg)
		return m, m.takePendingInboxThread()

	case inboxWatchMsg:
		return m, m.handleInboxWatch(msg)

	case transfersWatchMsg:
		return m, m.handleTransfersWatch(msg)

	case transfersLingerMsg:
		m.applyTransfersLinger(msg)
		return m, nil

	case transferCallMsg:
		return m, m.applyTransferCall(msg)

	case transfersClearedMsg:
		m.applyTransfersCleared(msg)
		return m, nil

	case dropProbeMsg:
		m.applyDropProbe(msg)
		return m, nil

	case dropSentMsg:
		return m, m.applyDropSent(msg)

	case foreignListingRefreshedMsg:
		return m, m.threadRetryOnState()

	case InboxAlertDueMsg:
		m.applyInboxAlertDue(msg)
		return m, nil

	case inboxTypingReleaseMsg:
		return m, m.applyInboxTypingRelease(msg)

	case InboxDismissedMsg:
		m.applyInboxDismissed(msg)
		return m, nil

	case InboxMarkedMsg:
		m.applyInboxMarked(msg)
		return m, nil

	case InboxPeekMsg:
		m.applyInboxPeek(msg)
		return m, nil

	case NavigatorLoadedMsg:
		m.ApplyNavigatorLoaded(msg)
		return m, nil

	case InboxRespondedMsg:
		return m, m.applyInboxResponded(msg)

	case InboxApprovalRepliedMsg:
		m.applyInboxApprovalReplied(msg)
		return m, nil

	case inboxJumpMsg:
		m.inboxJump(msg.item)
		return m, nil

	case InboxApprovalDetailMsg:
		m.applyInboxApprovalDetail(msg)
		return m, nil

	case InboxQuestionPeekMsg:
		return m, m.applyInboxQuestionPeek(msg)

	case inboxQuestionRetryMsg:
		return m, m.applyInboxQuestionRetry(msg)

	case InboxRepliedMsg:
		echo := m.echoReplied(msg)
		m.applyInboxReplied(msg)
		return m, tea.Batch(m.replyHistoryOnSent(msg), echo)

	case echoTickMsg:
		return m, m.applyEchoTick()

	case echoQueueMsg:
		return m, m.applyEchoQueue(msg)

	case echoCancelledMsg:
		m.applyEchoCancelled(msg)
		return m, nil

	case replyHistoryMsg:
		m.applyReplyHistory(msg)
		return m, nil

	case replyEditorDoneMsg:
		return m, m.applyReplyEditorDone(msg)

	case scrollbackFileMsg:
		return m, m.openScrollbackEditor(msg)

	case scrollbackEditorDoneMsg:
		return m, m.applyScrollbackEditorDone(msg)

	case clearScrollbackDoneMsg:
		m.applyClearScrollbackDone(msg)
		return m, nil

	case ReviewDiffMsg:
		m.applyReviewDiff(msg)
		if msg.Err == nil && msg.Gen == m.review.gen && m.review.open {
			return m, m.reviewIndexCmd()
		}
		return m, nil

	case ReviewIndexMsg:
		m.applyReviewIndex(msg)
		return m, nil

	case ReviewStagedMsg:
		return m, m.applyReviewStaged(msg)

	case ReviewNotesMsg:
		m.applyReviewNotes(msg)
		return m, nil

	case ReviewSentMsg:
		m.applyReviewSent(msg)
		return m, nil

	case ReviewCompareMsg:
		return m, m.applyReviewCompare(msg)

	case ReviewVerifyMsg:
		return m, m.applyReviewVerify(msg)

	case ReviewKeptMsg:
		return m, m.applyReviewKept(msg)

	case ReviewTickMsg:
		return m, m.applyReviewTick(msg)

	case InboxQueueDroppedMsg:
		m.applyInboxQueueDropped(msg)
		return m, nil

	case InboxRecapMsg:
		m.applyInboxRecap(msg)
		return m, nil

	case AgentReturnRecapMsg:
		m.applyAgentReturnRecap(msg)
		return m, nil

	case InboxAskAnsweredMsg:
		m.applyInboxAskAnswered(msg)
		return m, nil

	case InboxResumedMsg:
		m.applyInboxResumed(msg)
		return m, nil

	case InboxBulkResumedMsg:
		m.applyInboxBulkResumed(msg)
		return m, nil

	case InboxReleasedMsg:
		m.applyInboxReleased(msg)
		return m, nil

	case AgentMailSentMsg:
		m.applyAgentMailSent(msg)
		if msg.Err != nil || msg.histWindow == "" {
			return m, nil
		}
		return m, m.replyHistoryAdd(msg.histSession, msg.histWindow, msg.histParts)

	case AgentMailMarkedMsg:
		if msg.Err != nil {
			m.AgentMail.Error = "The mail could not be marked read. " + msg.Err.Error()
		}
		return m, nil

	case ClientLeftMsg:
		if m.staleAttach(msg.Attach) {
			return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)
		}
		m.sessionClients = msg.ClientCount
		m.ShowNotification(fmt.Sprintf("Client left (%d connected)", msg.ClientCount), "info", 2*time.Second)
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case SessionResizeMsg:
		if m.staleAttach(msg.Attach) {
			return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)
		}
		m.layoutGenApplied = max(m.layoutGenApplied, msg.Generation)
		m.sessionClients = msg.ClientCount
		if m.EffectiveWidth != msg.Width || m.EffectiveHeight != msg.Height || m.SessionReserve != msg.Reserve {
			oldWidth, oldHeight := m.GetLayoutWidth(), m.GetLayoutHeight()
			sizeChanged := m.EffectiveWidth != msg.Width || m.EffectiveHeight != msg.Height
			m.EffectiveWidth = msg.Width
			m.EffectiveHeight = msg.Height
			m.SessionReserve = msg.Reserve
			m.MarkAllDirty()
			if m.AutoTiling {
				m.TileAllWindows()
			} else if m.GetLayoutWidth() < oldWidth || m.GetLayoutHeight() < oldHeight {
				m.ClampWindowsToView()
			}
			m.SyncDaemonPTYDimensions()
			if sizeChanged {
				m.showAmbientNotification(fmt.Sprintf("Session size: %dx%d (%d clients)", msg.Width, msg.Height, msg.ClientCount), "info", 2*time.Second)
			}
		} else if m.DaemonClient.SessionSizedBySelf() {
			for _, w := range m.Windows {
				if w.DaemonMode && w.DaemonResizeFunc != nil && m.snapInFlight(w) == nil {
					_ = w.DaemonResizeFunc(w.ContentWidth(), w.ContentHeight())
				}
			}
		}
		if m.layoutGenApplied > m.layoutGenPushed {
			m.SyncStateToDaemon()
		}
		return m, tea.Batch(listenClientEventsUntil(m.stopSignal(), m.ClientEventChan), m.refreshSidebarSessions())

	case ForceRefreshMsg:
		m.MarkAllDirty()
		return m, listenClientEventsUntil(m.stopSignal(), m.ClientEventChan)

	case DaemonDisconnectedMsg:
		if m.noteRestartDisconnect() {
			return m, listenDaemonExitUntil(m.stopSignal(), m.daemonExitChan())
		}
		if m.AttachedHost != "" && !m.QuitRequested {
			listen := listenDaemonExitUntil(m.stopSignal(), m.daemonExitChan())
			if cmd := m.beginHostReconnect(msg.Err); cmd != nil {
				return m, tea.Batch(cmd, listen)
			}
			if m.ReconnectingToHost() {
				return m, listen
			}
			if m.HostReconnectDeferred() {
				return m, listen
			}
			m.ExitReason = ExitHostLost
			return m, m.QuitCmd()
		}
		if cmd := m.beginDaemonReconnect(); cmd != nil {
			return m, tea.Batch(cmd, listenDaemonExitUntil(m.stopSignal(), m.daemonExitChan()))
		}
		if m.ReconnectingToDaemon() {
			return m, listenDaemonExitUntil(m.stopSignal(), m.daemonExitChan())
		}
		if !m.QuitRequested {
			m.ExitReason = m.daemonGoneReason()
		}
		return m, m.QuitCmd()

	case daemonReconnectTickMsg:
		return m, m.handleDaemonReconnectTick(msg)

	case daemonReconnectResultMsg:
		return m, m.handleDaemonReconnectResult(msg)

	case restartPlanMsg, restartStepMsg, restartEndMsg, restartQuitMsg:
		return m, m.handleRestartMsg(msg)

	case hostReconnectTickMsg:
		return m, m.handleHostReconnectTick(msg)

	case hostReconnectResultMsg:
		return m, m.handleHostReconnectResult(msg)

	case SessionEndedMsg:
		if m.restart.running() {
			return m, listenDaemonExitUntil(m.stopSignal(), m.daemonExitChan())
		}
		if !m.QuitRequested {
			m.ExitReason = ExitSessionKilled
		}
		if m.DaemonClient != nil && m.DaemonClient.NestedRefusal() != "" {
			m.ExitReason = ExitNestedRefused
		}
		if m.DaemonClient != nil && m.DaemonClient.DetachedReason() != "" {
			m.ExitReason = ExitDetached
		}
		return m, m.QuitCmd()

	case omarchyFollowMsg:
		m.applyOmarchyPalette(msg.palette)
		return m, listenForOmarchy(m.omarchyChanges)

	case keybindPresetSavedMsg:
		return m, m.handleKeybindPresetSaved(msg)

	case welcomeSavedMsg:
		return m, m.handleWelcomeSaved(msg)

	case configWatchMsg:
		_, cmd := m.Update(msg.msg)
		return m, tea.Batch(cmd, listenForConfigReload(m.configReloads))

	case themeWatchMsg:
		_, cmd := m.Update(msg.msg)
		return m, tea.Batch(cmd, listenForThemeReload(m.themeReloads))

	case ThemeFilesReloadedMsg:
		m.UpdateAllWindowThemes()
		m.MarkAllDirty()
		if len(msg.Problems) > 0 {
			for _, p := range msg.Problems {
				m.LogWarn("Theme: %s", p)
			}
			m.ShowNotification(msg.Problems[0], "warning", m.Settings.NotificationWarningDuration)
		}
		if m.asksHostColors() {
			return m, tea.Batch(hostColorRequests()...)
		}
		return m, nil

	case themeEditorFlashEndMsg:
		m.endThemeEditorFlash(msg)
		return m, nil

	case displayRateMsg:
		m.handleDisplayRate(msg)
		return m, nil

	case ConfigReloadedMsg:
		cmd := m.ApplyReloadedConfig(msg.Config)
		if msg.Config != nil && len(msg.Config.LoadWarnings) > 0 {
			for _, w := range msg.Config.LoadWarnings {
				m.LogWarn("Config: %s", w)
			}
			m.ShowNotification(msg.Config.LoadWarnings[0], "warning", m.Settings.NotificationWarningDuration)
		}
		return m, tea.Batch(cmd, m.refreshSidebarSessions())

	case ConfigReloadFailedMsg:
		if msg.Err != nil {
			m.ShowNotification("Config not reloaded: "+msg.Err.Error(), "error", 0)
		}
		return m, nil

	case screenshotResultMsg:
		cmd := m.HandleScreenshotResult(msg)
		m.MarkAllDirty()
		return m, cmd

	case lookShareAuthorMsg:
		return m, m.handleLookShareAuthor(msg)

	case lookShareDoneMsg:
		m.handleLookShareDone(msg)
		return m, nil

	case screenshotCopiedMsg:
		m.HandleScreenshotCopied(msg)
		m.MarkAllDirty()
		return m, nil

	case launcherIconsMsg:
		m.applyLauncherIcons(msg)
		if m.ShowLauncher {
			m.MarkAllDirty()
		}
		return m, nil

	case PathAppsMsg:
		m.applyPathApps(msg.Entries)
		if !m.ShowLauncher {
			return m, nil
		}
		m.MarkAllDirty()
		return m, m.LauncherIconWork()

	case HostApplyFailedMsg:
		m.ShowNotification("Host "+msg.Host+" is saved, but the daemon did not open it: "+msg.Err.Error()+". Run tuios config apply in a terminal outside tuios.", "warning", m.Settings.NotificationWarningDuration)
		return m, nil

	case settingsSaveFailedMsg:
		config.RewindSave(m.UserConfig, msg.err)
		m.ShowNotification("Could not save settings: "+msg.err.Error(), "error", 0)
		return m, nil

	case settingsSaveRedirectedMsg:
		m.ShowNotification(msg.note.Message(), "warning", m.Settings.NotificationWarningDuration)
		return m, nil

	case tapeLayoutRefreshMsg:
		m.refreshAllPanesAfterTape()
		return m, nil

	case ScriptCommandMsg:
		m.scriptInFlight = false
		if !m.ScriptMode || m.ScriptFailure != "" {
			return m, nil
		}
		if executor := m.ScriptExecutor; executor != nil {
			if err := executor.Execute(msg.Command); err != nil {
				m.failScript(msg.Command, err)
			} else {
				m.SyncStateToDaemon()
			}
		}
		return m, m.takeScriptCmds()

	case RemoteCommandMsg:
		var relisten tea.Cmd
		if !msg.released {
			relisten = listenRemoteCommandsUntil(m.stopSignal(), m.RemoteCommandChan)
		}
		if held, wait := m.holdRemote(msg); held {
			return m, tea.Batch(relisten, wait)
		}
		var err error
		var persistCmd tea.Cmd
		var cmd tea.Cmd
		var notificationMsg string
		var resultData map[string]any

		switch msg.CommandType {
		case "tape_command":
			if len(msg.TapeArgs) > 0 {
				notificationMsg = fmt.Sprintf("Remote: %s %s", msg.TapeCommand, msg.TapeArgs[0])
			} else {
				notificationMsg = fmt.Sprintf("Remote: %s", msg.TapeCommand)
			}

			switch msg.TapeCommand {
			case "ListWindows":
				resultData = m.GetWindowListData()
				if m.DaemonClient != nil && msg.RequestID != "" {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
				}
				return m, relisten
			case "GetSessionInfo":
				resultData = m.GetSessionInfoData()
				if m.DaemonClient != nil && msg.RequestID != "" {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
				}
				return m, relisten
			case "ShareLook":
				var name, author string
				if len(msg.TapeArgs) > 0 {
					name = msg.TapeArgs[0]
				}
				if len(msg.TapeArgs) > 1 {
					author = msg.TapeArgs[1]
				}
				return m, tea.Batch(m.ShareLook(name, author, msg.RequestID), relisten)
			case "GetWindow":
				if len(msg.TapeArgs) > 0 {
					resultData, err = m.GetWindowData(msg.TapeArgs[0])
				} else {
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
				tapeCmd := &tape.Command{
					Type: tape.CommandType(msg.TapeCommand),
					Args: msg.TapeArgs,
				}
				executor := tape.NewCommandExecutor(m)
				err = executor.Execute(tapeCmd)
				cmd = m.takeScriptCmds()
			}
			if m.AutoTiling {
				m.TileAllWindows()
			}
		case "send_keys":
			notificationMsg = fmt.Sprintf("Remote: send-keys %s", msg.Keys)

			cmd, err = m.startRemoteSendKeys(msg.Keys, msg.Literal, msg.Raw, msg.WindowTarget, msg.RequestID)
			if err == nil {
				m.showRemoteNotice(notificationMsg)
				return m, tea.Batch(cmd, relisten)
			}
		case "set_config":
			notificationMsg = fmt.Sprintf("Remote: set %s=%s", msg.ConfigPath, msg.ConfigValue)

			if msg.ConfigNoSave {
				notificationMsg += " (not saved)"
				err = m.UserConfig.HoldOption(msg.ConfigPath, func() error {
					return m.SetConfig(msg.ConfigPath, msg.ConfigValue)
				})
			} else {
				m.UserConfig.ReleaseOption(msg.ConfigPath)
				err = m.SetConfig(msg.ConfigPath, msg.ConfigValue)
				if err == nil {
					persistCmd = m.persistSettings()
				}
			}
			if m.AutoTiling {
				m.TileAllWindows()
			}
		case "list_dock_components":
			resultData = map[string]any{
				"type":       "dock_component_list",
				"components": m.DockComponentsData(),
			}
			if m.DaemonClient != nil && msg.RequestID != "" {
				if sendErr := m.DaemonClient.SendCommandResultWithData(
					msg.RequestID, true, "command executed", resultData); sendErr != nil {
					m.LogError("dock: could not answer list-dock-components: %v", sendErr)
				}
			}
			return m, relisten
		case "list_hooks":
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
		case "pip":
			window, off := "", false
			if len(msg.TapeArgs) > 0 {
				window = msg.TapeArgs[0]
			}
			if len(msg.TapeArgs) > 1 {
				off = msg.TapeArgs[1] == "off"
			}
			pinned, id, pipErr := m.SetPiP(window, off)
			if m.DaemonClient != nil && msg.RequestID != "" {
				if pipErr != nil {
					_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, pipErr.Error())
				} else {
					_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed",
						map[string]any{"pinned": pinned, "window_id": id})
				}
			}
			return m, relisten
		case "swap_windows":
			if len(msg.TapeArgs) != 2 {
				err = errfmt.Errorf("swap_windows needs two window ids")
			} else {
				err = m.SwapWindowsByID(msg.TapeArgs[0], msg.TapeArgs[1])
			}
		case "zoom_window":
			if len(msg.TapeArgs) != 2 {
				err = errfmt.Errorf("zoom_window needs a window id and on or off")
			} else {
				err = m.ZoomWindowByID(msg.TapeArgs[0], msg.TapeArgs[1] == "on")
			}
		case "resize_window":
			if len(msg.TapeArgs) != 3 {
				err = errfmt.Errorf("resize_window needs a window id, a direction and an amount")
				break
			}
			amount, perr := strconv.ParseFloat(msg.TapeArgs[2], 64)
			if perr != nil {
				err = errfmt.Errorf("resize_window: bad amount %q", msg.TapeArgs[2])
				break
			}
			var changed bool
			if changed, err = m.ResizeWindowByID(msg.TapeArgs[0], msg.TapeArgs[1], amount); err == nil {
				resultData = map[string]any{"changed": changed}
			}
		case "set_client_title":
			title := ""
			if len(msg.TapeArgs) > 0 {
				title = msg.TapeArgs[0]
			}
			resultData = map[string]any{"changed": m.ClientTitle != title}
			m.ClientTitle = title
		case "switch_session":
			if len(msg.TapeArgs) < 1 || msg.TapeArgs[0] == "" {
				err = errfmt.Errorf("switch_session needs a session name")
				break
			}
			name := msg.TapeArgs[0]
			host := ""
			if len(msg.TapeArgs) > 1 {
				host = msg.TapeArgs[1]
			}
			if host == federation.LocalHostName && m.AttachedHost == "" {
				host = ""
			}
			if host != "" && host != m.attachedMachine() {
				create := len(msg.TapeArgs) > 2 && msg.TapeArgs[2] == "create"
				cwd := ""
				if len(msg.TapeArgs) > 3 {
					cwd = msg.TapeArgs[3]
				}
				if err = m.switchToHostAsync(host, name, create, cwd, msg.RequestID); err != nil {
					break
				}
				return m, relisten
			}
			if m.DaemonClient != nil && msg.RequestID != "" {
				_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "command executed")
			}
			return m, tea.Batch(func() tea.Msg { return remoteSwitchSessionMsg{name: name} }, relisten)
		case "leave_session":
			if len(msg.TapeArgs) < 2 || msg.TapeArgs[0] != m.SessionName {
				err = errfmt.Errorf("leave_session names a session this client does not show")
				break
			}
			to := m.leaveTarget(msg.TapeArgs[1:])
			if m.DaemonClient != nil && msg.RequestID != "" {
				_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", map[string]any{"session": to})
			}
			return m, tea.Batch(func() tea.Msg { return remoteSwitchSessionMsg{name: to} }, relisten)
		case "notice":
			if len(msg.TapeArgs) > 0 {
				m.heldNotice = msg.TapeArgs[0]
				m.ShowNotification(m.heldNotice, "info", m.Settings.NotificationDuration)
			}
			return m, relisten
		case "refresh_dock":
			name := ""
			if len(msg.TapeArgs) > 0 {
				name = msg.TapeArgs[0]
			}
			if err = m.RefreshDockComponent(name); err == nil {
				notificationMsg = "Remote: refresh-dock " + name
			}
		case "tape_script":
			notificationMsg = "Remote: running a tape"

			cmd, err = m.executeTapeScript(msg.TapeScript, msg.RequestID)
			if err == nil {
				if m.tapeStatusShown() {
					m.showRemoteNotice(notificationMsg)
				}
				return m, tea.Batch(cmd, relisten)
			}
		default:
			err = errfmt.Errorf("unknown remote command type: %s", msg.CommandType)
		}

		m.MarkAllDirty()

		if err == nil {
			m.SyncStateToDaemon()
		}

		if err != nil {
			m.ShowNotification(fmt.Sprintf("Remote error: %v", err), "error", m.Settings.NotificationDuration)
		} else if notificationMsg != "" {
			m.showRemoteNotice(notificationMsg)
		}

		if m.DaemonClient != nil && msg.RequestID != "" {
			if err != nil {
				_ = m.DaemonClient.SendCommandResult(msg.RequestID, false, err.Error())
			} else {
				_ = m.DaemonClient.SendCommandResultWithData(msg.RequestID, true, "command executed", resultData)
			}
		}

		return m, tea.Batch(cmd, persistCmd, relisten)

	case remoteSwitchSessionMsg:
		m.openSession("", msg.name)
		m.MarkAllDirty()
		return m, nil

	case RemoteKeyMsg:
		var cmd tea.Cmd

		m.hidePastePreview()

		if handler := getInputHandler(); handler != nil {
			newModel, keyCmd := handler(msg.Key, m)
			if newOS, ok := newModel.(*OS); ok {
				m = newOS
			}
			cmd = keyCmd
		}

		var next tea.Msg = RemoteKeysDoneMsg{RequestID: msg.RequestID}
		if len(msg.RemainingKeys) > 0 {
			next = RemoteKeyMsg{
				Key:           msg.RemainingKeys[0],
				RemainingKeys: msg.RemainingKeys[1:],
				RequestID:     msg.RequestID,
			}
		}
		return m, remoteKeyThen(cmd, next, remoteKeyCmdWait)

	case RemoteKeysDoneMsg:
		m.remoteKeyChains = max(m.remoteKeyChains-1, 0)
		if m.remoteKeyChains == 0 {
			m.ProcessingRemoteKeys = false
			m.Settings.AnimationsSuppressed = false
		}

		m.MarkAllDirty()

		if m.DaemonClient != nil && msg.RequestID != "" {
			_ = m.DaemonClient.SendCommandResult(msg.RequestID, true, "keys sent")
		}

		return m, nil

	}

	return m, nil
}

const activityCarry = 2 * time.Second

func (m *OS) reportActivity(msg tea.Msg) {
	if !m.IsDaemonSession || m.DaemonClient == nil {
		return
	}
	input := isActivityInput(msg)
	if input {
		m.lastActivity = m.msgClock
		m.DaemonClient.ReportUsed(m.msgClock)
	}
	policy := m.DaemonClient.SessionWindowSize()
	named := policy == config.WindowSizeLatest
	if name := m.DaemonClient.SessionName(); name != m.activitySession {
		if m.activitySession != "" && !m.lastActivity.IsZero() &&
			m.msgClock.Sub(m.lastActivity) < activityCarry {
			m.DaemonClient.ReportUsed(m.msgClock)
		}
		m.activitySession, m.activitySince = name, m.msgClock
	}
	switch {
	case policy != "" && !named:
	case input:
		m.DaemonClient.ReportActivity(m.msgClock)
	case named && !m.wasLatest && m.lastActivity.After(m.activitySince) &&
		m.msgClock.Sub(m.lastActivity) < activityCarry:
		m.DaemonClient.ReportActivity(m.msgClock)
	}
	m.wasLatest = named
}

func isActivityInput(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
		return true
	case tea.MouseMotionMsg:
		return msg.Button != tea.MouseNone
	}
	return false
}

func isRemoteKey(msg tea.Msg) bool {
	_, ok := msg.(RemoteKeyMsg)
	return ok
}

const remoteKeyCmdWait = time.Second

func remoteKeyThen(cmd tea.Cmd, next tea.Msg, wait time.Duration) tea.Cmd {
	if cmd == nil {
		return func() tea.Msg { return next }
	}
	var sent atomic.Bool
	ran := make(chan struct{})
	inOrder := func() tea.Msg {
		close(ran)
		if sent.CompareAndSwap(false, true) {
			return next
		}
		return nil
	}
	late := func() tea.Msg {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ran:
			return nil
		case <-t.C:
		}
		if sent.CompareAndSwap(false, true) {
			return next
		}
		return nil
	}
	return tea.Batch(tea.Sequence(cmd, inOrder), late)
}

func isPersonInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.KeyReleaseMsg, tea.MouseClickMsg, tea.MouseReleaseMsg,
		tea.MouseWheelMsg, tea.MouseMotionMsg, tea.PasteMsg:
		return true
	}
	return false
}
