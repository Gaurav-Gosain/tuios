package app

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	hostReconnectFirstDelay = time.Second
	hostReconnectMaxDelay   = 15 * time.Second
	hostReconnectBudget     = 3 * time.Minute
)

var reconnectBudget = sync.OnceValue(func() time.Duration {
	if v := os.Getenv("TUIOS_HOST_RECONNECT_BUDGET"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return hostReconnectBudget
})

type hostReconnect struct {
	gen           int
	host          string
	sessionName   string
	attempt       int
	since         time.Time
	reason        string
	width, height int
}

type hostReconnectTickMsg struct{ gen int }

type hostReconnectResultMsg struct {
	gen    int
	client *session.TUIClient
	state  *session.SessionState
	err    error
}

func (m *OS) beginHostReconnect(cause error) tea.Cmd {
	if m.AttachedHost == "" || m.QuitRequested {
		return nil
	}
	if m.hostReconnect != nil {
		return nil
	}
	if m.hostOpen != nil {
		m.hostReconnectDeferred = true
		return nil
	}
	m.hostReconnectGen++
	m.hostReconnect = &hostReconnect{
		gen:         m.hostReconnectGen,
		host:        m.AttachedHost,
		sessionName: m.SessionName,
		since:       time.Now(),
		width:       m.Width,
		height:      m.Height,
	}
	m.LogWarn("The link to %s dropped: %v. Connecting again.", m.hostReconnect.host, cause)
	m.ShowNotification(
		fmt.Sprintf("The link to %s dropped. tuios is connecting again.", m.hostReconnect.host),
		"warning", m.Settings.NotificationWarningDuration)
	m.MarkAllDirty()
	return m.hostReconnectWait()
}

func (m *OS) hostReconnectWait() tea.Cmd {
	r := m.hostReconnect
	if r == nil {
		return nil
	}
	gen := r.gen
	delay := hostReconnectDelay(r.attempt)
	return m.tickUntilStop(delay, func(time.Time) tea.Msg { return hostReconnectTickMsg{gen: gen} })
}

func hostReconnectDelay(attempt int) time.Duration {
	d := hostReconnectFirstDelay
	for range attempt {
		d *= 2
		if d >= hostReconnectMaxDelay {
			return hostReconnectMaxDelay
		}
	}
	return d
}

func (m *OS) handleHostReconnectTick(msg hostReconnectTickMsg) tea.Cmd {
	r := m.hostReconnect
	if r == nil || r.gen != msg.gen {
		return nil
	}
	if time.Since(r.since) > reconnectBudget() {
		return m.giveUpOnHost(r.reason)
	}
	r.attempt++
	gen := r.gen
	host, name := r.host, r.sessionName
	width, height := r.width, r.height
	version := m.clientVersionForReconnect()
	caps := m.clientCapabilities()
	reserve := m.ownLayoutReserveForReconnect()
	agent, kind := "", ""
	termType, colorTerm := "", ""
	served, viewOnly, upgradeDialog := false, false, false
	if m.DaemonClient != nil {
		agent = m.DaemonClient.SSHAuthSock
		termType, colorTerm = m.DaemonClient.Term, m.DaemonClient.ColorTerm
		kind, served, viewOnly = m.DaemonClient.Kind, m.DaemonClient.Served, m.DaemonClient.ViewOnly
		upgradeDialog = m.DaemonClient.UpgradeDialog
	}
	return func() tea.Msg {
		client := session.NewTUIClient()
		client.SetOwnLayoutReserve(reserve)
		client.Reconnect = true
		client.SSHAuthSock = agent
		client.Term, client.ColorTerm = termType, colorTerm
		client.Kind, client.Served, client.ViewOnly = kind, served, viewOnly
		client.UpgradeDialog = upgradeDialog
		if _, err := client.ConnectThroughHost(host, version, width, height, caps); err != nil {
			_ = client.Close()
			return hostReconnectResultMsg{gen: gen, err: err}
		}
		state, err := client.AttachSession(name, false, width, height)
		if err != nil {
			_ = client.Close()
			return hostReconnectResultMsg{gen: gen, err: err}
		}
		return hostReconnectResultMsg{gen: gen, client: client, state: state}
	}
}

func (m *OS) handleHostReconnectResult(msg hostReconnectResultMsg) tea.Cmd {
	r := m.hostReconnect
	if r == nil || r.gen != msg.gen {
		if msg.client != nil {
			_ = msg.client.Close()
		}
		return nil
	}
	if msg.err == nil && msg.client != nil {
		return m.resumeOnHost(msg.client, msg.state)
	}

	r.reason = hostReconnectReason(r.host, msg.err)
	m.LogWarn("Dial %d to %s failed: %v", r.attempt, r.host, msg.err)
	if hostReconnectFinal(msg.err) {
		return m.giveUpOnHost(r.reason)
	}
	if time.Since(r.since) > reconnectBudget() {
		return m.giveUpOnHost(r.reason)
	}
	return m.hostReconnectWait()
}

func (m *OS) resumeOnHost(client *session.TUIClient, state *session.SessionState) tea.Cmd {
	r := m.hostReconnect
	host := r.host
	took := time.Since(r.since).Round(time.Second)
	m.hostReconnect = nil
	m.cancelHostOpen()

	old := m.DaemonClient
	if old != nil {
		m.UnwireDaemonClient(old)
		_ = old.Close()
	}
	m.DaemonClient = client
	m.WireDaemonClient(client)
	client.StartReadLoop()
	m.AttachedHost = host
	m.SessionName = client.SessionName()
	m.tmlResync()

	if m.samePaneSet(state) {
		was := m.SubscribedPTYs
		m.SubscribedPTYs = make(map[string]bool)
		m.paneRequests = nil
		for _, w := range m.Windows {
			if w.DaemonMode && w.PTYID != "" && was[w.PTYID] {
				m.primePaneFromDaemon(w)
			}
		}
	} else {
		m.rebuildForSessionOn(state, m.Width, m.Height)
	}
	m.SyncDockContext()
	m.MarkAllDirty()
	m.LogInfo("The link to %s is back after %s", host, took)
	m.ShowNotification(fmt.Sprintf("The link to %s is back.", host), "success", m.Settings.NotificationDuration)
	return m.CockpitSideFetch()
}

func (m *OS) samePaneSet(state *session.SessionState) bool {
	if state == nil {
		return false
	}
	have := make([]string, 0, len(m.Windows))
	for _, w := range m.Windows {
		if !w.DaemonMode || w.PTYID == "" {
			return false
		}
		have = append(have, w.PTYID)
	}
	want := make([]string, 0, len(state.Windows))
	for _, ws := range state.Windows {
		if ws.PTYID == "" {
			return false
		}
		want = append(want, ws.PTYID)
	}
	if len(have) == 0 || len(have) != len(want) {
		return false
	}
	sort.Strings(have)
	sort.Strings(want)
	for i := range have {
		if have[i] != want[i] {
			return false
		}
	}
	return true
}

func (m *OS) giveUpOnHost(reason string) tea.Cmd {
	r := m.hostReconnect
	m.hostReconnect = nil
	if r == nil {
		return nil
	}
	m.LogWarn("Giving up on %s after %d attempts: %s", r.host, r.attempt, reason)
	if m.hostReturn == "" {
		m.hostReconnectReason = reason
		m.ExitReason = ExitHostLost
		return m.QuitCmd()
	}
	if err := m.SwitchToHostSession(federation.LocalHostName, m.hostReturn, false); err != nil {
		m.LogWarn("Could not return to %q: %v", m.hostReturn, err)
		m.hostReconnectReason = reason
		m.ExitReason = ExitHostLost
		return m.QuitCmd()
	}
	m.ShowNotification(reason, "warning", m.Settings.NotificationWarningDuration*2)
	return nil
}

func hostReconnectFinal(err error) bool {
	if err == nil {
		return false
	}
	if session.AttachRefused(err) {
		return true
	}
	if shake, ok := errors.AsType[*session.HostHandshakeError](err); ok {
		var mismatch *session.ProtocolMismatchError
		return errors.As(shake.Err, &mismatch)
	}
	if connErr, ok := errors.AsType[*session.HostConnectError](err); ok {
		switch connErr.Code {
		case session.ErrVerbUnknownHost, session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			return true
		}
	}
	return false
}

func hostReconnectReason(host string, err error) string {
	what := fmt.Sprintf("The link to %s did not come back.", host)
	if session.AttachRefused(err) {
		return fmt.Sprintf("The session is gone on %s. Open another one there.", host)
	}
	if shake, ok := errors.AsType[*session.HostHandshakeError](err); ok {
		if _, ok := errors.AsType[*session.ProtocolMismatchError](shake.Err); ok {
			return fmt.Sprintf("The tuios on %s cannot serve this client. Upgrade tuios on one machine.", host)
		}
	}
	if connErr, ok := errors.AsType[*session.HostConnectError](err); ok {
		switch connErr.Code {
		case session.ErrVerbUnknownHost:
			return fmt.Sprintf("The host %s is not configured. Add it with 'tuios hosts add'.", host)
		case session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			return "The daemon on this machine is too old. Restart it with 'tuios restart-server'."
		}
	}
	return what + fmt.Sprintf(" The session keeps running there. Run 'tuios hosts' to see %s.", host)
}

func (m *OS) hostLinkNote() string {
	r := m.hostReconnect
	if r == nil {
		if m.daemonReconnect != nil {
			return " " + daemonWaitNote + " "
		}
		return m.hostOpenNote()
	}
	name := r.host
	const room = 12
	if len(name) > room {
		name = name[:room-1] + "…"
	}
	return " Reconnecting to " + name + " "
}

// HostReconnectDeferred reports whether a lost link to the attached host
// waits for a rail click to land or fail before it is dialed again. The
// panes stay on screen meanwhile.
func (m *OS) HostReconnectDeferred() bool { return m.hostReconnectDeferred }

// ReconnectingToHost reports whether the client is trying to get a lost link
// back. It is what a test reads, and what tells a caller that the frame on
// screen is the last one the far session sent rather than a live one.
func (m *OS) ReconnectingToHost() bool { return m.hostReconnect != nil }

func (m *OS) clientVersionForReconnect() string {
	if m.DaemonClient == nil {
		return ""
	}
	return m.DaemonClient.ClientVersion()
}

func (m *OS) ownLayoutReserveForReconnect() session.LayoutReserve {
	if m.DaemonClient == nil {
		return session.LayoutReserve{}
	}
	return m.DaemonClient.OwnLayoutReserve()
}
