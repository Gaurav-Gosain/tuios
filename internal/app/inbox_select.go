package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
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
	text := st.Select
	if st.agents.on && st.agents.query != "" {
		text = strings.TrimSpace(st.Select + " " + st.agents.query)
	}
	st.selectEd = composer.New(composer.Options{SingleLine: true, MaxRows: 1, MaxBytes: 1024})
	st.selectEd.SetText(text)
	st.selectErr = ""
}

// selectDraft is what the selector line holds.
func (st *InboxState) selectDraft() string {
	if st.selectEd == nil {
		return ""
	}
	return st.selectEd.Text()
}

// InboxSelectType types text into the selector line at the cursor.
func (m *OS) InboxSelectType(text string) {
	if ed := m.Inbox.selectEd; ed != nil && text != "" {
		ed.Paste(text, false)
		m.Inbox.selectErr = ""
	}
}

// InboxSelectKey gives a key to the selector line: enter applies it, esc
// closes it, and every other key edits it.
func (m *OS) InboxSelectKey(msg tea.KeyPressMsg) {
	ed := m.Inbox.selectEd
	if ed == nil {
		return
	}
	switch ed.Update(msg, false) {
	case composer.EventSend:
		m.InboxSelectApply()
	case composer.EventClose:
		m.InboxSelectCancel()
	default:
		m.Inbox.selectErr = ""
	}
}

// InboxSelectCancel closes the selector line and keeps the selector in force.
func (m *OS) InboxSelectCancel() {
	m.Inbox.selectEditing = false
	m.Inbox.selectEd = nil
	m.Inbox.selectErr = ""
}

// selectLine draws the selector line at width cells.
func (m *OS) selectLine(width int) string {
	pal := theme.UI()
	bg := pal.Surface
	label := overlay.Style(bg).Foreground(overlay.Readable(pal.FgDim, bg)).Render("select ")
	lines := m.Inbox.selectEd.Render(max(width-7, 8), composerStyles(pal), bg)
	if len(lines) == 0 {
		return label
	}
	return label + lines[0]
}

// InboxSelectApply puts the typed selector in force, or clears it when the
// line is empty. A selector that does not parse keeps the line open with the
// reason under it.
func (m *OS) InboxSelectApply() {
	if m.Inbox.agents.on {
		m.inboxAgentsSelectApply()
		return
	}
	st := &m.Inbox
	text := strings.TrimSpace(st.selectDraft())
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
	st.selectEd = nil
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
