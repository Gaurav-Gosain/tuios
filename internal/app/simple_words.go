package app

import "strings"

func (m *OS) workspaceWord() string {
	if m.Settings.Simple {
		return "tab"
	}
	return "workspace"
}

// WorkspaceWordTitle is the word for a workspace at the start of a sentence.
func (m *OS) WorkspaceWordTitle() string {
	w := m.workspaceWord()
	return strings.ToUpper(w[:1]) + w[1:]
}
