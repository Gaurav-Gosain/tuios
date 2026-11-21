package app

import (
	tea "charm.land/bubbletea/v2"
)

type hostFocusState uint8

const (
	hostFocusUnknown hostFocusState = iota
	hostFocusIn
	hostFocusOut
)

// HostLooking reports whether the person may be looking at this client: true
// unless the host terminal reported losing focus. It is the question an alert
// asks before it holds back for a pane the person is already looking at.
func (m *OS) HostLooking() bool {
	return m.hostFocus != hostFocusOut
}

func (m *OS) noteHostFocus(focused bool) tea.Cmd {
	next := hostFocusOut
	if focused {
		next = hostFocusIn
	}
	if m.hostFocus == next {
		return nil
	}
	was := m.HostLooking()
	m.hostFocus = next
	if w := m.GetFocusedWindow(); w != nil && !w.DaemonMode && was != m.HostLooking() {
		m.reportPaneFocus(w, focused)
	}
	client := m.DaemonClient
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		_ = client.ReportHostFocus(focused)
		return nil
	}
}

func (m *OS) notePaneFocus() {
	looking := m.HostLooking()
	for i, w := range m.Windows {
		if w != nil && !w.DaemonMode {
			w.SetHasFocus(looking && i == m.FocusedWindow)
		}
	}
}
