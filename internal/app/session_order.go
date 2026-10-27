package app

import (
	"errors"
	"os"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The session order is the daemon's (set-session-order), not this client's.
// A move on the rail, by key or by drag, shows at once, as it always did, and
// goes to the daemon, which ranks the sessions, sorts its listing by the
// ranks and pushes the listing to every client. Once the daemon's listing
// carries an order, the order this client keeps in its sidebar state file
// is not applied any more: it would put the sessions back where this client
// last saw them, over a move made on another client.
//
// A daemon older than the verb keeps the client's own order, as before. The
// first move on a newer one sends the whole order the rail shows, the kept
// one included, so nobody's arrangement is lost to the upgrade.
//
// Until the daemon answers a move, the rail draws the order the move made,
// and a move made in that time is built on it. Without that, three quick
// presses of shift+down kept only the first, because each was built on the
// listing the daemon had not updated yet, and a drag drew the old order until
// the answer came. The moves go to the daemon one at a time: a move made
// while one is out waits, and only the newest waiting one is sent, since it
// already holds the ones before it.

// sessionOrderPush is one machine's orders on their way to its daemon.
type sessionOrderPush struct {
	// sending is set while an order is out and not answered.
	sending bool
	// next is the newest order made while one was out, nil for none.
	next []string
}

// SessionOrderAppliedMsg reports how a session order sent to the daemon
// landed. Order is what was sent, and Host the machine it was sent to.
type SessionOrderAppliedMsg struct {
	Host  string
	Order []string
	Err   error
}

// legacySessionOrder makes this client behave as a build from before the
// daemon's session order: it keeps its own order, applies it over the
// listing, and never sends one. It is TUIOS_SESSION_ORDER_LEGACY=1, so the
// mixed-version case can be tested without an old build.
func legacySessionOrder() bool {
	return os.Getenv("TUIOS_SESSION_ORDER_LEGACY") == "1"
}

// commitSessionOrder shows order on the rail and hands it to the daemon that
// holds the attached session, now or after the move that is out.
func (m *OS) commitSessionOrder(order []string) {
	host := m.attachedMachine()
	m.setSidebarSessionOrder(host, order)
	m.saveSidebarState()
	if m.DaemonClient == nil || m.sessionOrderLocal[host] || legacySessionOrder() {
		return
	}
	if m.sessionOrderPush == nil {
		m.sessionOrderPush = map[string]*sessionOrderPush{}
	}
	push := m.sessionOrderPush[host]
	if push == nil {
		push = &sessionOrderPush{}
		m.sessionOrderPush[host] = push
	}
	if push.sending {
		push.next = order
		return
	}
	push.sending = true
	m.queueSidebarCmd(sessionOrderCmd(m.verbDialer(), m.DaemonClient, host, order))
}

// sessionOrderPending reports whether a move on host's rail is on its way to
// the daemon, in which case the rail draws the order it made.
func (m *OS) sessionOrderPending(host string) bool {
	push := m.sessionOrderPush[host]
	return push != nil && push.sending
}

// sessionOrderCmd calls set-session-order off the Update goroutine, as
// labelVerbCmd does, then refreshes the listing, so the rail can drop its own
// copy of the order with the daemon's already in the cache.
func sessionOrderCmd(dial agentMailDial, client *session.TUIClient, host string, order []string) tea.Cmd {
	return func() tea.Msg {
		c, err := dial()
		if err != nil {
			return SessionOrderAppliedMsg{Host: host, Order: order, Err: err}
		}
		defer func() { _ = c.Close() }()
		if _, err := c.Call("set-session-order", map[string]any{"order": order}); err != nil {
			return SessionOrderAppliedMsg{Host: host, Order: order, Err: err}
		}
		_, _ = client.RefreshSessionList()
		return SessionOrderAppliedMsg{Host: host, Order: order}
	}
}

// applySessionOrderResult finishes a session order the daemon was sent, and
// sends the move that waited behind it, if one did.
func (m *OS) applySessionOrderResult(msg SessionOrderAppliedMsg) tea.Cmd {
	host := msg.Host
	push := m.sessionOrderPush[host]
	if push == nil {
		push = &sessionOrderPush{}
	}
	next := push.next
	push.sending, push.next = false, nil
	if msg.Err != nil {
		var call *session.VerbCallError
		if errors.As(msg.Err, &call) && call.Code == session.ErrVerbUnknownVerb {
			// An older daemon: the order stays this client's, as it was.
			if m.sessionOrderLocal == nil {
				m.sessionOrderLocal = map[string]bool{}
			}
			m.sessionOrderLocal[host] = true
			return nil
		}
		m.ShowNotification("Session order failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
	}
	if next != nil && host == m.attachedMachine() && m.DaemonClient != nil {
		push.sending = true
		return sessionOrderCmd(m.verbDialer(), m.DaemonClient, host, next)
	}
	// The daemon's order is in the listing now. The copy kept here goes,
	// unless a later move replaced it while this one was in flight.
	if msg.Err == nil && slices.Equal(m.sidebarSessionOrderFor(host), msg.Order) {
		m.setSidebarSessionOrder(host, nil)
		m.saveSidebarState()
	}
	m.refreshSwitcherItems()
	return nil
}

// sessionOrderFromDaemon reports whether the daemon that holds host's
// sessions keeps an order its listing carries, in which case it is the order
// the rail shows once no move of this client's is on its way to it.
func (m *OS) sessionOrderFromDaemon(host string) bool {
	return m.DaemonClient != nil && !m.sessionOrderLocal[host] && !legacySessionOrder() && m.DaemonClient.SessionOrderSet()
}
