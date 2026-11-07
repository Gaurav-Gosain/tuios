package app

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

// Copy mode in the agents view's thread. The copy_mode key starts the copy
// mode a pane has over the thread as it is drawn: the same motions,
// selections, search and yanks, run by the same code in internal/input
// (copymode_cockpit.go), as quick look's preview does (quicklook_copy.go).
//
// The thread is a terminal.CopyGrid for it. Each row is a line of the thread
// at the columns the screen shows it, with the chrome left out: a card's
// edge, the marks at the right (a status, a language), and the fold and state
// glyphs are blank cells. So the cursor and the selection sit on the text the
// person sees, and a yank holds none of the chrome.
//
// The rows are taken when copy mode starts and held: the thread may grow
// while it runs, and a row keeps its number. Copy mode ends when the thread
// is drawn at another width, or another agent is selected.
//
// A yank is the selected text as the agent wrote it, not as the thread
// wrapped it: rows that wrap one paragraph join with a space, and the indent
// the selection shares is taken off. Y yanks the entries under the selection
// as Markdown: the agent's own text, a prompt as a quote, and an edit as a
// diff block.

// ckCopyRow is one row of the thread for copy mode.
type ckCopyRow struct {
	cells []uv.Cell
	// more says the next row goes on with the same paragraph.
	more bool
	// id is the entry the row belongs to, "" for a row of no entry.
	id string
}

// ckThreadCopy is copy mode over a thread.
type ckThreadCopy struct {
	cm   terminal.CopyMode
	key  string
	rows []ckCopyRow
	// out is the thread's lines as drawn when copy mode started.
	out []string
	// w and h are the thread's width and the rows of its view.
	w, h   int
	offset int
}

// ckCopyOf is the copy rows of the thread's lines: each line at width w
// cells, its chrome blank.
func ckCopyOf(l ckLook, lines []ckLine, ids []string, w int) []ckCopyRow {
	glyphs := ckGlyphSet(l)
	rows := make([]ckCopyRow, len(lines))
	for i, ln := range lines {
		cells := make([]uv.Cell, w)
		for x := range cells {
			cells[x] = uv.Cell{Content: " ", Width: 1}
		}
		put := func(x int, s string) int {
			for len(s) > 0 && x < w {
				g, gw := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
				if g == "" {
					g = s[:1]
				}
				s = s[len(g):]
				gw = max(gw, 1)
				if x+gw > w {
					break
				}
				cells[x] = uv.Cell{Content: g, Width: gw}
				for k := 1; k < gw; k++ {
					cells[x+k] = uv.Cell{}
				}
				x += gw
			}
			return x
		}
		if ln.raw != "" {
			put(ln.indent, ansi.Strip(ln.raw))
		} else {
			x := ln.indent
			for _, sg := range ln.segs {
				if glyphs[strings.TrimSpace(sg.t)] {
					x += ckTextWidth(sg.t)
					continue
				}
				x = put(x, sg.t)
			}
		}
		rows[i].cells = cells
		rows[i].more = ln.wrap
		if i < len(ids) {
			rows[i].id = ids[i]
		}
	}
	return rows
}

// ckGlyphSet is the glyphs the thread draws as chrome, in the look's glyph
// set.
func ckGlyphSet(l ckLook) map[string]bool {
	set := map[string]bool{"▎": true, "━": true, "─": true}
	for _, g := range []ckGlyph{ckGlyphFold, ckGlyphOpen, ckGlyphReview, ckGlyphFailed, ckGlyphWorking, ckGlyphIdle, ckGlyphNeeds, ckGlyphUnseen} {
		if s := strings.TrimSpace(l.g(g)); s != "" {
			set[s] = true
		}
	}
	return set
}

// threadCopy is the copy mode of the shown thread, nil when it is off or no
// longer fits what the thread shows.
func (m *OS) threadCopy() *ckThreadCopy {
	q := m.Inbox.threads.copy
	if q == nil || !m.ShowInbox || !m.threadShown() {
		return nil
	}
	e, ok := m.cockpitAgent()
	if !ok || agentKey(e) != q.key {
		return nil
	}
	return q
}

