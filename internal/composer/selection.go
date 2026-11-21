package composer

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Selection reports the selected cells as [lo, hi). ok is false when nothing
// is selected.
func (m *Model) Selection() (lo, hi int, ok bool) {
	if m.anchor < 0 || m.anchor == m.cur {
		return m.cur, m.cur, false
	}
	return min(m.anchor, m.cur), max(m.anchor, m.cur), true
}

// SelectedText is the text of the selection, with a pasted block's own text in
// place of its label, as a send would put it.
func (m *Model) SelectedText() string {
	lo, hi, ok := m.Selection()
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, c := range m.cells[lo:hi] {
		if c.block != nil {
			b.WriteString(c.block.text)
			continue
		}
		b.WriteString(c.s)
	}
	return b.String()
}

// Copied is the text the last EventCopy or EventCut took.
func (m *Model) Copied() string { return m.copied }

// SelectAll selects the whole draft.
func (m *Model) SelectAll() {
	if len(m.cells) == 0 {
		return
	}
	m.anchor, m.cur = 0, len(m.cells)
	m.joinTyping = false
}

func (m *Model) unselect() { m.anchor = -1 }

func (m *Model) extendTo(i int) {
	if m.anchor < 0 {
		m.anchor = m.cur
	}
	m.cur = min(max(i, 0), len(m.cells))
	m.joinTyping = false
	if m.anchor == m.cur {
		m.anchor = -1
	}
}

func (m *Model) deleteSelection() bool {
	lo, hi, ok := m.Selection()
	if !ok {
		return false
	}
	m.edited()
	m.change(lo, hi-lo, nil, lo)
	m.anchor = -1
	return true
}

func (m *Model) replaceSelection(ins []cell) bool {
	lo, hi, ok := m.Selection()
	if !ok {
		return false
	}
	m.edited()
	m.change(lo, hi-lo, ins, lo+len(ins))
	m.anchor = -1
	return true
}

func (m *Model) selectedBytes() int {
	lo, hi, ok := m.Selection()
	if !ok {
		return 0
	}
	n := 0
	for _, c := range m.cells[lo:hi] {
		n += c.bytes()
	}
	return n
}

func (m *Model) wordAround(i int) (lo, hi int) {
	n := len(m.cells)
	if n == 0 {
		return 0, 0
	}
	i = min(max(i, 0), n-1)
	c := m.cells[i]
	if c.block != nil || c.s == "\n" {
		return i, i + 1
	}
	space := c.isSpace()
	lo, hi = i, i+1
	for lo > 0 && m.cells[lo-1].block == nil && m.cells[lo-1].s != "\n" && m.cells[lo-1].isSpace() == space {
		lo--
	}
	for hi < n && m.cells[hi].block == nil && m.cells[hi].s != "\n" && m.cells[hi].isSpace() == space {
		hi++
	}
	return lo, hi
}

func (m *Model) lineAround(i int) (lo, hi int) {
	i = min(max(i, 0), len(m.cells))
	lo, hi = i, i
	for lo > 0 && m.cells[lo-1].s != "\n" {
		lo--
	}
	for hi < len(m.cells) && m.cells[hi].s != "\n" {
		hi++
	}
	return lo, hi
}

func (m *Model) pointAt(line, col int) int {
	rows := m.rows(m.width)
	r := m.top + line - m.rowLine
	r = min(max(r, 0), len(rows)-1)
	return m.indexAt(rows[r], max(col-gutter, 0), m.width)
}

// Click places the cursor at line and column of the last frame, as a mouse
// press there does. count is the press count: 2 selects the word under it and
// 3 its line. With shift, the selection runs from where it was to there. A
// click while a block is open does nothing.
func (m *Model) Click(line, col, count int, shift bool) {
	if m.pager != nil || m.hsearch != nil {
		return
	}
	m.endHistoryWalk()
	m.goal = -1
	i := m.pointAt(line, col)
	switch {
	case count >= 3:
		m.anchor, m.cur = m.lineAround(i)
	case count == 2:
		m.anchor, m.cur = m.wordAround(i)
	case shift:
		m.extendTo(i)
	default:
		m.cur, m.anchor = i, i
	}
	if m.anchor == m.cur {
		m.anchor = -1
	}
	m.dragFrom = m.anchor
	if m.dragFrom < 0 {
		m.dragFrom = m.cur
	}
	m.joinTyping = false
	m.syncMention()
}

