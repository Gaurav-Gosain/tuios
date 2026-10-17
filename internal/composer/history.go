package composer

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// History: the messages sent to the same target before, which up brings back.
//
// up on the draft's first row loads the message before the one shown, and
// down on the last row the one after; down past the newest puts back the draft
// that was there before. Each load is one undo step. Any edit ends the walk,
// and the loaded text is the draft from then on.
//
// ctrl+r searches: a line opens under the editor, what is typed narrows the
// list to the messages that hold it, up and down choose, enter or tab loads
// the chosen one, and esc closes the search.
//
// The host gives the history (SetHistory). The composer keeps no history of
// its own and reads nothing: the host says where it is kept.

// historyRows is how many messages the search shows.
const historyRows = 6

// HistoryEntry is one message sent before.
type HistoryEntry struct {
	Parts []Part
	// Dropped names the blocks the history could not keep. Loading the
	// entry says they were removed.
	Dropped []string
}

// histSearch is the open ctrl+r search.
type histSearch struct {
	query string
	sel   int
}

// SetHistory gives the composer the target's history, oldest first.
func (m *Model) SetHistory(entries []HistoryEntry) {
	m.hist = entries
	m.histAt = -1
}

// historyActive reports whether up and down walk the history now.
func (m *Model) historyActive() bool { return len(m.hist) > 0 }

// historyStep loads the message d steps from the one shown: -1 older, 1
// newer. It reports whether it moved.
func (m *Model) historyStep(d int) bool {
	if len(m.hist) == 0 {
		return false
	}
	at := m.histAt
	switch {
	case at < 0 && d < 0:
		m.histDraft = append([]cell(nil), m.cells...)
		at = len(m.hist) - 1
	case at < 0:
		return false
	default:
		at += d
	}
	if at < 0 {
		return false
	}
	if at >= len(m.hist) {
		m.histAt = -1
		m.replaceCells(m.histDraft, "")
		m.histDraft = nil
		return true
	}
	m.histAt = at
	m.loadEntry(m.hist[at])
	return true
}

// loadEntry puts an entry in the draft as one undo step, and says which of
// its blocks the history could not keep.
func (m *Model) loadEntry(e HistoryEntry) {
	notice := ""
	if len(e.Dropped) > 0 {
		names := strings.Join(e.Dropped, ", ")
		verb := "is"
		pronoun := "It was"
		if len(e.Dropped) > 1 {
			verb, pronoun = "are", "They were"
		}
		notice = names + " " + verb + " no longer kept. " + pronoun + " removed from the message."
	}
	m.replaceCells(cellsOf(e.Parts), notice)
}

// replaceCells puts cells in place of the whole draft, as one undo step, with
// the cursor at the end.
func (m *Model) replaceCells(cells []cell, notice string) {
	m.edited()
	m.change(0, len(m.cells), append([]cell(nil), cells...), len(cells))
	m.notice = notice
	m.top = 0
}

// cellsOf is the cells of a message's parts.
func cellsOf(parts []Part) []cell {
	var out []cell
	for _, p := range parts {
		switch p.Kind {
		case PartText:
			out = segment(out, clean(p.Text))
		case PartPaste:
			out = append(out, blockCell(newBlock(p.N, p.Text)))
		case PartImage:
			if p.Image != nil {
				out = append(out, blockCell(newImageBlock(p.N, p.Image)))
			}
		}
	}
	return out
}

// ReplaceText puts text in place of the whole draft as one undo step, with
// the cursor at the end, for the text an external editor wrote. The editor
// had each image as its label, so a label still in the text gets its image
// back, once. Text longer than the draft holds is refused, the draft stays as
// it was, and ReplaceText reports false.
func (m *Model) ReplaceText(text string) bool {
	text = clean(text)
	if len(text) > m.opt.MaxBytes {
		m.notice = "The editor wrote more than the draft holds. The draft is as it was."
		return false
	}
	var images []*block
	for _, c := range m.cells {
		if c.block != nil && c.block.img != nil {
			images = append(images, c.block)
		}
	}
	var cells []cell
	for text != "" {
		at, which := -1, -1
		for i, b := range images {
			if b == nil {
				continue
			}
			if j := strings.Index(text, b.label); j >= 0 && (at < 0 || j < at) {
				at, which = j, i
			}
		}
		if at < 0 {
			cells = segment(cells, text)
			break
		}
		cells = segment(cells, text[:at])
		cells = append(cells, blockCell(images[which]))
		text = text[at+len(images[which].label):]
		images[which] = nil
	}
	m.replaceCells(cells, "")
	return true
}

