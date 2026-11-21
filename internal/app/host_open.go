package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

const hostOpenWait = time.Minute

const hostOpenPoll = time.Second

const hostOpenRetryGap = 2 * time.Second

type hostOpenPending struct {
	host, name string
	gen        uint64
	until      time.Time
	dialing    bool
	notBefore  time.Time
	wasUp      bool
	signIn     bool
	workspace  int
	paneID     string
}

func (m *OS) openRemoteSession(host, sessionName string) {
	m.openRemoteSessionAt(host, sessionName, 0, "")
}

func (m *OS) openRemoteSessionAt(host, sessionName string, workspace int, paneID string) {
	m.clearSidebarReturn()
	if m.sessionSwitchHook != nil {
		if err := m.sessionSwitchHook(host, sessionName); err != nil {
			m.reportSwitchFailure(err)
		}
		return
	}
	m.cancelHostOpen()
	p := &hostOpenPending{host: host, name: sessionName, gen: m.hostOpenGen, until: time.Now().Add(hostOpenWait), workspace: workspace, paneID: paneID}
	p.wasUp = m.hostWasUp(host)
	if m.hostReconnect != nil {
		m.hostReconnect = nil
		m.hostReconnectGen++
		m.hostReconnectDeferred = true
	}
	m.hostOpen = p
	cmds := []tea.Cmd{retryHostCmd(host)}
	if m.hostIsUp(host) {
		m.dialHostOpen(p)
	} else {
		m.ShowNotification(hostOpenWaitNote(p)+".", "info", m.Settings.NotificationDuration)
		cmds = append(cmds, m.federationRefreshTick(hostOpenPoll))
	}
	m.queueSidebarCmd(tea.Batch(cmds...))
}

func (m *OS) cancelHostOpen() {
	m.hostOpenGen++
	m.hostOpen = nil
}

// CancelHostOpen is cancelHostOpen for esc. It says so when it stopped a
// click.
func (m *OS) CancelHostOpen() tea.Cmd {
	p := m.hostOpen
	if p == nil {
		return nil
	}
	m.cancelHostOpen()
	m.ShowNotification("Stopped connecting to "+printableTitle(p.host)+".", "info", m.Settings.NotificationDuration)
	return m.resumeAfterHostOpen()
}

func (m *OS) hostWasUp(name string) bool {
	for _, h := range m.FederationHosts {
		if h.Name == name {
			return h.LastOK != 0 || h.Status == string(federation.StatusUp) || h.Status == string(federation.StatusReconnecting)
		}
	}
	return false
}

func (m *OS) dialHostOpen(p *hostOpenPending) {
	if m.DaemonClient == nil {
		m.hostOpen = nil
		m.ShowNotification("Switch failed: not in daemon mode", "error", m.Settings.NotificationDuration*2)
		return
	}
	p.dialing = true
	d := m.hostDialFor(p.host, p.name, false, false)
	ch := m.sessionCreateChan()
	gen, host := p.gen, p.host
	go func() {
		client, state, err := d.open()
		ch <- SessionCreatedMsg{Err: err, Client: client, State: state, Host: host, Switched: true, OpenGen: gen}
	}()
}

func (m *OS) advanceHostOpen() tea.Cmd {
	p := m.hostOpen
	if p == nil || p.dialing {
		return nil
	}
	now := time.Now()
	status, known := "", false
	for _, h := range m.FederationHosts {
		if h.Name == p.host {
			status, known = h.Status, true
			break
		}
	}
	if !known {
		return m.endHostOpen(p, printableTitle(p.host)+" is not in the hosts list now. Add it with 'tuios hosts add' to open its sessions.")
	}
	switch federation.Status(status) {
	case federation.StatusUp:
		if now.Before(p.notBefore) {
			return nil
		}
		m.dialHostOpen(p)
		return nil
	case federation.StatusApproval:
		if p.signIn {
			if now.After(p.until) {
				return m.endHostOpen(p, printableTitle(p.host)+" still waits for a Tailscale sign-in. Sign in, then select the session again.")
			}
			return nil
		}
		p.signIn = true
		if until := now.Add(hostSignInWatch); until.After(p.until) {
			p.until = until
		}
		return m.openHostSignIn(p.host)
	case federation.StatusNoDaemon, federation.StatusNoBinary, federation.StatusIncompatible:
		return m.endHostOpen(p, printableTitle(p.host)+": "+m.hostReason(p.host))
	}
	if now.After(p.until) {
		return m.endHostOpen(p, hostOpenExpiredNote(p, m.hostReason(p.host)))
	}
	return nil
}

func (m *OS) endHostOpen(p *hostOpenPending, note string) tea.Cmd {
	m.cancelHostOpen()
	m.ShowNotification(note, "warning", m.Settings.NotificationWarningDuration*2)
	return m.resumeAfterHostOpen()
}

func (m *OS) resumeAfterHostOpen() tea.Cmd {
	if !m.hostReconnectDeferred || m.hostOpen != nil || m.hostReconnect != nil {
		return nil
	}
	m.hostReconnectDeferred = false
	return m.beginHostReconnect(nil)
}

func hostOpenExpiredNote(p *hostOpenPending, reason string) string {
	what := "tuios could not connect to " + printableTitle(p.host) + " in one minute."
	if p.wasUp {
		what = "The link to " + printableTitle(p.host) + " did not come back in one minute."
	}
	if reason != "" {
		what += " " + reason
	}
	return what + " Run 'tuios hosts' to see the host, then select the session again."
}

func (m *OS) hostReason(name string) string {
	for _, h := range m.FederationHosts {
		if h.Name == name {
			return h.Reason
		}
	}
	return ""
}

func (m *OS) finishHostOpen(msg SessionCreatedMsg) tea.Cmd {
	p := m.hostOpen
	if p == nil || p.gen != msg.OpenGen {
		if msg.Client != nil {
			_ = msg.Client.Close()
		}
		return nil
	}
	p.dialing = false
	if msg.Err != nil {
		if !hostReconnectFinal(msg.Err) && time.Now().Before(p.until) {
			p.notBefore = time.Now().Add(hostOpenRetryGap)
			return tea.Batch(retryHostCmd(p.host), m.federationRefreshTick(hostOpenPoll))
		}
		m.cancelHostOpen()
		m.reportSwitchFailure(&hostSwitchError{msg: hostAttachRefusal(p.host, msg.Err)})
		return m.resumeAfterHostOpen()
	}
	m.hostOpen = nil
	m.adoptHostClient(msg.Client, msg.State, msg.Host)
	m.applyStartupToUnarranged()
	if p.workspace > 0 || p.paneID != "" {
		m.focusSwitchTarget(p.workspace, p.paneID)
		m.SyncStateToDaemon()
	}
	return nil
}

func (m *OS) focusSwitchTarget(workspace int, paneID string) {
	if workspace > 0 && workspace != m.CurrentWorkspace {
		m.SwitchToWorkspace(workspace)
	}
	if paneID != "" {
		_ = m.FocusPane(paneID)
	}
}

func hostOpenWaitNote(p *hostOpenPending) string {
	if p.wasUp {
		return "Reconnecting to " + printableTitle(p.host)
	}
	return "Connecting to " + printableTitle(p.host)
}

func (m *OS) hostOpenNote() string {
	p := m.hostOpen
	if p == nil {
		return ""
	}
	name := p.host
	const room = 12
	if len(name) > room {
		name = name[:room-1] + "…"
	}
	if p.dialing || !p.wasUp {
		return " Connecting to " + name + " "
	}
	return " Reconnecting to " + name + " "
}
