package composer

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// @ completion.
//
// Typing @ at the start of the draft or after a space opens a list under the
// editor: panes, agents and files whose names match what follows the @, up to
// the cursor. tab or enter puts the selected one in place of the @ and the
// query, as a click on it does. up and down (ctrl+p and ctrl+n) choose, and
// esc, a space, or deleting the @ closes the list. While the list is open it
// takes these keys before the editor does, so enter does not send.
//
// What is put in is text the agent reads: a pane's or an agent's address,
// such as work/api, or a file's path relative to the pane's folder. Nothing is
// copied. A folder puts in its path with a slash and keeps the list open, so
// the person walks into it.
//
// The composer does not know the panes, the agents or the files: the host's
// Completer does, and may read files on its own time. The composer asks it
// for the list on each frame the list is open, which costs nothing when the
// answer is the same as before, since the frame is kept.

// mentionRows is how many entries the list shows.
const mentionRows = 6

// MentionKind is what an entry names.
type MentionKind uint8

const (
	MentionPane MentionKind = iota
	MentionAgent
	MentionFile
	MentionFolder
)

// mentionBadges are the words in front of each kind.
var mentionBadges = [...]string{"pane", "agent", "file", "dir"}

// Mention is one entry of the list.
type Mention struct {
	Kind MentionKind
	// Label is what the list shows.
	Label string
	// Insert is the text that takes the place of the @ and the query.
	Insert string
	// Note is a word after the label, such as an agent's state.
	Note string
}

// Completer gives the entries for a query, best first.
type Completer interface {
	// Mentions are the entries that match query, the text after the @.
	Mentions(query string) []Mention
	// Version changes when Mentions would answer differently, such as when
	// a folder's listing arrives.
	Version() uint64
}

// mention is the open list.
type mention struct {
	// at is the index of the @ cell.
	at  int
	sel int
	// query is the text the selection was made in. A new query starts the
	// selection at the best entry again.
	query string
	// row is the frame line the list's first entry was drawn on, -1 when
	// the frame drew no entry, and top the index of that entry.
	row, top int
}

// SetCompleter gives the composer the host's completer. Nil turns @
// completion off.
func (m *Model) SetCompleter(c Completer) { m.completer = c }

// MentionQuery is the text after the @ while the list is open, and false
// when it is not, so the host can read what the list needs.
func (m *Model) MentionQuery() (string, bool) {
	if m.mention == nil {
		return "", false
	}
	return m.mentionQuery(), true
}

// mentionQuery is the text from after the @ to the cursor.
func (m *Model) mentionQuery() string {
	var b strings.Builder
	for _, c := range m.cells[m.mention.at+1 : m.cur] {
		b.WriteString(c.s)
	}
	return b.String()
}

// syncMention opens, keeps or closes the list after an edit or a move: it is
// open while the cursor is in a word that starts with an @ at the start of the
// draft or after a space.
func (m *Model) syncMention() {
	if m.completer == nil {
		m.mention = nil
		return
	}
	at := -1
	for i := m.cur - 1; i >= 0; i-- {
		c := m.cells[i]
		if c.block != nil || c.isSpace() {
			break
		}
		if c.s == "@" && (i == 0 || m.cells[i-1].isSpace()) {
			at = i
			break
		}
	}
	if at < 0 {
		m.mention, m.dismissedAt = nil, -1
		return
	}
	if at == m.dismissedAt {
		m.mention = nil
		return
	}
	if m.mention == nil || m.mention.at != at {
		m.mention = &mention{at: at, row: -1}
	}
	if q := m.mentionQuery(); q != m.mention.query {
		m.mention.query, m.mention.sel = q, 0
	}
}

// mentions is the list's entries now.
func (m *Model) mentions() []Mention {
	if m.mention == nil || m.completer == nil {
		return nil
	}
	return m.completer.Mentions(m.mentionQuery())
}