// endHistoryWalk ends a walk: the loaded message is the draft now.
func (m *Model) endHistoryWalk() {
	m.histAt, m.histDraft = -1, nil
}

// OpenHistorySearch opens the ctrl+r search. It reports false when there is
// no history to search.
func (m *Model) OpenHistorySearch() bool {
	if len(m.hist) == 0 {
		m.notice = "Nothing was sent to this agent yet."
		return false
	}
	m.hsearch = &histSearch{}
	return true
}

// historyMatches are the entries the search shows, newest first.
func (m *Model) historyMatches() []int {
	q := strings.ToLower(m.hsearch.query)
	var out []int
	for i := len(m.hist) - 1; i >= 0; i-- {
		if q == "" || strings.Contains(strings.ToLower(JoinParts(m.hist[i].Parts)), q) {
			out = append(out, i)
		}
	}
	return out
}

// historyKey handles a key while the search is open.
func (m *Model) historyKey(msg tea.KeyPressMsg) {
	key := msg.String()
	s := m.hsearch
	matches := m.historyMatches()
	switch key {
	case "esc", "ctrl+g", "ctrl+c":
		m.hsearch = nil
	case "enter", "tab":
		if len(matches) > 0 {
			m.loadEntry(m.hist[matches[min(s.sel, len(matches)-1)]])
			m.endHistoryWalk()
		}
		m.hsearch = nil
	case "up", "ctrl+p", "ctrl+r":
		if len(matches) > 0 {
			s.sel = (s.sel + 1) % len(matches)
		}
	case "down", "ctrl+n":
		if len(matches) > 0 {
			s.sel = (s.sel - 1 + len(matches)) % len(matches)
		}
	case "backspace":
		if r := []rune(s.query); len(r) > 0 {
			s.query = string(r[:len(r)-1])
			s.sel = 0
		}
	case "space":
		s.query += " "
		s.sel = 0
	default:
		if msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper|tea.ModMeta|tea.ModHyper) == 0 && msg.Text != "" {
			s.query += clean(msg.Text)
			s.sel = 0
		}
	}
}

// renderHistorySearch draws the open search under the editor.
func (m *Model) renderHistorySearch(width int, in inks) []string {
	s := m.hsearch
	st := &in.st
	bg := in.surface
	pad := on(bg).Render(strings.Repeat(" ", gutter))
	dim := on(bg).Foreground(st.readable(st.Dim, bg))
	out := []string{fill(pad+dim.Render("history: ")+on(bg).Foreground(st.readable(st.Text, bg)).Render(s.query)+cursor(" ", bg, in.text), width, bg)}
	matches := m.historyMatches()
	if len(matches) == 0 {
		return append(out, fill(pad+dim.Render("No message sent before holds that."), width, bg))
	}
	sel := min(s.sel, len(matches)-1)
	top := max(0, min(sel-historyRows+1, len(matches)-historyRows))
	for i := top; i < len(matches) && i < top+historyRows; i++ {
		selected := i == sel
		ground := st.ground(selected, bg)
		line, _, more := strings.Cut(strings.TrimSpace(JoinParts(m.hist[matches[i]].Parts)), "\n")
		line = strings.Join(strings.Fields(line), " ")
		if more {
			line += " …"
		}
		row := on(ground).Foreground(st.readable(st.Muted, ground)).Render(strconv.Itoa(len(m.hist)-matches[i])+" ") +
			on(ground).Foreground(st.readable(st.Text, ground)).Render(line)
		out = append(out, pad+st.row(ansi.Truncate(row, width-gutter, "…"), width-gutter, selected, bg))
	}
	return out
}

// historyHints are the editor's keys while the search is open.
func historyHints(ascii bool) []Hint {
	return []Hint{
		{Key: enterKey(ascii), Label: "load"},
		{Key: arrowKeys(ascii), Label: "choose"},
		{Key: "esc", Label: "close"},
	}
}

// Cleared is the draft ctrl+c cleared last, for the host to keep in the
// history, nil when there is none.
func (m *Model) Cleared() []Part { return m.cleared }
