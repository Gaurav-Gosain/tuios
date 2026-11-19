package app

import "strings"

// The words of the Simple profile. One word per concept: a workspace is a
// "tab", which is what a person who knows a browser calls it. The Keyboard
// profile keeps "workspace".

// workspaceWord is the word for a workspace, in lower case.
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
