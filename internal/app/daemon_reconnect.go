package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Getting the session back after the daemon on this machine restarted.
//
// A restart from another client, from tuios restart-server or from hosts sync
// stops the daemon this client is attached to, and a new one takes its place
// a moment later with the same sessions. A client that quit on the lost
// connection made the person attach again by hand. So a local client waits a
// short time for a new daemon instead: it dials again a few times, and when
// its session is there it attaches and draws it. The panes are new, so the
// screen is rebuilt from the state the attach returns.
//
// The daemon says why it stops (see session/daemon_goodbye.go). After
// "restart" the client waits; after "stop" it quits at once, as it always did;
// after silence, a crash, it waits the shorter time. Keys typed while it
// waits are not sent, and the wait note says so.
//
// A live upgrade (tuios daemon upgrade, see session/handoff_clients.go) drops
// the client too, and says so first. The new image holds the same sessions
// and the same running panes, so the client waits for it as after a restart.
// The image may speak another wire protocol than this client's build. The
// handshake finds that before the attach, from the new daemon's own hello
// answer, so nothing of that daemon is drawn: the client stops waiting and
// exits saying the daemon is being upgraded and how to attach again. A wait
// after an upgrade that ends any other way says the same.
//
// The wait is short and has an end. A daemon that stopped for good, a crash
// with no restart, ends the client as it always did, after the budget, with
// the same message. Nothing runs while the client is attached: the dials
// start only when the connection is lost, and stop when it is back.

// The wait for a new daemon. After a restart the daemon said it, so the wait
// counts from its goodbye and has room for the save, the stop and the start.
// After a drop it did not announce (a crash) the wait is shorter: a new daemon
// may not come at all.
const (
	daemonRestartBudget = 15 * time.Second
	daemonDropBudget    = 6 * time.Second
)

// daemonDialTimeout bounds one dial's handshake. A daemon that is still
// starting does not hold the dial for the 30 seconds an attach is given.
const daemonDialTimeout = 2 * time.Second

// daemonWaitNote is what the dock says while the client waits, or the middle
// of the screen when the dock is hidden.
const daemonWaitNote = "Waiting for the daemon. Keys are not sent."

// daemonReconnectEvery is the time between two dials.
const daemonReconnectEvery = 500 * time.Millisecond

// daemonReconnect is a wait for a new daemon. Nil while attached.
type daemonReconnect struct {
	gen         int
	sessionName string
	// since is when the wait began: the goodbye of a daemon that said it
	// restarts, or the drop. budget is how long it lasts.
	since         time.Time
	budget        time.Duration
	width, height int
}

type (
	daemonReconnectTickMsg   struct{ gen int }
	daemonReconnectResultMsg struct {
		gen    int
		client *session.TUIClient
		state  *session.SessionState
		err    error
	}
)

// beginDaemonReconnect starts the wait for a lost local connection. It returns
// nil when the client should quit instead: a client of another machine, a
// served client, a deliberate quit, or a wait already under way.
func (m *OS) beginDaemonReconnect() tea.Cmd {
	if m.QuitRequested || m.AttachedHost != "" || m.Client != ClientLocal || m.RemoteClient ||
		!m.IsDaemonSession || m.SessionName == "" || m.daemonReconnect != nil || m.DaemonClient == nil {
		return nil
	}
	// A daemon that said it stops is not coming back.
	if bye := m.DaemonClient.Goodbye(); bye != nil && bye.Reason == session.GoodbyeStop {
		return nil
	}
	since, budget := time.Now(), daemonDropBudget
	if bye := m.DaemonClient.Goodbye(); bye != nil && bye.Reason == session.GoodbyeRestart {
		since, budget = bye.At, daemonRestartBudget
	}
	// A live upgrade said it as it dropped this client: the new image comes,
	// as a restarted daemon does.
	if m.droppedByUpgrade() {
		budget = daemonRestartBudget
	}
	m.daemonReconnectGen++
	m.daemonReconnect = &daemonReconnect{
		gen:         m.daemonReconnectGen,
		sessionName: m.SessionName,
		since:       since,
		budget:      budget,
		width:       m.Width,
		height:      m.Height,
	}
	m.daemonWaiting.Store(true)
	m.LogWarn("The daemon connection was lost. Waiting for a new daemon.")
	m.MarkAllDirty()
	return m.daemonReconnectWait()
}

// daemonReconnectWait schedules the next dial.
func (m *OS) daemonReconnectWait() tea.Cmd {
	gen := m.daemonReconnect.gen
	return tea.Tick(daemonReconnectEvery, func(time.Time) tea.Msg { return daemonReconnectTickMsg{gen: gen} })
}

