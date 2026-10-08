package app

import (
	"log"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// One wiring for the daemon connection, shared by every client.
//
// The local attach client, the SSH server and tuios-web each used to register
// the daemon client's callbacks themselves, three copies of the same list.
// When the session-ended and disconnect handlers were added the local client
// got them first and the two servers held their last frame for ever until a
// later fix caught them up. The list lives here now, once, and the callbacks
// deliver through the model's own channels, which Init listens on. A callback
// runs on the daemon read-loop goroutine, so nothing in here touches the model:
// every event is queued and applied in Update like any other message. The log
// lines cannot use the model's own ring for the same reason, and they cannot go
// to the standard logger unconditionally either: in a local client that logger
// is the terminal the TUI is drawing on, and one line per state sync wrote
// itself across the frame. They go out only when verbose logging is on, which
// is when the local client has already pointed the logger at a file.
//
// TestEveryDaemonHandlerIsWiredHere pins that no entry point registers one of
// these itself.

// WireDaemonClient registers this model as the listener for everything the
// daemon sends an attached client: routed verbs, state syncs, peers joining and
// leaving, the session's size, the session ending and the connection going.
func (m *OS) WireDaemonClient(client *session.TUIClient) {
	if client == nil {
		return
	}
	// A new connection starts with nothing said about focus. A client that
	// replaces a lost one already knows, so it says so at once.
	if m.hostFocus != hostFocusUnknown {
		focused := m.hostFocus == hostFocusIn
		go func() { _ = client.ReportHostFocus(focused) }()
	}
	// A verb the daemon routed to this client, applied in Update. Without this
	// set-option, send-keys and refresh-dock timed out against a served client
	// while the daemon still reported one attached.
	client.OnRemoteCommand(func(payload *session.RemoteCommandPayload) error {
		if m.QueueRemoteCommand(payload) {
			clientLog("RemoteCommandChan full, dropped a routed %s", payload.CommandType)
		}
		return nil
	})
	client.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		clientLog("State sync: trigger=%s, source=%s", triggerType, shortID(sourceID))
		if m.QueueStateSync(StateSyncMsg{State: state, TriggerType: triggerType, SourceID: sourceID, Attach: client.AttachGeneration()}) {
			clientLog("StateSyncChan full, superseded the queued snapshot")
		}
	})
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		clientLog("Client joined: %s (total: %d, size: %dx%d)", shortID(clientID), clientCount, width, height)
		m.QueueClientEvent(ClientEvent{Type: "joined", ClientID: clientID, ClientCount: clientCount, Width: width, Height: height})
	})
	// Mail an agent left in the session's ring, and receipts for mail an agent
	// read. Queued like every other broadcast; the mirror is touched in Update.
	client.OnAgentMail(func(payload session.AgentMailPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "agent-mail", Mail: payload}) {
			clientLog("ClientEventChan full, displaced an event for agent mail")
		}
	})
	// The daemon's host table changed. The rail re-polls in Update, where the
	// poll gate lives; see HostsChangedMsg.
	client.OnHostsChanged(func(session.HostsChangedPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "hosts-changed"}) {
			clientLog("ClientEventChan full, displaced an event for a hosts change")
		}
	})
	// A paste the daemon refused twice, or one too large to send. The person
	// is told, so a paste never vanishes. See session/paste_retry.go.
	client.OnPasteRefused(func(string) {
		if m.QueueClientEvent(ClientEvent{Type: "paste-refused"}) {
			clientLog("ClientEventChan full, displaced an event for a refused paste")
		}
	})
	// A folder the daemon watches for the files section changed. See
	// sidebar_files_watch.go.
	client.OnDirChanged(m.fileWatch.remoteDirChanged)
	client.OnClientLeft(func(clientID string, clientCount int) {
		clientLog("Client left: %s (remaining: %d)", shortID(clientID), clientCount)
		m.QueueClientEvent(ClientEvent{Type: "left", ClientID: clientID, ClientCount: clientCount})
	})
	// The session's size is the minimum over its clients. The geometry
	// mutation (TileAllWindows, emulator resizes) happens in Update.
	client.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		clientLog("Session resize: %dx%d chrome %+v (clients: %d)", width, height, reserve, clientCount)
		// Read here, on the client's read loop, where it is this resize's
		// own: the loop took it just before calling this handler.
		gen := client.SessionLayoutGeneration()
		m.QueueClientEvent(ClientEvent{Type: "resize", ClientCount: clientCount, Width: width, Height: height, Reserve: reserve, Generation: gen})
	})
	// The session killed out from under this client, and the daemon going
	// away. Both leave nothing to render and nothing to reconnect to, so the
	// client says why and quits. The queue is separate from ClientEventChan.
	client.OnSessionEnded(func(name, reason string) {
		clientLog("Session ended: %s (%s)", name, reason)
		if m.QueueSessionEnded(name, reason) {
			clientLog("DaemonExitChan full, dropped the session end")
		}
	})
	client.OnDisconnect(func(err error) {
		clientLog("Daemon connection lost: %v", err)
		if m.QueueDaemonDisconnect(err) {
			clientLog("DaemonExitChan full, dropped the disconnect")
		}
	})
}