// Drag moves the cursor to line and column of the last frame while the mouse
// button is held, selecting from where the press landed. A drag above or below
// the rows shown scrolls one row toward it.
func (m *Model) Drag(line, col int) {
	if m.pager != nil || m.hsearch != nil {
		return
	}
	rows := m.rows(m.width)
	switch shown := min(len(rows), m.textRows(m.rowLine)); {
	case line < m.rowLine && m.top > 0:
		m.top--
	case line >= m.rowLine+shown && m.top+shown < len(rows):
		m.top++
	}
	i := m.pointAt(line, col)
	m.anchor, m.cur = m.dragFrom, i
	if m.anchor == m.cur {
		m.anchor = -1
	}
	m.joinTyping = false
	m.syncMention()
}

func (m *Model) selectionKey(msg tea.KeyPressMsg, key string) (Event, bool) {
	switch key {
	case "shift+left":
		m.extendTo(m.cur - 1)
	case "shift+right":
		m.extendTo(m.cur + 1)
	case "alt+shift+left", "ctrl+shift+left":
		m.extendTo(m.wordLeft())
	case "alt+shift+right", "ctrl+shift+right":
		m.extendTo(m.wordRight())
	case "shift+home":
		m.extendTo(m.lineStart())
	case "shift+end":
		m.extendTo(m.lineEnd())
	case "ctrl+shift+home":
		m.extendTo(0)
	case "ctrl+shift+end":
		m.extendTo(len(m.cells))
	case "shift+up", "shift+down":
		if m.anchor < 0 {
			m.anchor = m.cur
		}
		d := -1
		if key == "shift+down" {
			d = 1
		}
		m.verticalKeep(d)
		if m.anchor == m.cur {
			m.anchor = -1
		}
		return EventNone, true
	case "super+a", "ctrl+shift+a":
		m.SelectAll()
	case "ctrl+c", "super+c":
		if text := m.SelectedText(); text != "" {
			m.copied = text
			return EventCopy, true
		}
		return EventNone, key == "super+c"
	case "ctrl+x", "super+x":
		if text := m.SelectedText(); text != "" {
			m.copied = text
			m.deleteSelection()
			return EventCut, true
		}
		return EventNone, true
	default:
		return m.withSelection(msg, key)
	}
	m.goal = -1
	return EventNone, true
}

func (m *Model) withSelection(msg tea.KeyPressMsg, key string) (Event, bool) {
	lo, hi, ok := m.Selection()
	if !ok {
		m.anchor = -1
		return EventNone, false
	}
	switch key {
	case "left":
		m.unselect()
		m.move(lo)
		return EventNone, true
	case "right":
		m.unselect()
		m.move(hi)
		return EventNone, true
	case "backspace", "ctrl+h", "delete", "ctrl+d", "ctrl+w", "alt+backspace", "ctrl+backspace",
		"alt+d", "alt+delete", "ctrl+delete":
		m.deleteSelection()
		return EventNone, true
	case "space":
		m.typeOver(" ")
		return EventNone, true
	case "ctrl+j", "shift+enter", "ctrl+enter":
		if !m.opt.SingleLine {
			m.typeOver("\n")
		}
		return EventNone, true
	case "ctrl+z", "ctrl+_", "ctrl+shift+z", "alt+z", "enter", "alt+enter", "esc", "ctrl+v", "ctrl+r",
		"ctrl+g", "ctrl+o", "tab", "shift+tab":
		m.unselect()
		return EventNone, false
	}
	if msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper|tea.ModMeta|tea.ModHyper) == 0 && msg.Text != "" {
		m.typeOver(msg.Text)
		return EventNone, true
	}
	m.unselect()
	return EventNone, false
}

func (m *Model) typeOver(text string) {
	text = clean(text)
	if text == "" {
		return
	}
	if m.size-m.selectedBytes()+len(text) > m.opt.MaxBytes {
		m.notice = "The message is too long to add more."
		return
	}
	ins := segment(nil, text)
	m.replaceSelection(ins)
	if n := len(m.undo); n > 0 {
		m.undo[n-1].typing = true
		m.joinTyping = true
	}
}

func (m *Model) verticalKeep(d int) {
	width := m.width
	rows := m.rows(width)
	r := rowOf(rows, m.cur)
	if m.goal < 0 {
		m.goal = m.colOf(rows[r], m.cur, width)
	}
	switch t := r + d; {
	case t < 0:
		m.cur = 0
	case t >= len(rows):
		m.cur = len(m.cells)
	default:
		m.cur = m.indexAt(rows[t], m.goal, width)
	}
	m.joinTyping = false
}