// handleDaemonReconnectTick dials once, off the Update goroutine.
func (m *OS) handleDaemonReconnectTick(msg daemonReconnectTickMsg) tea.Cmd {
	r := m.daemonReconnect
	if r == nil || r.gen != msg.gen {
		return nil
	}
	gen, name, width, height := r.gen, r.sessionName, r.width, r.height
	// The whole dial ends with the wait. A new daemon that says hello and
	// then stalls would otherwise hold the attach for its 30 seconds.
	end := r.since.Add(r.budget)
	version := m.clientVersionForReconnect()
	caps := m.clientCapabilities()
	reserve := m.ownLayoutReserveForReconnect()
	agent, display := "", session.ClientDisplay{}
	upgradeDialog := false
	if m.DaemonClient != nil {
		agent, display = m.DaemonClient.SSHAuthSock, m.DaemonClient.Display
		upgradeDialog = m.DaemonClient.UpgradeDialog
	}
	return func() tea.Msg {
		client := session.NewTUIClient()
		client.SetOwnLayoutReserve(reserve)
		// Getting the session back is not the person attaching, so it must
		// not detach the other clients under single_client.
		client.Reconnect = true
		client.HandshakeTimeout = max(min(daemonDialTimeout, time.Until(end)), time.Millisecond)
		client.AttachDeadline = end
		client.SSHAuthSock, client.Display = agent, display
		client.UpgradeDialog = upgradeDialog
		client.UseCallerTerm()
		if err := client.ConnectWithCapabilities(version, width, height, caps); err != nil {
			_ = client.Close()
			return daemonReconnectResultMsg{gen: gen, err: err}
		}
		state, err := client.AttachSession(name, false, width, height)
		if err != nil {
			_ = client.Close()
			return daemonReconnectResultMsg{gen: gen, err: err}
		}
		return daemonReconnectResultMsg{gen: gen, client: client, state: state}
	}
}

// handleDaemonReconnectResult applies one dial: back in the session, another
// dial, or the end of the wait.
func (m *OS) handleDaemonReconnectResult(msg daemonReconnectResultMsg) tea.Cmd {
	r := m.daemonReconnect
	if r == nil || r.gen != msg.gen {
		if msg.client != nil {
			_ = msg.client.Close()
		}
		return nil
	}
	if msg.err == nil && msg.client != nil {
		return m.resumeOnNewDaemon(msg.client, msg.state)
	}
	// A daemon that answers and does not hold the session, or does not speak
	// this client's protocol, will not take it back by waiting.
	if session.AttachRefused(msg.err) || session.ProtocolRefused(msg.err) || time.Since(r.since) > r.budget {
		m.LogWarn("No new daemon took the session back: %v", msg.err)
		m.daemonReconnect = nil
		m.daemonWaiting.Store(false)
		m.ExitReason = m.daemonGoneReason()
		return m.QuitCmd()
	}
	return m.daemonReconnectWait()
}

// resumeOnNewDaemon puts the client on the session the new daemon restored.
func (m *OS) resumeOnNewDaemon(client *session.TUIClient, state *session.SessionState) tea.Cmd {
	upgraded := m.droppedByUpgrade()
	m.daemonReconnect = nil
	m.daemonWaiting.Store(false)
	if old := m.DaemonClient; old != nil {
		m.UnwireDaemonClient(old)
		_ = old.Close()
	}
	m.DaemonClient = client
	m.WireDaemonClient(client)
	client.StartReadLoop()
	m.SessionName = client.SessionName()
	m.forgetSyncedState()
	m.SubscribedPTYs = make(map[string]bool)
	m.paneRequests = nil
	// The new daemon started new shells, so every pane is new.
	m.rebuildForSessionOn(state, m.Width, m.Height)
	m.SyncDockContext()
	m.MarkAllDirty()
	// The version notice named the daemon that stopped. When this window
	// still differs from the new one, the line below says so.
	m.clearRestartNotice()
	text := "The daemon restarted. tuios reattached."
	if upgraded {
		text = "The daemon was upgraded. tuios reattached."
	}
	if c, d := client.BuildMismatch(); c != "" {
		on := "restarted on " + d
		if upgraded {
			on = "was upgraded to " + d
		}
		text = fmt.Sprintf("The daemon %s. tuios reattached. This window still runs %s: start tuios again to use %s.", on, c, d)
	}
	m.LogInfo("%s", text)
	m.ShowNotification(text, "success", 2*m.Settings.NotificationDuration)
	// An open agents view picks its live pane again: no input may come to
	// run the follow, and the box stays empty until one does.
	return m.CockpitSideFetch()
}

// droppedByUpgrade reports whether a live upgrade told this client it was
// dropping it.
func (m *OS) droppedByUpgrade() bool {
	if m.DaemonClient == nil {
		return false
	}
	_, upgraded := m.DaemonClient.UpgradedTo()
	return upgraded
}

// daemonGoneReason is why the client exits when the daemon is gone and no new
// one took the session back.
func (m *OS) daemonGoneReason() ExitReason {
	if m.droppedByUpgrade() {
		return ExitDaemonUpgraded
	}
	return ExitDaemonLost
}

// ReconnectingToDaemon reports whether the client waits for a new daemon.
func (m *OS) ReconnectingToDaemon() bool { return m.daemonReconnect != nil }
