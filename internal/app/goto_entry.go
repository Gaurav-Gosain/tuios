package app

import "strings"

// OpenSessionSwitcher opens go-to on the sessions. Shared by the keybinding,
// the palette entry and the quit menu.
func (m *OS) OpenSessionSwitcher() {
	if m.learnOff(learnNoteSessions) {
		return
	}
	m.OpenGotoPickerWith("# ")
}

// OpenWorkspaceSwitcher opens go-to on the workspaces of the session the
// client shows.
func (m *OS) OpenWorkspaceSwitcher() {
	m.OpenGotoPickerWith("! " + m.gotoThisSession())
}

// OpenAggregateView opens go-to on the panes of the session the client shows,
// which is where the panes the dock could not fit are listed.
func (m *OS) OpenAggregateView() {
	m.OpenGotoPickerWith("kind:pane " + m.gotoThisSession())
}

func (m *OS) gotoThisSession() string {
	name := m.sidebarCurrentSessionID()
	if name == "" || strings.ContainsAny(name, " \t:*?[]{},\x00") {
		return ""
	}
	return "session:" + name + " "
}

func (m *OS) gotoThisSessionWorkspaces() []int {
	worth := make([]int, 0, m.NumWorkspaces)
	for n := 1; n <= m.NumWorkspaces; n++ {
		if m.GetWorkspaceWindowCount(n) > 0 || m.WorkspaceNames[n] != "" || n == m.dockWorkspace() {
			worth = append(worth, n)
		}
	}
	return m.workspaceDisplayOrder(worth)
}
