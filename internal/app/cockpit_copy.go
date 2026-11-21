package app

import (
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

type ckCopyRow struct {
	cells      []uv.Cell
	more, cont bool
	id         string
}

type ckThreadCopy struct {
	cm       terminal.CopyMode
	key      string
	rows     []ckCopyRow
	out      []string
	w, h     int
	offset   int
	implicit bool
}

func ckCopyOf(l ckLook, lines []ckLine, ids []string, w int) []ckCopyRow {
	glyphs := ckGlyphSet(l)
	rows := make([]ckCopyRow, len(lines))
	for i, ln := range lines {
		cells := make([]uv.Cell, w)
		for x := range cells {
			cells[x] = uv.Cell{Content: " ", Width: 1}
		}
		put := func(x int, s, link string) int {
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
				cells[x] = uv.Cell{Content: g, Width: gw, Link: uv.Link{URL: link}}
				for k := 1; k < gw; k++ {
					cells[x+k] = uv.Cell{}
				}
				x += gw
			}
			return x
		}
		if ln.raw != "" {
			put(ln.indent, ansi.Strip(ln.raw), "")
		} else {
			x := ln.indent
			for _, sg := range ln.segs {
				if t := strings.TrimSpace(sg.t); glyphs[t] || ckChromeGlyph(t) {
					x += ckTextWidth(sg.t)
					continue
				}
				x = put(x, sg.t, sg.link)
			}
		}
		rows[i].cells = cells
		rows[i].more, rows[i].cont = ln.wrap, ln.cont
		if i < len(ids) {
			rows[i].id = ids[i]
		}
	}
	return rows
}

func ckChromeGlyph(t string) bool {
	r, n := utf8.DecodeRuneInString(t)
	return n == len(t) && r >= 0xe000 && r <= 0xf8ff
}

func ckGlyphSet(l ckLook) map[string]bool {
	set := map[string]bool{"▎": true, "━": true, "─": true}
	for _, g := range []ckGlyph{ckGlyphFold, ckGlyphOpen, ckGlyphReview, ckGlyphFailed, ckGlyphWorking, ckGlyphIdle, ckGlyphNeeds, ckGlyphUnseen} {
		if s := strings.TrimSpace(l.g(g)); s != "" {
			set[s] = true
		}
	}
	return set
}

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
	if !m.cockpitCopyBegin(false) {
		return false
	}
	m.ShowNotification("Copy mode. v selects, y copies, Y copies as Markdown, gx opens a link, esc stops.", "info", m.Settings.NotificationDuration)
	return true
}

func (m *OS) cockpitCopyBegin(implicit bool) bool {
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
	_, end := t.follow.Window()
	if t.actRow {
		end--
	}
	q.SetScrollOffset(len(q.rows) - min(end, len(q.rows)))
	q.cm = terminal.CopyMode{Active: true, State: terminal.CopyModeNormal, ScrollOffset: q.offset, Implicit: implicit}
	q.implicit = implicit
	m.Inbox.threads.copy = q
	m.cockpitTouch()
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

// CockpitCopyCheck ends copy mode over the thread once it no longer fits
// what the view shows: the cursor went to another agent, the thread tab
// closed, or the view did. Copy mode is for the thread it started on, and a
// pointer kept past that refuses the next start and comes back with old rows
// when the person returns to the agent. Update asks it after every message:
// one pointer check when copy mode is off.
func (m *OS) CockpitCopyCheck() {
	if m.Inbox.threads.copy != nil && m.threadCopy() == nil {
		m.Inbox.threads.copy = nil
		m.cockpitTouch()
	}
}

// CockpitCopyEnd ends copy mode over the thread.
func (m *OS) CockpitCopyEnd() {
	if m.Inbox.threads.copy != nil {
		m.Inbox.threads.copy = nil
		m.cockpitTouch()
	}
}

// CockpitCopyText is the text of the selection as the agent wrote it: rows
// that wrap one paragraph joined with a space, the rows of a wrapped code
// line joined with nothing, and the indent the rows share taken off. block takes the columns from x0 to x1 of each row.
func (m *OS) CockpitCopyText(start, end terminal.Position, line, block bool) string {
	q := m.threadCopy()
	if q == nil {
		return ""
	}
	return ckCopyRowsText(q.rows, q.w, start, end, line, block)
}

func ckCopyRowsText(rows []ckCopyRow, w int, start, end terminal.Position, line, block bool) string {
	if start.Y > end.Y || start.Y == end.Y && start.X > end.X {
		start, end = end, start
	}
	x0, x1 := min(start.X, end.X), max(start.X, end.X)
	type piece struct {
		text       string
		more, cont bool
	}
	var parts []piece
	for y := max(start.Y, 0); y <= min(end.Y, len(rows)-1); y++ {
		r := rows[y]
		a, b := 0, w-1
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
		parts = append(parts, piece{text: ckCellsText(r.cells, a, b), more: r.more && !block && y < end.Y, cont: r.cont && !block && y > start.Y})
	}
	indent := -1
	for i, p := range parts {
		if strings.TrimSpace(p.text) == "" || i > 0 && parts[i-1].more {
			continue
		}
		if n := len(p.text) - len(strings.TrimLeft(p.text, " ")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, 0, 2*len(parts))
	for i, p := range parts {
		s := strings.TrimRight(p.text, " ")
		if i > 0 && parts[i-1].more {
			s = strings.TrimLeft(s, " ")
		} else if indent > 0 && len(s) >= indent {
			s = s[indent:]
		}
		sep := "\n"
		switch {
		case i+1 < len(parts) && parts[i+1].cont:
			sep = ""
		case p.more:
			sep = " "
		}
		out = append(out, s, sep)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out[:len(out)-1], "")
}

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
		out := make([]string, 0, 3*len(en.Todos))
		for _, td := range en.Todos {
			mark := "- [ ] "
			if td.Status == "completed" {
				mark = "- [x] "
			}
			out = append(out, mark, td.Text, "\n")
		}
		return strings.TrimSuffix(strings.Join(out, ""), "\n")
	case en.Kind == "tool_call" && en.Diff != nil:
		out := []string{"```diff\n--- a/", en.Diff.File, "\n+++ b/", en.Diff.File, "\n"}
		for _, h := range en.Diff.Hunks {
			for _, dl := range h.Lines {
				out = append(out, dl.Op, dl.Text, "\n")
			}
		}
		return strings.Join(append(out, "```"), "")
	case en.Kind == "tool_call" && en.Target == "":
		return "`" + en.Tool + "`"
	case en.Kind == "tool_call":
		return "`" + en.Tool + " " + en.Target + "`"
	}
	return ""
}

