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

// Getting a lost link back.
//
// The premise of a session on another machine is that the daemon there owns it.
// The link is a pipe to it and nothing more, so a pipe that broke is not a
// session that ended: the shells are still running, the panes still have their
// scrollback, and the only thing that went is this client's way of seeing them.
//
// What used to happen was that the client gave up on the first end of the
// connection, closed every pane it was drawing, and put the user back in the
// session on their own machine. The far session survived, which was the design,
// and the person lost their place, which was not. This file is the other half:
// the client keeps the last frame on screen, dials again with a backoff, and
// puts the person back where they were.
//
// Three rules shape it.
//
// A failure that cannot be fixed by trying again is not tried again. A host
// that is not configured, a daemon that speaks a protocol this build cannot
// read, a session that is no longer there: all three fail at once, with the
// reason, because retrying them for three minutes teaches the user nothing and
// hides what is wrong.
//
// Leaving on purpose is not the same event as losing a link. A detach and a
// session switch both take the old connection down deliberately, and both
// unwire it first, so its closing is never heard here.
//
// A machine that is genuinely down is given up on. The budget below is the
// whole of it.

const (
	// hostReconnectFirstDelay is the wait before the first redial. It is short
	// because the common loss is a momentary one - a keepalive that lapsed, a
	// far daemon restarted - and the link is usually back on the first try.
	hostReconnectFirstDelay = time.Second
	// hostReconnectMaxDelay caps the backoff. A machine that is really down
	// must not be dialed harder than this, and a machine that is coming back
	// must not be waited on much longer than this once it has.
	hostReconnectMaxDelay = 15 * time.Second
	// hostReconnectBudget is how long the client keeps trying before it gives
	// up and says so.
	//
	// It is measured against the two waits that sit under it, not chosen for
	// how it reads. This client dials the host through the daemon on its own
	// machine, and that daemon only answers once its own link is back: its
	// redial backoff runs to a minute, and ssh's keepalives take another
	// forty-five seconds to notice a dead path in the first place. A budget
	// under two minutes would give up while the machinery below it was still
	// working. Three minutes clears both with room, and covers what actually
	// takes a host away for a minute or two: a network changing under a
	// laptop, an sshd restarting, a cloud host rebooting.
	hostReconnectBudget = 3 * time.Minute
)

