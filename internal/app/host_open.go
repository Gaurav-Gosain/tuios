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
	m.hostOpenGen++
	p := &hostOpenPending{host: host, name: sessionName, gen: m.hostOpenGen, until: time.Now().Add(hostOpenWait)}
	m.hostOpen = p
	cmds := []tea.Cmd{retryHostCmd(host)}
	if m.hostIsUp(host) {
		m.dialHostOpen(p)
	} else {
		m.ShowNotification("Reconnecting to "+printableTitle(host)+".", "info", m.Settings.NotificationDuration)
		cmds = append(cmds, m.federationRefreshTick(hostOpenPoll))
	}
	m.queueSidebarCmd(tea.Batch(cmds...))
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
// machine. It dials when the link is up, and gives up when the wait is over
// or the machine is in a state that waiting cannot change.
func (m *OS) advanceHostOpen() {
	p := m.hostOpen
	if p == nil || p.dialing {
		return
	}
	now := time.Now()
	status := m.hostStatusByName(p.host)
	switch federation.Status(status) {
	case federation.StatusUp:
		if now.Before(p.notBefore) {
			return
		}
		m.dialHostOpen(p)
		return
	case federation.StatusNoDaemon, federation.StatusNoBinary, federation.StatusIncompatible, federation.StatusApproval:
		m.hostOpen = nil
		m.ShowNotification(printableTitle(p.host)+": "+m.hostReason(p.host), "warning", m.Settings.NotificationWarningDuration*2)
		return
	}
	if now.After(p.until) {
		m.hostOpen = nil
		m.ShowNotification(hostReconnectReason(p.host, nil), "warning", m.Settings.NotificationWarningDuration*2)
	}
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
		m.hostOpen = nil
		m.reportSwitchFailure(&hostSwitchError{msg: hostAttachRefusal(p.host, msg.Err)})
		return nil
	}
	m.hostOpen = nil
	m.adoptHostClient(msg.Client, msg.State, msg.Host)
	m.applyStartupToUnarranged()
	return nil
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
	if p.dialing {
		return " Connecting to " + name + " "
	}
	return " Reconnecting to " + name + " "
}
