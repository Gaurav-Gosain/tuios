package app

import (
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The Inbox's selector filter: / opens a line where a selector is typed, the
// same syntax list-agents --select and every other selector reader takes
// (session/selector.go), and enter narrows the Inbox to the items it matches.
// It sits beside the kind filter on f, and both apply. The selector stays
// while the Inbox is closed and opened again, and the title says what it is,
// so a narrowed Inbox never passes for an empty one.

// InboxSelecting reports whether the selector line is open. While it is,
// every printable key is text.
func (m *OS) InboxSelecting() bool {
	return m.Inbox.selectEditing
}

// InboxStartSelect opens the selector line with the selector in force.
func (m *OS) InboxStartSelect() {
	st := &m.Inbox
	st.selectEditing = true
	st.selectDraft = st.Select
	st.selectErr = ""
}

// InboxSelectType adds typed text to the selector line.
func (m *OS) InboxSelectType(text string) {
	if text == "" || strings.ContainsAny(text, "\r\n\x1b") {
		return
	}
	m.Inbox.selectDraft += text
	m.Inbox.selectErr = ""
}

// InboxSelectBackspace removes the last character of the selector line.
func (m *OS) InboxSelectBackspace() {
	st := &m.Inbox
	if r := []rune(st.selectDraft); len(r) > 0 {
		st.selectDraft = string(r[:len(r)-1])
	}
	st.selectErr = ""
}

// InboxSelectCancel closes the selector line and keeps the selector in force.
func (m *OS) InboxSelectCancel() {
	m.Inbox.selectEditing = false
	m.Inbox.selectErr = ""
}

// InboxSelectApply puts the typed selector in force, or clears it when the
// line is empty. A selector that does not parse keeps the line open with the
// reason under it.
func (m *OS) InboxSelectApply() {
	st := &m.Inbox
	text := strings.TrimSpace(st.selectDraft)
	if text == "" {
		st.Select, st.selector = "", nil
	} else {
		home, _ := os.UserHomeDir()
		sel, err := session.ParseSelector(text, home)
		if err != nil {
			st.selectErr = err.Error()
			return
		}
		st.Select, st.selector = sel.String(), sel
	}
	st.selectEditing = false
	st.selectErr = ""
	// The rows changed under the cursor, so nothing counts as on screen until
	// the next draw, the rule OpenInbox follows for the answer keys.
	st.shown = inboxShown{}
	st.Selected, st.SelectedID = 0, ""
	st.Scroll = 0
	m.clampInboxSelection()
}

// inboxItemSelected reports whether an item passes the selector in force. An
// item of this machine answers group from its session's worktree record; cwd
// is not known here, so a cwd term matches no item in the Inbox (list-attention
// --select answers it for this machine's items).
func (m *OS) inboxItemSelected(it session.AttentionItem) bool {
	sel := m.Inbox.selector
	if sel == nil {
		return true
	}
	t := session.AttentionSelectorTarget(it)
	if it.Host == "" {
		var info *session.WorktreeInfo
		switch {
		case it.Session == m.SessionName:
			info = m.SessionWorktree
		case m.DaemonClient != nil:
			info = m.DaemonClient.SessionWorktree(it.Session)
		}
		if info != nil {
			t.Group = info.Group
		}
	}
	return sel.Match(t)
}