// reconnectBudget is how long this client keeps trying, which is
// hostReconnectBudget unless TUIOS_HOST_RECONNECT_BUDGET names another
// duration. The variable exists so a test can watch the client give up without
// waiting three minutes for it, and it is read once: a person who wants a
// longer budget on a link that is often down sets it before tuios starts.
var reconnectBudget = sync.OnceValue(func() time.Duration {
	if v := os.Getenv("TUIOS_HOST_RECONNECT_BUDGET"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return hostReconnectBudget
})

// hostReconnect is what the client is doing about a link that dropped. It is
// nil whenever the client is not trying to get one back.
type hostReconnect struct {
	// gen makes a result from an abandoned attempt ignorable. Every message
	// this file sends carries the generation it was started for, and one that
	// does not match the live state is dropped.
	gen int
	// host and sessionName are where to go back to. They are read once, when
	// the link drops, because the model's own AttachedHost is what a later
	// switch would change.
	host        string
	sessionName string
	// attempt counts the dials made so far, and since is when the link went.
	attempt int
	since   time.Time
	// reason is the last failure, in plain words, for the notice shown when
	// the budget runs out.
	reason string
	// width and height are the size the client had when the link went. The
	// screen does not change while it is away, so this is the size to attach
	// at.
	width, height int
}

// hostReconnectTickMsg says the backoff has elapsed and it is time to dial.
type hostReconnectTickMsg struct{ gen int }

// hostReconnectResultMsg carries one dial's outcome back to the Update loop.
// Exactly one of client and err is meaningful.
type hostReconnectResultMsg struct {
	gen    int
	client *session.TUIClient
	state  *session.SessionState
	err    error
}

// beginHostReconnect starts trying to get the link back. It reports whether it
// took the loss on: false means the caller should fall back as it did before.
func (m *OS) beginHostReconnect(cause error) tea.Cmd {
	if m.AttachedHost == "" || m.QuitRequested {
		return nil
	}
	if m.hostReconnect != nil {
		// Already trying. A second disconnect from the connection that just
		// died must not restart the clock or double the dials.
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
	// The panes are left exactly as they are. The last frame the far session
	// sent is the truest thing on screen until a new one arrives, and clearing
	// it would throw away the only picture of the session there is.
	m.MarkAllDirty()
	return m.hostReconnectWait()
}

// hostReconnectWait schedules the next dial after this attempt's backoff.
func (m *OS) hostReconnectWait() tea.Cmd {
	r := m.hostReconnect
	if r == nil {
		return nil
	}
	gen := r.gen
	delay := hostReconnectDelay(r.attempt)
	return tea.Tick(delay, func(time.Time) tea.Msg { return hostReconnectTickMsg{gen: gen} })
}

// hostReconnectDelay is the wait before dial number attempt+1: one second, then
// doubling to the cap. A machine that is off is dialed four times a minute at
// the cap, which is enough to catch it coming back and little enough to be
// invisible on it.
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

// handleHostReconnectTick runs one dial, off this goroutine.
func (m *OS) handleHostReconnectTick(msg hostReconnectTickMsg) tea.Cmd {
	r := m.hostReconnect
	if r == nil || r.gen != msg.gen {
		return nil
	}
	if time.Since(r.since) > reconnectBudget() {
		return m.giveUpOnHost(r.reason)
	}
	r.attempt++
	// Every input the dial needs is read here, on the Update goroutine, and
	// handed to the command by value. Nothing in the command touches the model.
	gen := r.gen
	host, name := r.host, r.sessionName
	width, height := r.width, r.height
	version := m.clientVersionForReconnect()
	caps := m.clientCapabilities()
	reserve := m.ownLayoutReserveForReconnect()
	return func() tea.Msg {
		client := session.NewTUIClient()
		client.SetOwnLayoutReserve(reserve)
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

// handleHostReconnectResult applies one dial's outcome.
func (m *OS) handleHostReconnectResult(msg hostReconnectResultMsg) tea.Cmd {
	r := m.hostReconnect
	if r == nil || r.gen != msg.gen {
		// The user moved on. Whatever this dial won belongs to nobody.
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

// resumeOnHost puts the client back on the session it lost.
//
// The panes are kept whenever the far session still has the same set of them,
// which is the usual case: the shells did not stop when the pipe did. Keeping
// them is what makes the return cheap and what makes it land in the right
// place. Each pane asks the daemon only for the rows its own emulator does not
// already hold, and each pane keeps the scroll anchor it was carrying, so a
// person who was reading something forty lines back is still reading it.
//
// A session whose panes changed while the link was down cannot be resumed that
// way, and is rebuilt from the state the attach returned instead.
func (m *OS) resumeOnHost(client *session.TUIClient, state *session.SessionState) tea.Cmd {
	r := m.hostReconnect
	host := r.host
	took := time.Since(r.since).Round(time.Second)
	m.hostReconnect = nil

	old := m.DaemonClient
	if old != nil {
		m.UnwireDaemonClient(old)
		// No detach is sent: the connection that carried one is gone, and the
		// daemon on the host has already seen this client leave.
		_ = old.Close()
	}
	m.DaemonClient = client
	// Wired before the read loop starts, so a push that arrives in the first
	// millisecond has somewhere to go and a connection that fails immediately
	// is reported rather than lost.
	m.WireDaemonClient(client)
	client.StartReadLoop()
	m.AttachedHost = host
	m.SessionName = client.SessionName()

	if m.samePaneSet(state) {
		// The daemon on the host dropped every subscription when this client's
		// connection died, so the client's record of them is cleared and each
		// pane subscribes again from where its own emulator got to.
		//
		// Only the panes that were subscribed are primed. A pane on another
		// workspace was not being streamed before the link went, and priming
		// it here would subscribe this client to more than it had, which is
		// not what coming back means.
		was := m.SubscribedPTYs
		m.SubscribedPTYs = make(map[string]bool)
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
	return nil
}

// samePaneSet reports whether the session still has exactly the panes this
// client is drawing.
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

// giveUpOnHost ends the attempt and falls back the way the client always did:
// to the session on this machine it left, or out with a reason when it started
// on the host.
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
		return tea.Quit
	}
	if err := m.SwitchToHostSession(federation.LocalHostName, m.hostReturn, false); err != nil {
		m.LogWarn("Could not return to %q: %v", m.hostReturn, err)
		m.hostReconnectReason = reason
		m.ExitReason = ExitHostLost
		return tea.Quit
	}
	m.ShowNotification(reason, "warning", m.Settings.NotificationWarningDuration*2)
	return nil
}

// hostReconnectFinal reports a failure that trying again cannot fix.
func hostReconnectFinal(err error) bool {
	if err == nil {
		return false
	}
	// The daemon on the host answered the attach and declined it. The session
	// is not there any more, so no number of attempts brings it back.
	if session.AttachRefused(err) {
		return true
	}
	var shake *session.HostHandshakeError
	if errors.As(err, &shake) {
		var mismatch *session.ProtocolMismatchError
		return errors.As(shake.Err, &mismatch)
	}
	var connErr *session.HostConnectError
	if errors.As(err, &connErr) {
		switch connErr.Code {
		case session.ErrVerbUnknownHost, session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			return true
		}
	}
	return false
}

// hostReconnectReason is the sentence a person reads when the client stops
// trying. It says what happened, where the session is, and what to do.
func hostReconnectReason(host string, err error) string {
	what := fmt.Sprintf("The link to %s did not come back.", host)
	if session.AttachRefused(err) {
		return fmt.Sprintf("The session is gone on %s. Open another one there.", host)
	}
	var shake *session.HostHandshakeError
	if errors.As(err, &shake) {
		var mismatch *session.ProtocolMismatchError
		if errors.As(shake.Err, &mismatch) {
			return fmt.Sprintf("The tuios on %s cannot serve this client. Upgrade tuios on one machine.", host)
		}
	}
	var connErr *session.HostConnectError
	if errors.As(err, &connErr) {
		switch connErr.Code {
		case session.ErrVerbUnknownHost:
			return fmt.Sprintf("The host %s is not configured. Add it with 'tuios hosts add'.", host)
		case session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			return "The daemon on this machine is too old. Restart it with 'tuios kill-server'."
		}
	}
	return what + fmt.Sprintf(" The session keeps running there. Run 'tuios hosts' to see %s.", host)
}

// hostLinkNote is the dock's one line about a link that is not there. It is
// empty whenever the link is fine, which is why the dock says nothing at all in
// the ordinary case.
func (m *OS) hostLinkNote() string {
	r := m.hostReconnect
	if r == nil {
		return ""
	}
	name := r.host
	const room = 12
	if len(name) > room {
		name = name[:room-1] + "…"
	}
	return " Reconnecting to " + name + " "
}

// ReconnectingToHost reports whether the client is trying to get a lost link
// back. It is what a test reads, and what tells a caller that the frame on
// screen is the last one the far session sent rather than a live one.
func (m *OS) ReconnectingToHost() bool { return m.hostReconnect != nil }

// clientVersionForReconnect is the version string to introduce this client
// with. It comes from the connection that is going away, which is the same
// build, so a dead connection still answers it.
func (m *OS) clientVersionForReconnect() string {
	if m.DaemonClient == nil {
		return ""
	}
	return m.DaemonClient.ClientVersion()
}

// ownLayoutReserveForReconnect is the chrome this client keeps for itself, as
// the daemon on the host was last told.
func (m *OS) ownLayoutReserveForReconnect() session.LayoutReserve {
	if m.DaemonClient == nil {
		return session.LayoutReserve{}
	}
	return m.DaemonClient.OwnLayoutReserve()
}