// mentionKey handles a key while the list is open. It reports whether the key
// was the list's.
func (m *Model) mentionKey(key string) bool {
	items := m.mentions()
	switch key {
	case "esc":
		m.dismissedAt = m.mention.at
		m.mention = nil
		return true
	case "up", "ctrl+p":
		if len(items) > 0 {
			m.mention.sel = (m.mention.sel - 1 + len(items)) % len(items)
		}
		return true
	case "down", "ctrl+n":
		if len(items) > 0 {
			m.mention.sel = (m.mention.sel + 1) % len(items)
		}
		return true
	case "tab", "enter":
		if len(items) > 0 {
			m.insertMention(items[min(m.mention.sel, len(items)-1)])
		}
		return true
	}
	return false
}

// insertMention puts it in place of the @ and the query, and closes the list,
// or keeps it open in a folder.
func (m *Model) insertMention(it Mention) {
	text := it.Insert
	if it.Kind == MentionFolder {
		text = "@" + text
	} else {
		text += " "
	}
	m.edited()
	ins := segment(nil, clean(text))
	at := m.mention.at
	m.change(at, m.cur-at, ins, at+len(ins))
	m.mention = nil
	m.syncMention()
}

// MentionAt is the entry of the open list drawn on line row of the last
// frame, counted from its first line, and false when no entry is there.
func (m *Model) MentionAt(row int) (int, bool) {
	if m.mention == nil || m.mention.row < 0 || row < m.mention.row {
		return 0, false
	}
	i := m.mention.top + row - m.mention.row
	n := len(m.mentions())
	if row-m.mention.row >= mentionRows || i >= n {
		return 0, false
	}
	return i, true
}

// PickMention puts entry i of the open list in, as tab does on it. It
// reports whether there was such an entry.
func (m *Model) PickMention(i int) bool {
	items := m.mentions()
	if i < 0 || i >= len(items) {
		return false
	}
	m.endHistoryWalk()
	m.goal = -1
	m.insertMention(items[i])
	return true
}

// renderMentions draws the open list, under the editor, from frame line row.
func (m *Model) renderMentions(width, row int, in inks) []string {
	items := m.mentions()
	if m.mention == nil {
		return nil
	}
	st := &in.st
	bg := in.surface
	pad := on(bg).Render(strings.Repeat(" ", gutter))
	m.mention.row = -1
	if len(items) == 0 {
		dim := on(bg).Foreground(st.readable(st.Dim, bg))
		return []string{fill(pad+dim.Render("No pane, agent or file matches."), width, bg)}
	}
	sel := min(m.mention.sel, len(items)-1)
	// The selected entry is marked in the gutter as well as by its ground,
	// so it reads at 16 colours, where there are no grounds.
	marker := on(bg).Foreground(st.readable(st.Accent, bg)).Bold(true).Render(st.arrow("›", ">")) + on(bg).Render(" ")
	top := max(0, min(sel-mentionRows+1, len(items)-mentionRows))
	m.mention.row, m.mention.top = row, top
	out := make([]string, 0, mentionRows)
	for i := top; i < len(items) && i < top+mentionRows; i++ {
		it := items[i]
		selected := i == sel
		ground := st.ground(selected, bg)
		s := on(ground)
		badge := mentionBadges[it.Kind]
		row := s.Foreground(st.readable(st.Muted, ground)).Render(badge+strings.Repeat(" ", 6-len(badge))) +
			s.Foreground(st.readable(st.Text, ground)).Render(it.Label)
		if it.Note != "" {
			row += s.Foreground(st.readable(st.Dim, ground)).Render("  " + it.Note)
		}
		row = ansi.Truncate(row, width-gutter, "…")
		lead := pad
		if selected {
			lead = marker
		}
		out = append(out, lead+st.row(row, width-gutter, selected, bg))
	}
	return out
}

// mentionHints are the editor's keys while the list is open.
func mentionHints(ascii bool) []Hint {
	return []Hint{
		{Key: "tab", Label: "insert"},
		{Key: arrowKeys(ascii), Label: "choose"},
		{Key: "esc", Label: "close"},
	}
}