// UnwireDaemonClient removes every handler WireDaemonClient registered, so a
// connection this model is about to replace cannot report its own closing as
// a loss, or hand a stale push to the session that took its place. It is the
// same list as WireDaemonClient, and the test that pins that list pins this.
func (m *OS) UnwireDaemonClient(client *session.TUIClient) {
	if client == nil {
		return
	}
	client.OnRemoteCommand(nil)
	client.OnStateSync(nil)
	client.OnClientJoined(nil)
	client.OnAgentMail(nil)
	client.OnDirChanged(nil)
	client.OnPasteRefused(nil)
	client.OnClientLeft(nil)
	client.OnSessionResize(nil)
	client.OnSessionEnded(nil)
	client.OnDisconnect(nil)
}

// clientLog is the standard logger, when verbose logging is on.
func clientLog(format string, args ...any) {
	if verboseLog {
		log.Printf("[CLIENT] "+format, args...)
	}
}

// QueueClientEvent hands a join, leave or resize to the Update loop. When the
// queue is full the oldest event is displaced, not the newest: the latest
// count and the latest size are the ones that matter, and a resize storm that
// dropped its last event left the client laid out for a size the session no
// longer had.
func (m *OS) QueueClientEvent(ev ClientEvent) (displaced bool) {
	if m.ClientEventChan == nil {
		return false
	}
	select {
	case m.ClientEventChan <- ev:
		return false
	default:
	}
	select {
	case <-m.ClientEventChan:
	default:
	}
	select {
	case m.ClientEventChan <- ev:
	default:
	}
	return true
}

// RestoreAttachedSession brings the windows the daemon handed over at attach
// on screen, and announces the attach once they are whole.
//
// It is the one attach sequence. Each entry point used to write its own, and
// the SSH and web copies never fired the attach hook, so a hook that tracks
// which session is live heard from local clients only. A session switch runs
// the same window steps through rebuildForSession.
func (m *OS) RestoreAttachedSession(state *session.SessionState) {
	if state != nil && len(state.Windows) > 0 {
		m.LogInfo("Restoring %d windows from session state", len(state.Windows))
		if err := m.RestoreFromState(state); err != nil {
			m.LogWarn("Failed to restore session state: %v", err)
		}
		m.rehydrateWindows()
		m.LogInfo("Restore complete, %d windows", len(m.Windows))
	} else {
		m.LogInfo("No existing state to restore")
		m.adoptEmptySessionVersion(state)
	}

	m.reportFocusChange()

	// The session is now whole: state restored, PTYs wired, layout applied. A
	// hook that inspects the session here sees what the user is about to see.
	m.FireAttached()
}

// reportFocusChange sends the focus reports for a change of the focused
// pane since the last report: CSI O to the pane that had focus, if it still
// exists, and CSI I to the pane that has it now.
//
// It compares window IDs, not indices. Many paths set FocusedWindow directly
// (an empty workspace, minimizing the last pane, closing the focused pane)
// and an index can point at a different pane after a close. Update calls this
// after every message, so those paths report too, and FocusWindow calls it so
// that a focus change reports at once.
func (m *OS) reportFocusChange() {
	// A client that detached has told the pane it lost focus, and the
	// messages it handles on its way out must not take that back.
	if m.detachFired.Load() {
		return
	}
	current := ""
	if w := m.GetFocusedWindow(); w != nil {
		current = w.ID
	}
	if current == m.focusReportedID {
		return
	}
	if old := m.windowByID(m.focusReportedID); m.focusReportedID != "" && old != nil {
		m.reportPaneFocus(old, false)
	}
	m.focusReportedID = current
	if current != "" {
		m.reportPaneFocus(m.GetFocusedWindow(), true)
	}
}

