package app

import "strings"

const hostTitleMax = 256

func (m *OS) hostTitle() string {
	tmpl := m.Settings.HostTitle
	if tmpl == "" {
		return ""
	}
	session := m.sessionTitle(m.SessionName)
	if session == "" {
		session = "tuios"
	}
	title := ""
	if w := m.GetFocusedWindow(); w != nil {
		name := paneName(w, &m.Settings)
		if name != "" || !strings.Contains(tmpl, "{title}") {
			workspace := m.WorkspaceLabel(m.dockWorkspace())
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
