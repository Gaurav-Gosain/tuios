package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Opening a session on another machine from the rail.
//
// A session row of a machine is always a target, whatever state its link is
// in. A click on a machine that is up dials the session off the Update
// goroutine, so a slow link does not freeze the screen. A click on a machine
// that is not up asks the daemon to dial it now, says "Reconnecting to HOST"
// in the dock, and opens the session when the link comes up.
//
// Every click also asks the daemon to retry the host. On a link that is up
// that is a ping: after a laptop wakes, a link can look up and carry nothing,
// and the ping finds that out in seconds instead of when ssh gives up.

// hostOpenWait is how long a click waits for a machine to come up.
const hostOpenWait = time.Minute

// hostOpenPoll is how often the rail asks the daemon for the machine's state
// while a click waits for it.
const hostOpenPoll = time.Second

// hostOpenRetryGap is the least time between a dial that failed and the next
// one. The snapshot can still say up for a moment after the link went away.
const hostOpenRetryGap = 2 * time.Second

// hostOpenPending is the session a click asked for on another machine.
type hostOpenPending struct {
	host, name string
	// gen tells this click's dial apart from the dial of an older click.
	gen uint64
	// until is when the click gives up.
	until time.Time
	// dialing says a dial is running.
	dialing bool
	// notBefore holds the next dial back after one that failed.
	notBefore time.Time
	// wasUp says the host's link was up at some time, so the dock says
	// "Reconnecting" and not "Connecting".
	wasUp bool
	// signIn says the click already opened the host's Tailscale sign-in.
	signIn bool
}

// openRemoteSession attaches a session that lives on another machine, in this
// client. The connection goes through this machine's daemon over its link,
// and the session is drawn here with this machine's theme and config.
func (m *OS) openRemoteSession(host, sessionName string) {
	m.clearSidebarReturn() // opening the session is where the user asked to end up
	if m.sessionSwitchHook != nil {
		// A test records where the switch goes. See switchSession.
		if err := m.sessionSwitchHook(host, sessionName); err != nil {
			m.reportSwitchFailure(err)
		}
		return
	}
	m.cancelHostOpen()
	p := &hostOpenPending{host: host, name: sessionName, gen: m.hostOpenGen, until: time.Now().Add(hostOpenWait)}
	p.wasUp = m.hostWasUp(host)
	// The newest request wins. A reconnect to the attached host would pull
	// the person back when it lands, so it stops here, and starts again if
	// this click fails.
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

// cancelHostOpen drops the click that waits for a machine, if any. A dial it
// started may still answer, and the new gen makes finishHostOpen close it.
// Every switch, create and detach calls it, so an old click never pulls the
// person away from what they chose after it.
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

// hostWasUp reports whether the last snapshot says the host's link was up
// at some time.
func (m *OS) hostWasUp(name string) bool {
	for _, h := range m.FederationHosts {
		if h.Name == name {
			return h.LastOK != 0 || h.Status == string(federation.StatusUp) || h.Status == string(federation.StatusReconnecting)
		}
	}
	return false
}

// dialHostOpen dials the session p names, off the Update goroutine. The
// answer is a SessionCreatedMsg with p's gen.
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

// advanceHostOpen runs on each host snapshot while a click waits for its
// machine. It dials when the link is up, opens the sign-in page when the host
// waits for one, and gives up when the wait is over, the host left the
// table, or the host is in a state that waiting cannot change.
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
		// The host waits for a Tailscale sign-in. The click opens it, as the
		// sign-in control on the rail does, and keeps waiting for the link.
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

// endHostOpen stops the click p with a warning that says why.
func (m *OS) endHostOpen(p *hostOpenPending, note string) tea.Cmd {
	m.cancelHostOpen()
	m.ShowNotification(note, "warning", m.Settings.NotificationWarningDuration*2)
	return m.resumeAfterHostOpen()
}

// resumeAfterHostOpen starts the reconnect to the attached host that a click
// put off, once no click waits. A click that lands adopts a new client and
// clears the debt, so this runs only when the click failed, ran out or was
// replaced by a switch that left the client on the dead connection. The host
// snapshot calls it too, which covers a click cancelled by a path that has
// no command to return.
func (m *OS) resumeAfterHostOpen() tea.Cmd {
	if !m.hostReconnectDeferred || m.hostOpen != nil || m.hostReconnect != nil {
		return nil
	}
	m.hostReconnectDeferred = false
	return m.beginHostReconnect(nil)
}

// hostOpenExpiredNote says that the wait for a host ran out, why when the
// daemon said, and what to do.
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

// hostReason is the sentence the last snapshot carries for a machine.
func (m *OS) hostReason(name string) string {
	for _, h := range m.FederationHosts {
		if h.Name == name {
			return h.Reason
		}
	}
	return ""
}

// finishHostOpen takes the answer to a dial dialHostOpen started.
func (m *OS) finishHostOpen(msg SessionCreatedMsg) tea.Cmd {
	p := m.hostOpen
	if p == nil || p.gen != msg.OpenGen {
		// The person asked for something else while this dial ran.
		if msg.Client != nil {
			_ = msg.Client.Close()
		}
		return nil
	}
	p.dialing = false
	if msg.Err != nil {
		if !hostReconnectFinal(msg.Err) && time.Now().Before(p.until) {
			// The link went away under the dial. Wait for it to come back.
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
	return nil
}

// hostOpenWaitNote is what the dock and the notice say while a click waits
// for a host that is not up.
func hostOpenWaitNote(p *hostOpenPending) string {
	if p.wasUp {
		return "Reconnecting to " + printableTitle(p.host)
	}
	return "Connecting to " + printableTitle(p.host)
}

// hostOpenNote is the dock's line while a click waits for its machine.
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
