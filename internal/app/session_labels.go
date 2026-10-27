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
	m.SessionRestored = state.Restored
	m.SessionGlobal = state.Global
	m.SessionSpace = state.Space
	m.adoptNotes(state)
	// The worktree record travels with the state, so the attached row is drawn
	// from the same push every other label on it comes from. A copy, because
	// the state snapshot is the daemon's and this outlives the call.
	m.SessionWorktree = nil
	if state.Worktree != nil {
		wt := *state.Worktree
		m.SessionWorktree = &wt
	}
	// The tab keys are what the next state's renumber is read against. See
	// adoptWorkspaceRenumbering.
	m.workspaceTabKeys = maps.Clone(state.WorkspaceTabKeys)
	m.workspaceTabKeysKnown = true
	if len(state.WorkspaceNames) == 0 {
		m.WorkspaceNames = nil
		return
	}
	m.WorkspaceNames = maps.Clone(state.WorkspaceNames)
}

// adoptNotes copies the return notes off a state push, and shows the
// session's note in the dock when this client has just come to the session:
// an attach or a switch. A note that changes while the client stays is not
// shown again: the person who changed it has just read it.
func (m *OS) adoptNotes(state *session.SessionState) {
	m.SessionNote = state.Note
	clear(m.paneNotes)
	for i := range state.Windows {
		if n := state.Windows[i].Note; n != "" {
			if m.paneNotes == nil {
				m.paneNotes = make(map[string]string)
			}
			m.paneNotes[state.Windows[i].ID] = n
		}
	}
	name := state.Name
	if name == "" {
		name = m.SessionName
	}
	if name == "" || name == m.noteAnnouncedFor {
		return
	}
	m.noteAnnouncedFor = name
	if state.Note != "" {
		m.ShowNotification("Note: "+printableTitle(state.Note), "info", 2*m.Settings.NotificationDuration)
	}
}

// sessionNote is the return note of the named session: the attached one from
// its state, any other from the cached listing.
func (m *OS) sessionNote(name string) string {
	if name == m.SessionName {
		return m.SessionNote
	}
	if m.DaemonClient != nil {
		return m.DaemonClient.SessionNote(name)
	}
	return ""
}

// paneNote is the return note of one pane, else the note of its session.
func (m *OS) paneNote(sessionName, windowID string) string {
	if sessionName == m.SessionName {
		if n := m.paneNotes[windowID]; n != "" {
			return n
		}
	} else if m.DaemonClient != nil {
		if n := m.DaemonClient.SessionWindowNote(sessionName, windowID); n != "" {
			return n
		}
	}
	return m.sessionNote(sessionName)
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

// sessionTitle is the one name a session is shown by in every view: the rail's
// title for it (sessiontree.BuildSession), which is its display name, else the
// directory of its focused pane when tuios made the name up, else the name.
// The rail said "demo" while the Inbox, the peek, the dock's alerts, the
// palette and the close dialog said "session-0" for the same session.
//
// It is for showing only. Never use it as a key: the daemon addresses a
// session by its name.
func (m *OS) sessionTitle(name string) string {
	if display := m.SessionLabel(name); display != name && display != "" {
		return display
	}
	if dir, _ := m.sessionPlace(name); dir != "" {
		return dir
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
