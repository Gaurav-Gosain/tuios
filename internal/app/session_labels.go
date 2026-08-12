package app

import (
	"maps"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// adoptSessionLabels copies the daemon-owned labels off a state push. They are
// daemon-exclusive: the client never sends them back (BuildSessionState omits
// them and the merge treats an empty incoming value as "not sent"), so this is
// a read-only adoption and a client can never clear a label by syncing.
func (m *OS) adoptSessionLabels(state *session.SessionState) {
	m.SessionDisplayName = state.DisplayName
	m.SessionAccent = state.Accent
	if len(state.WorkspaceNames) == 0 {
		m.WorkspaceNames = nil
		return
	}
	m.WorkspaceNames = maps.Clone(state.WorkspaceNames)
}

// SessionLabel is what to show for a session: its display name when it has one,
// otherwise the identity name. Never use it as a key.
func (m *OS) SessionLabel(name string) string {
	if name == m.SessionName && m.SessionDisplayName != "" {
		return m.SessionDisplayName
	}
	if m.DaemonClient != nil {
		if display, _ := m.DaemonClient.SessionLabel(name); display != "" {
			return display
		}
	}
	return name
}

// WorkspaceLabel is what to show for a workspace of the attached session. An
// unnamed workspace reads back as its number, which is both its identity and
// the label it has always shown.
func (m *OS) WorkspaceLabel(ws int) string {
	if name := m.WorkspaceNames[ws]; name != "" {
		return name
	}
	return strconv.Itoa(ws)
}
