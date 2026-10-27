package app

import (
	"errors"
	"os"
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
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
	// dial reaches that machine's daemon. It is kept from the move, so a
	// waiting move still goes there after the client moved to another
	// machine.
	dial agentMailDial
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
var legacySessionOrder = sync.OnceValue(func() bool {
	return os.Getenv("TUIOS_SESSION_ORDER_LEGACY") == "1"
})

// commitSessionOrder shows order on the rail and hands it to the daemon that
// holds the attached session, now or after the move that is out.
func (m *OS) commitSessionOrder(order []string) {
	host := m.attachedMachine()
	m.setSidebarSessionOrder(host, order)
	m.saveSidebarState()
	if m.DaemonClient == nil || m.sessionOrderIsLocal(host) || legacySessionOrder() {
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
	push.dial = m.verbDialer()
	m.queueSidebarCmd(sessionOrderCmd(push.dial, m.DaemonClient, host, order))
}

// sessionOrderIsLocal reports whether host's daemon turned the order down,
// and still runs the build that did. A daemon upgraded since gets the order
// again.
func (m *OS) sessionOrderIsLocal(host string) bool {
	build, ok := m.sessionOrderLocal[host]
	if !ok {
		return false
	}
	if host == m.attachedMachine() && m.DaemonClient != nil && m.DaemonClient.DaemonVersion() != build {
		delete(m.sessionOrderLocal, host)
		return false
	}
	return true
}

// storedSessionOrder is the order this client keeps for host's sessions,
// whether or not the rail draws it now.
func (m *OS) storedSessionOrder(host string) []string {
	if host == federation.LocalHostName {
		return m.SidebarOrder
	}
	return m.SidebarHostSessionOrder[host]
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
		if _, err := c.Call("set-session-order", map[string]any{"order": order, "skip_missing": true}); err != nil {
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
		if errors.As(msg.Err, &call) && (call.Code == session.ErrVerbUnknownVerb || call.Code == session.ErrVerbForbidden) {
			// An older daemon, or a link that does not allow the verb: the
			// order stays this client's, as it was before the daemon kept one.
			if m.sessionOrderLocal == nil {
				m.sessionOrderLocal = map[string]string{}
			}
			build := ""
			if m.DaemonClient != nil {
				build = m.DaemonClient.DaemonVersion()
			}
			m.sessionOrderLocal[host] = build
			return nil
		}
		m.LogWarn("Session order: %v", msg.Err)
		m.ShowNotification("The session order did not reach the daemon.", "error", m.Settings.NotificationDuration*2)
	}
	if next != nil && push.dial != nil && m.DaemonClient != nil {
		push.sending = true
		return sessionOrderCmd(push.dial, m.DaemonClient, host, next)
	}
	// The daemon's order is in the listing now. The copy kept here goes,
	// unless a later move replaced it while this one was in flight.
	if msg.Err == nil && slices.Equal(m.storedSessionOrder(host), msg.Order) {
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
	return m.DaemonClient != nil && !m.sessionOrderIsLocal(host) && !legacySessionOrder() && m.DaemonClient.SessionOrderSet()
}
