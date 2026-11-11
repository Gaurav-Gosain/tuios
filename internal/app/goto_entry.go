package app

import "strings"

// The keys and entries that opened the session switcher, the workspace
// switcher and the window list open the go-to picker on the filter that lists
// the same places.

// OpenSessionSwitcher opens go-to on the sessions. Shared by the keybinding,
// the palette entry and the quit menu.
func (m *OS) OpenSessionSwitcher() {
	if m.learnOff(learnNoteSessions) {
		return
	}
	// "#" lists the sessions, and enter on a name nothing has makes that
	// session.
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

// gotoThisSession is the go-to term that keeps the rows of the session the
// client shows, or "" when its name cannot be written as one.
func (m *OS) gotoThisSession() string {
	name := m.sidebarCurrentSessionID()
	if name == "" || strings.ContainsAny(name, " \t:*?[]{},\x00") {
		return ""
	}
	return "session:" + name + " "
}

// gotoThisSessionWorkspaces lists the workspaces of the session the client
// shows that are worth going to: the ones holding a pane, the current one,
// and any the person has named. They are in the dock's order, so go-to does
// not show a second arrangement of the same workspaces.
func (m *OS) gotoThisSessionWorkspaces() []int {
	worth := make([]int, 0, m.NumWorkspaces)
	for n := 1; n <= m.NumWorkspaces; n++ {
		if m.GetWorkspaceWindowCount(n) > 0 || m.WorkspaceNames[n] != "" || n == m.dockWorkspace() {
			worth = append(worth, n)
		}
	}
	return m.workspaceDisplayOrder(worth)
}