// CockpitCopyOn reports whether copy mode runs over the thread.
func (m *OS) CockpitCopyOn() bool { return m.threadCopy() != nil }

// CockpitCopyStart starts copy mode over the shown thread, with the cursor on
// the first row in view. It reports false when no conversation is on screen,
// or before the thread has been drawn once.
func (m *OS) CockpitCopyStart() bool {
	if !m.ShowInbox || !m.threadShown() || m.Inbox.threads.copy != nil || m.cockpitTab() != "Thread" {
		return false
	}
	e, ok := m.cockpitAgent()
	if !ok || !m.threadReadsTranscript(e) {
		return false
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil || len(t.lines) == 0 || len(t.lines) != len(t.body) || t.viewW <= 0 || t.viewH <= 0 {
		return false
	}
	q := &ckThreadCopy{key: t.key, out: t.body, w: t.viewW, h: t.viewH}
	q.rows = ckCopyOf(m.cockpitLook(), t.lines, t.lineIDs, q.w)
	// The view copy mode starts at is the one on screen.
	_, end := t.follow.Window()
	q.SetScrollOffset(len(q.rows) - min(end, len(q.rows)))
	q.cm = terminal.CopyMode{Active: true, State: terminal.CopyModeNormal, ScrollOffset: q.offset}
	m.Inbox.threads.copy = q
	m.cockpitTouch()
	m.ShowNotification("Copy mode. v selects, y copies, Y copies as Markdown, esc stops.", "info", m.Settings.NotificationDuration)
	return true
}

// CockpitCopy is copy mode's state and the grid it moves over, or nil and nil
// when copy mode is off. It is asked for each key, which may move the cursor
// or the selection, so the next frame draws the view again.
func (m *OS) CockpitCopy() (*terminal.CopyMode, terminal.CopyGrid) {
	q := m.threadCopy()
	if q == nil {
		return nil, nil
	}
	m.cockpitTouch()
	return &q.cm, q
}

// CockpitCopyEnd ends copy mode over the thread.
func (m *OS) CockpitCopyEnd() {
	if m.Inbox.threads.copy != nil {
		m.Inbox.threads.copy = nil
		m.cockpitTouch()
	}
}

// CockpitCopyText is the text of the selection as the agent wrote it: rows
// that wrap one paragraph joined with a space, and the indent the rows share
// taken off. block takes the columns from x0 to x1 of each row.
func (m *OS) CockpitCopyText(start, end terminal.Position, line, block bool) string {
	q := m.threadCopy()
	if q == nil {
		return ""
	}
	if start.Y > end.Y || start.Y == end.Y && start.X > end.X {
		start, end = end, start
	}
	x0, x1 := min(start.X, end.X), max(start.X, end.X)
	type piece struct {
		text string
		more bool
	}
	var parts []piece
	for y := max(start.Y, 0); y <= min(end.Y, len(q.rows)-1); y++ {
		r := q.rows[y]
		a, b := 0, q.w-1
		switch {
		case block:
			a, b = x0, x1
		case !line:
			if y == start.Y {
				a = start.X
			}
			if y == end.Y {
				b = end.X
			}
		}
		parts = append(parts, piece{text: ckCellsText(r.cells, a, b), more: r.more && !block && y < end.Y})
	}
	// The indent the rows share is chrome: a card's, a list's.
	indent := -1
	for i, p := range parts {
		if strings.TrimSpace(p.text) == "" || i > 0 && parts[i-1].more {
			continue
		}
		if n := len(p.text) - len(strings.TrimLeft(p.text, " ")); indent < 0 || n < indent {
			indent = n
		}
	}
	var sb strings.Builder
	for i, p := range parts {
		s := strings.TrimRight(p.text, " ")
		if i > 0 && parts[i-1].more {
			s = strings.TrimLeft(s, " ")
		} else if indent > 0 && len(s) >= indent {
			s = s[indent:]
		}
		sb.WriteString(s)
		if i < len(parts)-1 {
			if p.more {
				sb.WriteByte(' ')
			} else {
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

// ckCellsText is the text of cells a to b.
func ckCellsText(cells []uv.Cell, a, b int) string {
	var sb strings.Builder
	for x := max(a, 0); x <= min(b, len(cells)-1); x++ {
		sb.WriteString(cells[x].Content)
	}
	return sb.String()
}

// CockpitCopyMarkdown is the entries under rows y0 to y1 as Markdown: an
// answer as the agent wrote it, a prompt as a quote, an edit as a diff
// block, a call as its tool and target.
func (m *OS) CockpitCopyMarkdown(y0, y1 int) string {
	q := m.threadCopy()
	if q == nil {
		return ""
	}
	t := m.Inbox.threads.byKey[q.key]
	if t == nil {
		return ""
	}
	byID := make(map[string]*threadEntry, len(t.entries))
	for i := range t.entries {
		byID[t.entries[i].ID] = &t.entries[i]
	}
	var out []string
	seen := map[string]bool{}
	for y := max(min(y0, y1), 0); y <= min(max(y0, y1), len(q.rows)-1); y++ {
		id := q.rows[y].id
		en := byID[id]
		if en == nil || seen[id] {
			continue
		}
		seen[id] = true
		if md := ckEntryMarkdown(en); md != "" {
			out = append(out, md)
		}
	}
	return strings.Join(out, "\n\n")
}

// ckEntryMarkdown is one entry as Markdown, "" for one with none.
func ckEntryMarkdown(en *threadEntry) string {
	switch {
	case en.Image != nil:
		return "[image]"
	case en.Kind == "text" && en.Role == "user":
		return "> " + strings.ReplaceAll(strings.TrimSpace(en.Text), "\n", "\n> ")
	case en.Kind == "text":
		return strings.TrimSpace(en.Text)
	case en.Kind == "plan":
		return strings.TrimSpace(en.Plan)
	case en.Kind == "todos":
		var sb strings.Builder
		for i, td := range en.Todos {
			if i > 0 {
				sb.WriteByte('\n')
			}
			mark := "[ ]"
			if td.Status == "completed" {
				mark = "[x]"
			}
			sb.WriteString("- ")
			sb.WriteString(mark)
			sb.WriteByte(' ')
			sb.WriteString(td.Text)
		}
		return sb.String()
	case en.Kind == "tool_call" && en.Diff != nil:
		var sb strings.Builder
		sb.WriteString("```diff\n--- a/")
		sb.WriteString(en.Diff.File)
		sb.WriteString("\n+++ b/")
		sb.WriteString(en.Diff.File)
		sb.WriteByte('\n')
		for _, h := range en.Diff.Hunks {
			for _, dl := range h.Lines {
				sb.WriteString(dl.Op)
				sb.WriteString(dl.Text)
				sb.WriteByte('\n')
			}
		}
		sb.WriteString("```")
		return sb.String()
	case en.Kind == "tool_call":
		if en.Target == "" {
			return "`" + en.Tool + "`"
		}
		return "`" + en.Tool + " " + en.Target + "`"
	}
	return ""
}

// drawThreadCopy draws the thread as copy mode holds it, in room rows from
// row top, with the search matches, the selection and the cursor on it, and
// reports whether copy mode is on. It also notes the thread's view, which
// copy mode starts from.
func (m *OS) drawThreadCopy(c *ckCanvas, t *agentThread, x, w, top, room int) bool {
	if t == nil {
		return false
	}
	t.viewW, t.viewH = w, room
	q := m.threadCopy()
	if q == nil || q.key != t.key {
		return false
	}
	if q.w != w || q.h != room {
		// The thread is drawn at another size: the rows no longer fit it.
		m.Inbox.threads.copy = nil
		return false
	}
	first := q.top()
	for y := 0; y < room && first+y < len(q.out); y++ {
		c.draw(x, top+y, w, q.out[first+y])
	}
	drawCopyMarks(c, m.marks(), &q.cm, x, top, q.w, q.h, first)
	return true
}

// top is the row at the top of the view.
func (q *ckThreadCopy) top() int { return q.ScrollbackLen() - q.offset }

// screenRow is the row on line y of the view.
func (q *ckThreadCopy) screenRow(y int) int { return q.ScrollbackLen() + y }

// row is row i's cells, nil past the rows.
func (q *ckThreadCopy) row(i int) []uv.Cell {
	if i < 0 || i >= len(q.rows) {
		return nil
	}
	return q.rows[i].cells
}

// Text is the grid's text, the thread itself.
func (q *ckThreadCopy) Text() terminal.CopyText { return q }

// ScrollbackLen is the rows above the view when it is at the bottom.
func (q *ckThreadCopy) ScrollbackLen() int { return max(len(q.rows)-q.h, 0) }

// ScrollbackLine is row i above the bottom view.
func (q *ckThreadCopy) ScrollbackLine(i int) uv.Line { return q.row(i) }

// ScrollbackText calls fn with the rows from to end-1 as text cells.
func (q *ckThreadCopy) ScrollbackText(from, end int, fn func(index, width int, cells []vt.TextCell) bool) {
	var buf []vt.TextCell
	for i := max(from, 0); i < min(end, q.ScrollbackLen()); i++ {
		buf = buf[:0]
		for _, c := range q.row(i) {
			buf = append(buf, vt.TextCell{Content: []byte(c.Content), Width: c.Width})
		}
		if !fn(i, q.w, buf) {
			return
		}
	}
}

// ScrollbackGeneration does not change: the rows are held while copy mode
// runs.
func (q *ckThreadCopy) ScrollbackGeneration() uint64 { return 1 }

// CellAt is the cell at column x of line y of the bottom view.
func (q *ckThreadCopy) CellAt(x, y int) *uv.Cell {
	cells := q.row(q.screenRow(y))
	if x < 0 || x >= len(cells) {
		return nil
	}
	return &cells[x]
}

// Width is the thread's width.
func (q *ckThreadCopy) Width() int { return q.w }

// Height is the view's rows.
func (q *ckThreadCopy) Height() int { return q.h }

func (q *ckThreadCopy) wraps(i int) (bool, bool) {
	if i < 0 || i >= len(q.rows) {
		return false, true
	}
	return q.rows[i].more, true
}

// RowSoftWrapped says line y of the bottom view goes on in the next row.
func (q *ckThreadCopy) RowSoftWrapped(y int) (bool, bool) { return q.wraps(q.screenRow(y)) }

// ScrollbackSoftWrapped says row i goes on in the next row.
func (q *ckThreadCopy) ScrollbackSoftWrapped(i int) (bool, bool) { return q.wraps(i) }

// RowPadded is false: a row ends where its last cluster does.
func (q *ckThreadCopy) RowPadded(int) bool { return false }

// ScrollbackPadded is false, as RowPadded.
func (q *ckThreadCopy) ScrollbackPadded(int) bool { return false }

// ScrollOffset is how many rows the view is above the bottom.
func (q *ckThreadCopy) ScrollOffset() int { return q.offset }

// SetScrollOffset moves the view offset rows above the bottom.
func (q *ckThreadCopy) SetScrollOffset(offset int) {
	q.offset = max(min(offset, q.ScrollbackLen()), 0)
}

// ViewHeight is the view's rows.
func (q *ckThreadCopy) ViewHeight() int { return q.h }

// ContentHeight is the view's rows.
func (q *ckThreadCopy) ContentHeight() int { return q.h }

// LastContentRow is the view's last row.
func (q *ckThreadCopy) LastContentRow() int { return q.h - 1 }

// LastContentCol is the thread's last column.
func (q *ckThreadCopy) LastContentCol() int { return q.w - 1 }

// threadWrapMore is threadWrap, and for each row whether the next one goes
// on with the same line of the text.
func threadWrapMore(text string, width int) ([]string, []bool) {
	var out []string
	var more []bool
	blank := false
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		line = printableTitle(line)
		if strings.TrimSpace(line) == "" {
			if !blank && len(out) > 0 {
				out, more = append(out, ""), append(more, false)
			}
			blank = true
			continue
		}
		blank = false
		rows := wrapPlain(line, width)
		for k, r := range rows {
			out, more = append(out, r), append(more, k < len(rows)-1)
		}
	}
	return out, more
}