func (m *OS) drawThreadCopy(c *ckCanvas, t *agentThread, x, w, top, room, screenTop int) bool {
	if t == nil {
		return false
	}
	t.viewW, t.viewH = w, room
	t.viewX, t.viewY = x, screenTop+top
	q := m.threadCopy()
	if q == nil || q.key != t.key {
		return false
	}
	if q.w != w || q.h != room {
		m.Inbox.threads.copy = nil
		return false
	}
	top += room - q.visible()
	first := q.top()
	for y := 0; y < q.visible() && first+y < len(q.out); y++ {
		c.draw(x, top+y, w, q.out[first+y])
	}
	drawCopyMarks(c, m.marks(), &q.cm, x, top, q.w, q.visible(), first)
	return true
}

func (q *ckThreadCopy) visible() int { return min(q.h, len(q.rows)) }

func (q *ckThreadCopy) top() int { return q.ScrollbackLen() - q.offset }

func (q *ckThreadCopy) screenRow(y int) int { return q.ScrollbackLen() + y }

func (q *ckThreadCopy) row(i int) []uv.Cell {
	if i < 0 || i >= len(q.rows) {
		return nil
	}
	return q.rows[i].cells
}

func (q *ckThreadCopy) Text() terminal.CopyText { return q }

func (q *ckThreadCopy) ScrollbackLen() int { return max(len(q.rows)-q.h, 0) }

func (q *ckThreadCopy) ScrollbackLine(i int) uv.Line { return q.row(i) }

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

func (q *ckThreadCopy) ScrollbackGeneration() uint64 { return 1 }

func (q *ckThreadCopy) CellAt(x, y int) *uv.Cell {
	cells := q.row(q.screenRow(y))
	if x < 0 || x >= len(cells) {
		return nil
	}
	return &cells[x]
}

func (q *ckThreadCopy) Width() int { return q.w }

func (q *ckThreadCopy) Height() int { return q.h }

func (q *ckThreadCopy) wraps(i int) (bool, bool) {
	if i < 0 || i >= len(q.rows) {
		return false, true
	}
	return q.rows[i].more, true
}

func (q *ckThreadCopy) RowSoftWrapped(y int) (bool, bool) { return q.wraps(q.screenRow(y)) }

func (q *ckThreadCopy) ScrollbackSoftWrapped(i int) (bool, bool) { return q.wraps(i) }

func (q *ckThreadCopy) RowPadded(int) bool { return false }

func (q *ckThreadCopy) ScrollbackPadded(int) bool { return false }

func (q *ckThreadCopy) ScrollOffset() int { return q.offset }

func (q *ckThreadCopy) SetScrollOffset(offset int) {
	q.offset = max(min(offset, q.ScrollbackLen()), 0)
}

func (q *ckThreadCopy) ViewHeight() int { return q.visible() }

func (q *ckThreadCopy) ContentHeight() int { return q.visible() }

func (q *ckThreadCopy) LastContentRow() int { return q.visible() - 1 }

func (q *ckThreadCopy) LastContentCol() int { return q.w - 1 }

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
