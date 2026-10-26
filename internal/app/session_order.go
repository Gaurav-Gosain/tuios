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

// SessionOrderAppliedMsg reports how a session order sent to the daemon
// landed. Order is what was sent.
type SessionOrderAppliedMsg struct {
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
// holds the attached session.
func (m *OS) commitSessionOrder(order []string) {
	m.setSidebarSessionOrder(m.attachedMachine(), order)
	m.saveSidebarState()
	if m.DaemonClient == nil || m.sessionOrderLocal || legacySessionOrder() {
		return
	}
	m.queueSidebarCmd(sessionOrderCmd(m.verbDialer(), m.DaemonClient, order))
}

// sessionOrderCmd calls set-session-order off the Update goroutine, as
// labelVerbCmd does, then refreshes the listing, so the rail can drop its own
// copy of the order with the daemon's already in the cache.
func sessionOrderCmd(dial agentMailDial, client *session.TUIClient, order []string) tea.Cmd {
	return func() tea.Msg {
		c, err := dial()
		if err != nil {
			return SessionOrderAppliedMsg{Order: order, Err: err}
		}
		defer func() { _ = c.Close() }()
		if _, err := c.Call("set-session-order", map[string]any{"order": order}); err != nil {
			return SessionOrderAppliedMsg{Order: order, Err: err}
		}
		_, _ = client.RefreshSessionList()
		return SessionOrderAppliedMsg{Order: order}
	}
}

// applySessionOrderResult finishes a session order the daemon was sent.
func (m *OS) applySessionOrderResult(msg SessionOrderAppliedMsg) {
	host := m.attachedMachine()
	if msg.Err != nil {
		var call *session.VerbCallError
		if errors.As(msg.Err, &call) && call.Code == session.ErrVerbUnknownVerb {
			// An older daemon: the order stays this client's, as it was.
			m.sessionOrderLocal = true
			return
		}
		m.ShowNotification("Session order failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}
	// The daemon's order is in the listing now. The copy kept here goes,
	// unless a later move replaced it while this one was in flight.
	if slices.Equal(m.sidebarSessionOrderFor(host), msg.Order) {
		m.setSidebarSessionOrder(host, nil)
		m.saveSidebarState()
	}
	m.refreshSwitcherItems()
}

// sessionOrderFromDaemon reports whether the daemon's listing carries a
// session order, in which case it is the order the rail shows.
func (m *OS) sessionOrderFromDaemon() bool {
	return m.DaemonClient != nil && !m.sessionOrderLocal && !legacySessionOrder() && m.DaemonClient.SessionOrderSet()
}
