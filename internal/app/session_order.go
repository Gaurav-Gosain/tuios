package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type sessionOrderPush struct {
	sending bool
	next    []string
	dial    agentMailDial
}

// SessionOrderAppliedMsg reports how a session order sent to the daemon
// landed. Order is what was sent, and Host the machine it was sent to.
type SessionOrderAppliedMsg struct {
	Host  string
	Order []string
	Err   error
}

func (m *OS) commitSessionOrder(order []string) {
	host := m.attachedMachine()
	m.setSidebarSessionOrder(host, order)
	m.saveSidebarState()
	if m.DaemonClient == nil || m.sessionOrderIsLocal(host) {
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

func (m *OS) storedSessionOrder(host string) []string {
	if host == federation.LocalHostName {
		return m.SidebarOrder
	}
	return m.SidebarHostSessionOrder[host]
}

func (m *OS) sessionOrderPending(host string) bool {
	push := m.sessionOrderPush[host]
	return push != nil && push.sending
}

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

func (m *OS) applySessionOrderResult(msg SessionOrderAppliedMsg) tea.Cmd {
	host := msg.Host
	push := m.sessionOrderPush[host]
	if push == nil {
		push = &sessionOrderPush{}
	}
	next := push.next
	push.sending, push.next = false, nil
	if msg.Err != nil {
		if code := session.VerbErrCode(msg.Err); code == session.ErrVerbUnknownVerb || code == session.ErrVerbForbidden {
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
	if msg.Err == nil && slices.Equal(m.storedSessionOrder(host), msg.Order) {
		m.setSidebarSessionOrder(host, nil)
		m.saveSidebarState()
	}
	m.refreshGotoRows()
	return nil
}

func (m *OS) sessionOrderFromDaemon(host string) bool {
	return m.DaemonClient != nil && !m.sessionOrderIsLocal(host) && m.DaemonClient.SessionOrderSet()
}
