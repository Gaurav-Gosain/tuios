package app

import (
	tea "charm.land/bubbletea/v2"
)

// Host terminal focus.
//
// The view asks the host terminal for focus events (DECSET 1004), and a
// terminal that supports them reports each time its window gains or loses
// focus. The client keeps the last report and passes every change to the
// daemon, which keeps one per attached client (session.sessionHostFocus).
//
// Until the terminal says anything the focus is unknown, and unknown is read as
// looking. A terminal without focus events therefore behaves exactly as tuios
// did before the signal existed: every rule that asks "is the person looking"
// answers yes, as it always did.

// hostFocusState is the host terminal's focus as its last event reported it.
type hostFocusState uint8

const (
	hostFocusUnknown hostFocusState = iota
	hostFocusIn
	hostFocusOut
)

// HostFocused reports whether the host terminal has focus, and whether it has
// ever said. known is false for a terminal that sends no focus events.
func (m *OS) HostFocused() (focused, known bool) {
	return m.hostFocus == hostFocusIn, m.hostFocus != hostFocusUnknown
}

// HostLooking reports whether the person may be looking at this client: true
// unless the host terminal reported losing focus. It is the question an alert
// asks before it holds back for a pane the person is already looking at.
func (m *OS) HostLooking() bool {
	return m.hostFocus != hostFocusOut
}

// noteHostFocus records a focus event and, in a daemon session, returns the
// command that reports it to the daemon. A repeat of the same focus sends
// nothing.
func (m *OS) noteHostFocus(focused bool) tea.Cmd {
	next := hostFocusOut
	if focused {
		next = hostFocusIn
	}
	if m.hostFocus == next {
		return nil
	}
	m.hostFocus = next
	client := m.DaemonClient
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		_ = client.ReportHostFocus(focused)
		return nil
	}
}
