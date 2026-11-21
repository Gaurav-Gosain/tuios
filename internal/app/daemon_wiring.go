package app

import (
	"log"
	"maps"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func queueSidebarSessionState(events chan sidebarSessionEvent, mu *sync.Mutex, event sidebarSessionEvent) {
	mu.Lock()
	defer mu.Unlock()
	select {
	case queued := <-events:
		if queued.generation > event.generation ||
			(queued.generation == event.generation && queued.state != nil && event.state != nil &&
				queued.state.SnapshotSeq > 0 && event.state.SnapshotSeq > 0 && queued.state.SnapshotSeq > event.state.SnapshotSeq) {
			event = queued
		}
	default:
	}
	events <- event
}

// WireSidebarSessionClient wires the separate passive viewer. Capture the
// channels on the UI goroutine so shutdown cannot race a daemon callback.
func (m *OS) WireSidebarSessionClient(client *session.TUIClient, edge sidebarEdge, generation uint64) {
	events, states, done := m.sidebarSessionEvents, m.sidebarSessionStateEvents[edge], m.sidebarSessionDone
	stateMu := &m.sidebarSessionStateMu[edge]
	queueTerminal := func(event sidebarSessionEvent) {
		select {
		case events <- event:
		case <-done:
		}
	}
	client.OnStateSync(func(state *session.SessionState, _, _ string) {
		queueSidebarSessionState(states, stateMu, sidebarSessionEvent{edge: edge, generation: generation, kind: "state", state: state})
	})
	client.OnSessionEnded(func(_, reason string) {
		queueTerminal(sidebarSessionEvent{edge: edge, generation: generation, kind: "ended", err: errfmt.Errorf("session ended: %s", reason)})
	})
	client.OnDisconnect(func(err error) {
		queueTerminal(sidebarSessionEvent{edge: edge, generation: generation, kind: "ended", err: err})
	})
}

// WireDaemonClient registers this model as the listener for everything the
// daemon sends an attached client: routed verbs, state syncs, peers joining and
// leaving, the session's size, the session ending and the connection going.
func (m *OS) WireDaemonClient(client *session.TUIClient) {
	if client == nil {
		return
	}
	m.foreignSessionReplan = true
	if m.hostFocus != hostFocusUnknown {
		focused := m.hostFocus == hostFocusIn
		go func() { _ = client.ReportHostFocus(focused) }()
	}
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
		m.QueueClientEvent(ClientEvent{Type: "joined", ClientID: clientID, ClientCount: clientCount, Width: width, Height: height, Attach: client.AttachGeneration()})
	})
	client.OnAgentMail(func(payload session.AgentMailPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "agent-mail", Mail: payload, Attach: client.AttachGeneration()}) {
			clientLog("ClientEventChan full, displaced an event for agent mail")
		}
	})
	client.OnHostsChanged(func(session.HostsChangedPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "hosts-changed"}) {
			clientLog("ClientEventChan full, displaced an event for a hosts change")
		}
	})
	client.OnSessionsChanged(func() {
		if m.QueueClientEvent(ClientEvent{Type: "sessions-changed"}) {
			clientLog("ClientEventChan full, displaced an event for a listing change")
		}
	})
	client.OnPaneMark(func(mark session.PaneMarkPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "pane-mark", PaneMark: mark}) {
			clientLog("ClientEventChan full, displaced an event for a pane mark")
		}
	})
	client.OnPluginApprovals(func(p session.PluginApprovalsPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "plugin-approvals", PluginAsks: p.Asks}) {
			clientLog("ClientEventChan full, displaced an event for plugin approvals")
		}
	})
	client.OnPrompt(func(p session.PromptPayload) {
		if m.QueueClientEvent(ClientEvent{Type: "prompt", Prompt: p}) {
			clientLog("ClientEventChan full, displaced an event for a prompt")
		}
	})
	client.OnPluginUI(func(p session.PluginUIPayload) {
		if p.Notice == "" && m.queuePluginCells(p) {
			return
		}
		if m.QueueClientEvent(ClientEvent{Type: "plugin-ui", PluginUI: p}) {
			clientLog("ClientEventChan full, displaced an event for a plugin notice")
		}
	})
	m.initTML()
	client.OnTMLSurface(m.queueTMLSurface)
	m.tml.far.drop()
	client.OnPasteRefused(func(_, message string) {
		if m.QueueClientEvent(ClientEvent{Type: "paste-refused", Reason: message}) {
			clientLog("ClientEventChan full, displaced an event for a refused paste")
		}
	})
	client.OnDirChanged(m.fileWatch.remoteDirChanged)
	client.OnClientLeft(func(clientID string, clientCount int) {
		clientLog("Client left: %s (remaining: %d)", shortID(clientID), clientCount)
		m.QueueClientEvent(ClientEvent{Type: "left", ClientID: clientID, ClientCount: clientCount, Attach: client.AttachGeneration()})
	})
	client.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		clientLog("Session resize: %dx%d chrome %+v (clients: %d)", width, height, reserve, clientCount)
		gen := client.SessionLayoutGeneration()
		m.QueueClientEvent(ClientEvent{Type: "resize", ClientCount: clientCount, Width: width, Height: height, Reserve: reserve, Generation: gen, Attach: client.AttachGeneration()})
	})
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
	client.OnTMLSurface(nil)
	client.OnDirChanged(nil)
	client.OnPasteRefused(nil)
	client.OnClientLeft(nil)
	client.OnSessionResize(nil)
	client.OnSessionEnded(nil)
	client.OnDisconnect(nil)
}

func (m *OS) staleAttach(attach uint64) bool {
	return attach != 0 && m.DaemonClient != nil && attach != m.DaemonClient.AttachGeneration()
}

func clientLog(format string, args ...any) {
	if verboseLog() {
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
	m.pruneTMLViews(state)
	if m.DaemonClient != nil {
		m.sessionClients = m.DaemonClient.AttachClientCount()
	}
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
		if m.DaemonClient != nil {
			m.SessionReserve = m.DaemonClient.SessionLayoutReserve()
		}
	}

	m.reportFocusChange()

	m.FireAttached()
}

func (m *OS) reportFocusChange() {
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

func (m *OS) adoptFocusReport() {
	m.focusReportedID = ""
	if w := m.GetFocusedWindow(); w != nil {
		m.focusReportedID = w.ID
	}
}

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

func (m *OS) adoptEmptySessionVersion(state *session.SessionState) {
	m.DaemonStateVersion = 0
	if state != nil {
		m.DaemonStateVersion = state.Version
	}
	m.treeSeen = nil
	m.treeDerived = nil
	m.WorkspaceLayoutMode = nil
	m.sessionLayoutMode = ""
	if state != nil {
		m.AutoTiling = state.AutoTiling
		m.WorkspaceLayoutMode = make(map[int]string, len(state.WorkspaceLayoutModes))
		maps.Copy(m.WorkspaceLayoutMode, state.WorkspaceLayoutModes)
		m.ApplyLayoutModeName(state.LayoutMode)
		m.adoptSessionLayoutMode(state)
	} else {
		m.AutoTiling = false
	}
	m.joinSession(state)
}

func (m *OS) rehydrateWindows() {
	if err := m.RestoreTerminalStates(); err != nil {
		m.LogWarn("Failed to restore terminal states: %v", err)
	}
	if err := m.SetupPTYOutputHandlers(); err != nil {
		m.LogWarn("Failed to set up PTY handlers: %v", err)
	}
	if m.AutoTiling {
		m.TileAllWindows()
	}
	m.SyncDaemonPTYDimensions()
}
