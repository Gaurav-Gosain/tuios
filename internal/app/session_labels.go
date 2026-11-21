package app

import (
	"maps"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func (m *OS) adoptSessionLabels(state *session.SessionState) {
	m.SessionDisplayName = state.DisplayName
	m.SessionAccent = state.Accent
	m.SessionRestored = state.Restored
	m.SessionGlobal = state.Global
	m.SessionSpace = state.Space
	m.adoptNotes(state)
	m.SessionWorktree = nil
	if state.Worktree != nil {
		wt := *state.Worktree
		m.SessionWorktree = &wt
	}
	m.workspaceTabKeys = maps.Clone(state.WorkspaceTabKeys)
	m.workspaceTabKeysKnown = true
	if !maps.Equal(m.paneIconNames, state.IconNames) {
		m.paneIconNames = maps.Clone(state.IconNames)
	}
	if len(state.WorkspaceNames) == 0 {
		m.WorkspaceNames = nil
		return
	}
	m.WorkspaceNames = maps.Clone(state.WorkspaceNames)
}

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

func (m *OS) sessionNote(name string) string {
	if name == m.SessionName {
		return m.SessionNote
	}
	if m.DaemonClient != nil {
		return m.DaemonClient.SessionNote(name)
	}
	return ""
}

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

func (m *OS) sessionTitle(name string) string {
	if display := m.SessionLabel(name); display != name && display != "" {
		return display
	}
	if t := m.sessionAgentTitle(name); t != "" {
		return t
	}
	if dir, _ := m.sessionPlace(name); dir != "" {
		return dir
	}
	return name
}

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

func (m *OS) sessionAgentTitle(name string) string {
	if !session.IsGeneratedSessionName(name) {
		return ""
	}
	if name == m.SessionName {
		return m.attachedAgentTitle()
	}
	if m.DaemonClient != nil {
		return m.DaemonClient.SessionAgentTitle(name)
	}
	return ""
}

func (m *OS) attachedAgentTitle() string {
	for _, w := range m.Windows {
		if w == nil || w.AgentHarness == "" || hiddenFromLists(w) {
			continue
		}
		title, program := w.Title(), w.ForegroundCmd
		if w.CustomName != "" {
			title, program = w.CustomName, ""
		}
		return session.PaneAgentTitle(w.AgentHarness, program, title)
	}
	return ""
}

func (m *OS) workspaceDisplayNumber(ws int) int {
	if session.IsScratchWorkspace(ws) {
		return ws
	}
	return ws - m.Settings.WorkspaceIndexOffset
}

// WorkspaceLabel is what to show for a workspace of the attached session: its
// name, else the name its active pane's program gave with OSC 1, else its
// number, which is its identity and the label it has always shown. Only OSC
// 1 counts, not the title OSC 0 sets along with the icon name: shells and
// agents send OSC 0 with every prompt, so a label that took it would follow
// whatever ran last in every workspace.
func (m *OS) WorkspaceLabel(ws int) string {
	if name := m.workspaceGivenName(ws); name != "" {
		return name
	}
	return strconv.Itoa(m.workspaceDisplayNumber(ws))
}

func (m *OS) workspaceGivenName(ws int) string {
	if name := m.WorkspaceNames[ws]; name != "" {
		return name
	}
	if w := m.workspaceActivePane(ws); w != nil {
		return m.paneIconName(w)
	}
	return ""
}

func (m *OS) paneIconName(w *terminal.Window) string {
	if w.DaemonMode {
		return m.paneIconNames[w.ID]
	}
	return w.IconName()
}

func (m *OS) workspaceActivePane(ws int) *terminal.Window {
	speaks := func(i int) bool {
		if i < 0 || i >= len(m.Windows) {
			return false
		}
		w := m.Windows[i]
		return w != nil && w.Workspace == ws && !w.Minimized && !hiddenFromLists(w)
	}
	if ws == m.CurrentWorkspace && speaks(m.FocusedWindow) {
		return m.Windows[m.FocusedWindow]
	}
	if i, ok := m.WorkspaceFocus[ws]; ok && speaks(i) {
		return m.Windows[i]
	}
	for i := range m.Windows {
		if speaks(i) {
			return m.Windows[i]
		}
	}
	return nil
}
