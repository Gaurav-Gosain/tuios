package app

import (
	"log"

	"github.com/Gaurav-Gosain/tuios/internal/session"
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
		clientLog("State sync: trigger=%s, source=%s", triggerType, shortClientID(sourceID))
		if m.QueueStateSync(state) {
			clientLog("StateSyncChan full, superseded the queued snapshot")
		}
	})
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		clientLog("Client joined: %s (total: %d, size: %dx%d)", shortClientID(clientID), clientCount, width, height)
		m.QueueClientEvent(ClientEvent{Type: "joined", ClientID: clientID, ClientCount: clientCount, Width: width, Height: height})
	})
	client.OnClientLeft(func(clientID string, clientCount int) {
		clientLog("Client left: %s (remaining: %d)", shortClientID(clientID), clientCount)
		m.QueueClientEvent(ClientEvent{Type: "left", ClientID: clientID, ClientCount: clientCount})
	})
	// The session's size is the minimum over its clients. The geometry
	// mutation (TileAllWindows, emulator resizes) happens in Update.
	client.OnSessionResize(func(width, height, clientCount int, reserve session.LayoutReserve) {
		clientLog("Session resize: %dx%d chrome %+v (clients: %d)", width, height, reserve, clientCount)
		m.QueueClientEvent(ClientEvent{Type: "resize", ClientCount: clientCount, Width: width, Height: height, Reserve: reserve})
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

// shortClientID is the first 8 characters of a client id for a log line, or
// the whole id when it is shorter, so a non-UUID id cannot panic the call.
func shortClientID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
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
	}

	// The session is now whole: state restored, PTYs wired, layout applied. A
	// hook that inspects the session here sees what the user is about to see.
	m.FireAttached()
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
