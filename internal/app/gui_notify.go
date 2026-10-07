package app

import "time"

// DesktopAlerts reports whether the person's alert policy asks for a desktop
// notification now: alerts on, desktop notifications on, and outside the
// quiet hours. The GUI bridge sends it with each alert, and the native
// renderer shows the notification itself.
func (m *OS) DesktopAlerts() bool {
	p := m.agentAlertPolicy()
	return p.Enabled && p.Notify && !p.Quiet(time.Now())
}

// NoteInboxReplied records that this client answers the held approval
// requestID, before the answer goes out, so the close event that follows is
// not announced as answered from another client. The GUI bridge calls it
// for the renderer's reply-approval.
func (m *OS) NoteInboxReplied(requestID string) {
	if requestID == "" {
		return
	}
	if m.Inbox.replied == nil {
		m.Inbox.replied = make(map[string]bool)
	}
	m.Inbox.replied[requestID] = true
}
