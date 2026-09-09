package app

import (
	"maps"
	"slices"
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
	m.SessionRestored = state.Restored
	// A drag in flight owns the arrangement until the pointer comes up. Adopting
	// mid-drag would snap the pills back under the pointer on any push that
	// happened to land, and the push that matters is this client's own.
	if !m.dockWorkspaceDrag.Dragging {
		m.WorkspaceOrder = slices.Clone(state.WorkspaceOrder)
	}
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

// sessionPlace is where a session is, as the rail shows it: the directory label
// for its focused pane and the git branch there, both from the daemon's listing
// and both empty until the listing has said. The directory is offered only for
// a name tuios generated, since "session-3" says nothing and a name a person
// chose says more than a directory. The branch follows either.
func (m *OS) sessionPlace(name string) (dir, branch string) {
	if m.DaemonClient == nil {
		return "", ""
	}
	dir, branch = m.DaemonClient.SessionPlace(name)
	if !session.IsGeneratedSessionName(name) {
		dir = ""
	}
	return dir, branch
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
