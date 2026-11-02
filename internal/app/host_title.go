package app

import "strings"

// hostTitleMax bounds the host title, in runes, as herdr's
// client.window_title.set bounds a title a tool sets.
const hostTitleMax = 256

// hostTitle is the title appearance.host_title gives the terminal this client
// runs in: the template expanded for the focused pane, or the session's name
// (tuios, with no session) when no pane on the workspace in view has the
// focus, when the template asks for {title} and the pane has none (a fresh
// shell), or when the template comes out empty. Empty when the option is off.
//
// Pane titles and names come from programs, so the result goes through the
// same laundering as a notification's text before it reaches the host inside
// an OSC 2.
func (m *OS) hostTitle() string {
	tmpl := m.Settings.HostTitle
	if tmpl == "" {
		return ""
	}
	// The name the rail shows the session by, display name and all. The
	// standalone TUI has no session, and there the program's name stands in,
	// so a template is never left with a separator hanging off it and the
	// title is never cleared for want of a name.
	session := m.sessionTitle(m.SessionName)
	if session == "" {
		session = "tuios"
	}
	title := ""
	if w := m.GetFocusedWindow(); w != nil {
		name := paneName(w, &m.Settings)
		if name != "" || !strings.Contains(tmpl, "{title}") {
			// The workspace a scratch group is shown over, as the dock names it.
			workspace := m.WorkspaceLabel(m.dockWorkspace())
			// paneDir never reads a pane's pid on this machine when the pane's
			// shell is on another, where it would name some local process.
			cwd := ""
			if strings.Contains(tmpl, "{cwd}") {
				cwd = m.paneDir(w)
			}
			title = notifyPlainText(m.Settings.FormatHostTitle(
				name, m.workspacePosition(w), cwd, session, workspace))
		}
	}
	if title == "" {
		title = notifyPlainText(session)
	}
	return truncateRunes(title, hostTitleMax)
}