// reportFocusLost tells the pane that has focus that it lost it, as this
// client leaves.
func (m *OS) reportFocusLost() {
	id := m.focusReportedID
	if id == "" {
		if w := m.GetFocusedWindow(); w != nil {
			id = w.ID
		}
	}
	if w := m.windowByID(id); w != nil {
		m.reportPaneFocus(w, false)
	}
	m.focusReportedID = ""
}

// adoptFocusReport records the focused pane without a report. A state sync
// carries a focus change another client made, and that client reported it.
func (m *OS) adoptFocusReport() {
	m.focusReportedID = ""
	if w := m.GetFocusedWindow(); w != nil {
		m.focusReportedID = w.ID
	}
}

// reportPaneFocus tells a guest about a focus change it requested.
func (m *OS) reportPaneFocus(window *terminal.Window, focused bool) {
	if window == nil || !window.FocusReportingOn() {
		return
	}
	report := "\x1b[O"
	if focused {
		report = "\x1b[I"
	}
	if err := window.SendInput([]byte(report)); err != nil {
		m.LogWarn("Failed to report focus to pane %s: %v", window.ID, err)
	}
}

// adoptEmptySessionVersion records the daemon state version of a session that
// arrived with no windows, which RestoreFromState is not run for.
//
// Every push this client makes echoes the version back as BaseVersion, and the
// daemon reads a BaseVersion of 0 as a client that predates versioning and takes
// its push as sent. A client that attached to an empty session never recorded
// the version, so its first pushes said 0: a window the daemon created in the
// meantime (tuios new-window from a script, say) was missing from them, and the
// push that was taken as sent removed it. A session switch to an empty session
// kept the previous session's version instead, which is a number about a
// different session.
func (m *OS) adoptEmptySessionVersion(state *session.SessionState) {
	m.DaemonStateVersion = 0
	if state != nil {
		m.DaemonStateVersion = state.Version
	}
	// The session holds no tree this client has heard of, so whatever tree it
	// builds from here on is news.
	m.treeSeen = nil
	m.treeDerived = nil
	m.sessionTreeOpsOff = state != nil && !state.LayoutTreeOps
	m.sessionScratchWSOff = state != nil && !state.ScratchWorkspaces
	// The tiling and the layout mode are the session's too, and
	// RestoreFromState, which takes them for a session with windows, does not
	// run for one without. Kept from the session just left, a tiled client
	// came to a new session with tiling on in its own model and off in the
	// daemon's. The [startup] tiling that follows saw it on and did nothing,
	// and the daemon made the first window floating (#480). A nil state is a
	// session with nothing set, so tiling is off.
	if state != nil {
		m.AutoTiling = state.AutoTiling
		m.ApplyLayoutModeName(state.LayoutMode)
	} else {
		m.AutoTiling = false
	}
	// The rail is the session's, and RestoreFromState, which takes it for a
	// session with windows, does not run for one without. A new session made
	// from the switcher is empty, and it has to be offered this client's
	// rail as any other new session is.
	m.joinSession(state)
}

// rehydrateWindows wires the restored windows to their daemon PTYs and lays
// them out for this client's screen. RestoreFromState has already run.
func (m *OS) rehydrateWindows() {
	if err := m.RestoreTerminalStates(); err != nil {
		m.LogWarn("Failed to restore terminal states: %v", err)
	}
	// Subscribes to the PTYs of the windows in the current workspace.
	if err := m.SetupPTYOutputHandlers(); err != nil {
		m.LogWarn("Failed to set up PTY handlers: %v", err)
	}
	// Re-tile for this client's screen, which is not the screen the state was
	// saved from.
	if m.AutoTiling {
		m.TileAllWindows()
	}
	// The daemon's PTYs still have the size the last client left them at.
	m.SyncDaemonPTYDimensions()
}
