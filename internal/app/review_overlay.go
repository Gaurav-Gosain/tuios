package app

import tea "charm.land/bubbletea/v2"

// Reviewing what an agent changed: the full-screen diff of a pane's worktree
// against its base, the notes the person leaves on it, and the comparison of
// a fan's attempts. It reads the review-diff and compare-fan verbs, runs no
// git until it is opened, and draws nothing until then.
//
// It is reached from ctrl+b v on the focused pane, v on an Inbox row and v on
// a rail agent row. The overlay itself has not landed; the entry points below
// do nothing yet, and say so by answering false where the input path asks.

// ReviewFocusedPane opens the review of the focused pane (ctrl+b v). handled
// is false while the overlay has not landed, and the prefix path then treats
// the key as unbound: in terminal mode it reaches the pane.
func (m *OS) ReviewFocusedPane() (tea.Cmd, bool) {
	return nil, false
}

// InboxReview opens the review of the selected Inbox item's pane.
func (m *OS) InboxReview() (tea.Cmd, bool) {
	return nil, false
}

// SidebarAgentReview opens the review of the pane of a rail agent row.
func (m *OS) SidebarAgentReview(session, window string) (tea.Cmd, bool) {
	return nil, false
}
