package app

import (
	"cmp"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const daemonDropBudget = 6 * time.Second

func (m *OS) daemonRestartBudget() time.Duration {
	return time.Duration(cmp.Or(max(m.Settings.ReconnectSeconds, 0), config.DefaultReconnectSeconds)) * time.Second
}

const daemonDialTimeout = 2 * time.Second

const daemonWaitNote = "Waiting for the daemon. Keys are not sent."

const daemonReconnectEvery = 500 * time.Millisecond

type daemonReconnect struct {
	gen           int
	sessionName   string
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

func (m *OS) beginDaemonReconnect() tea.Cmd {
	if m.QuitRequested || m.AttachedHost != "" || m.Client != ClientLocal || m.RemoteClient ||
		!m.IsDaemonSession || m.SessionName == "" || m.daemonReconnect != nil || m.DaemonClient == nil {
		return nil
	}
	if bye := m.DaemonClient.Goodbye(); bye != nil && bye.Reason == session.GoodbyeStop {
		return nil
	}
	since, budget := time.Now(), min(daemonDropBudget, m.daemonRestartBudget())
	if bye := m.DaemonClient.Goodbye(); bye != nil && bye.Reason == session.GoodbyeRestart {
		since, budget = bye.At, m.daemonRestartBudget()
	}
	if m.droppedByUpgrade() {
		budget = m.daemonRestartBudget()
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

func (m *OS) daemonReconnectWait() tea.Cmd {
	gen := m.daemonReconnect.gen
	return tea.Tick(daemonReconnectEvery, func(time.Time) tea.Msg { return daemonReconnectTickMsg{gen: gen} })
}

func (m *OS) handleDaemonReconnectTick(msg daemonReconnectTickMsg) tea.Cmd {
	r := m.daemonReconnect
	if r == nil || r.gen != msg.gen {
		return nil
	}
	gen, name, width, height := r.gen, r.sessionName, r.width, r.height
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

func (m *OS) handleDaemonReconnectResult(msg daemonReconnectResultMsg) tea.Cmd {
	r := m.daemonReconnect
	if r == nil || r.gen != msg.gen {
		if msg.client != nil {
			_ = msg.client.Close()
		}
		return nil
	}
	if msg.err == nil && msg.client != nil {
		if exec := m.upgradeToFollow(); exec != nil {
			return m.followUpgrade(exec, msg.client)
		}
		return m.resumeOnNewDaemon(msg.client, msg.state)
	}
	if session.ProtocolRefused(msg.err) {
		if exec := m.upgradeToFollow(); exec != nil {
			return m.followUpgrade(exec, nil)
		}
	}
	if session.AttachRefused(msg.err) || session.ProtocolRefused(msg.err) || time.Since(r.since) > r.budget {
		m.LogWarn("No new daemon took the session back: %v", msg.err)
		m.daemonReconnect = nil
		m.daemonWaiting.Store(false)
		m.ExitReason = m.daemonGoneReason()
		return m.QuitCmd()
	}
	return m.daemonReconnectWait()
}

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
	m.rebuildForSessionOn(state, m.Width, m.Height)
	m.SyncDockContext()
	m.MarkAllDirty()
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
	return m.CockpitSideFetch()
}

func (m *OS) droppedByUpgrade() bool {
	if m.DaemonClient == nil {
		return false
	}
	_, upgraded := m.DaemonClient.UpgradedTo()
	return upgraded
}

func (m *OS) daemonGoneReason() ExitReason {
	if m.droppedByUpgrade() {
		return ExitDaemonUpgraded
	}
	return ExitDaemonLost
}

// ReconnectingToDaemon reports whether the client waits for a new daemon.
func (m *OS) ReconnectingToDaemon() bool { return m.daemonReconnect != nil }
