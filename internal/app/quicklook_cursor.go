package app

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The cursor inside a text preview. While the keyboard is inside a text, a
// cell cursor and a quiet highlight of its line show where the reader is. The
// line and column keys, the half page keys, start and end move the cursor,
// and the view scrolls only to keep it in view, as less with a cursor or
// vim's normal mode do. A move of the view the cursor did not make (the
// wheel, the ruler, a resize) brings the cursor into the view in the next
// frame. A search and go to line put the cursor on the line they find. A
// click on the text puts it under the pointer. Copy mode starts at the
// cursor, and the cursor stays where copy mode ended.
//
// Nothing runs for it: the cursor is drawn with the frame, and it does not
// blink.

// quickLookCursor is the cursor in a text. line is an index into res.lines,
// as scroll is, so a window joined above the held text or let go moves both
// by the same lines. col is the column the cursor wants, in cells: j and k
// keep it across a short line, as vim does, and the frame shows the cursor
// on the last character of a line shorter than that.
type quickLookCursor struct {
	line, col int
}

// quickLookDrawnRow is one row of the text the last frame drew: its line,
// the byte of the line it starts at, and the column of the line it starts
// at.
type quickLookDrawnRow struct {
	line, at, col int
}

// cursorShown reports whether the text shows the cursor: the keyboard is
// inside a text, in its own view, and copy mode is off.
func (ql *quickLookState) cursorShown() bool {
	r := ql.res
	return ql.open && ql.inside && ql.copy == nil && quickLookText(r) && r.target == ql.target &&
		len(r.lines) > 0 && !ql.quickLookOffMain()
}

// cursorTo puts the cursor on line at column col.
func (ql *quickLookState) cursorTo(line, col int) { ql.cur = quickLookCursor{line: line, col: col} }

// quickLookCellAt is the cluster of line under column want: the column it
// starts at and its byte. A column past the end of the line is the last
// cluster, and an empty line is 0, 0.
func quickLookCellAt(line string, want int) (col, at int) {
	x := 0
	for i := 0; i < len(line); {
		g, w := ansi.FirstGraphemeCluster(line[i:], ansi.GraphemeWidth)
		if g == "" {
			g = line[i : i+1]
		}
		w = max(w, 1)
		if x+w > want {
			return x, i
		}
		col, at = x, i
		x += w
		i += len(g)
	}
	return col, at
}

// quickLookColOf is the column byte at of line starts at.
func quickLookColOf(line string, at int) int {
	x := 0
	for i := 0; i < min(at, len(line)); {
		g, w := ansi.FirstGraphemeCluster(line[i:], ansi.GraphemeWidth)
		if g == "" {
			g = line[i : i+1]
		}
		x += max(w, 1)
		i += len(g)
	}
	return x
}

// quickLookWrapRowOf is the row of line, wrapped at width, that holds byte at.
func quickLookWrapRowOf(line string, at, width int) int {
	row, from := 0, 0
	for {
		end, _ := quickLookWrapNext(line, from, width)
		if at < end || end >= len(line) || end <= from {
			return row
		}
		row++
		from = end
	}
}

// cursorCell is the cursor's line as the frame shows it: the column and the
// byte of the cluster it is on. With wrap off, the cursor stays on the row.
func (ql *quickLookState) cursorCell() (col, at int) {
	r := ql.res
	want := ql.cur.col
	if !ql.wrap && ql.textW > 0 {
		want = min(want, ql.textW-1)
	}
	return quickLookCellAt(r.lines[ql.cur.line], want)
}

// cursorFollow holds the cursor to the held text and scrolls the view, as
// little as it can, so the cursor's row is in it. It uses the size of the
// text in the last frame.
func (ql *quickLookState) cursorFollow() {
	r := ql.res
	if !ql.cursorShown() {
		return
	}
	cur := &ql.cur
	cur.line = min(max(cur.line, 0), len(r.lines)-1)
	rows, w := ql.bodyRows, ql.textW
	if rows <= 0 || w <= 0 {
		return
	}
	// The frame shows no lower top than this, so the view is held to it
	// first: G puts the top past the end.
	last := len(r.lines) - rows
	if ql.wrap {
		last = quickLookLastWrapped(r.lines, rows, w)
	}
	ql.scroll = min(ql.scroll, max(last, 0))
	if cur.line < ql.scroll {
		ql.scroll = cur.line
		return
	}
	if !ql.wrap {
		ql.scroll = max(ql.scroll, cur.line-rows+1)
		return
	}
	// The lowest top that still shows the cursor's row.
	_, at := ql.cursorCell()
	used := quickLookWrapRowOf(r.lines[cur.line], at, w) + 1
	top := cur.line
	for top > 0 {
		n := quickLookWrapRows(r.lines[top-1], w)
		if used+n > rows {
			break
		}
		used += n
		top--
	}
	ql.scroll = max(ql.scroll, top)
}

// cursorIntoView moves the cursor into the view when a move of the view left
// it out: the frame draws the lines from on.
func (ql *quickLookState) cursorIntoView(from int) {
	r := ql.res
	cur := &ql.cur
	cur.line = min(max(cur.line, 0), len(r.lines)-1)
	last := min(from+ql.bodyRows-1, len(r.lines)-1)
	if ql.wrap {
		used := 0
		for n := from; n < len(r.lines); n++ {
			used += quickLookWrapRows(r.lines[n], ql.textW)
			if used >= ql.bodyRows {
				last = n
				break
			}
		}
	}
	cur.line = min(max(cur.line, from), last)
}

// QuickLookCursorMove moves the cursor inside a text for a line, column,
// half page, start or end key. It reports false when the text shows no
// cursor, and the key then acts as before.
func (m *OS) QuickLookCursorMove(act string) bool {
	ql := &m.quickLook
	if !ql.cursorShown() {
		return false
	}
	cur := &ql.cur
	cur.line = min(max(cur.line, 0), len(ql.res.lines)-1)
	switch act {
	case "line_down":
		cur.line++
	case "line_up":
		cur.line--
	case "column_left":
		col, _ := ql.cursorCell()
		cur.col = max(col-1, 0)
	case "column_right":
		col, at := ql.cursorCell()
		line := ql.res.lines[cur.line]
		if at < len(line) {
			g, w := ansi.FirstGraphemeCluster(line[at:], ansi.GraphemeWidth)
			if next := at + max(len(g), 1); next < len(line) && (ql.wrap || col+max(w, 1) < ql.textW) {
				cur.col = col + max(w, 1)
			} else {
				cur.col = col
			}
		}
	case "half_down", "half_up":
		n := m.quickLookPage()
		if act == "half_up" {
			n = -n
		}
		// The view and the cursor move together, as ctrl+d does in vim.
		m.QuickLookScroll(n)
		cur.line += n
	case "start":
		m.QuickLookEdge(false)
		ql.cursorTo(0, 0)
	case "end":
		m.QuickLookEdge(true)
		ql.cursorTo(len(ql.res.lines)-1, 0)
	default:
		return false
	}
	ql.cursorFollow()
	return true
}

// QuickLookCursorAt puts the cursor on the text under screen cell x, y, for a
// click. It reports false off the text, or when the text shows no cursor.
func (m *OS) QuickLookCursorAt(x, y int) bool {
	ql := &m.quickLook
	if !ql.cursorShown() || !m.QuickLookCopyOnText(x, y) {
		return false
	}
	cx, cy, _, _ := m.QuickLookCopyCell(x, y)
	if cy < 0 || cy >= len(ql.drawn) {
		return false
	}
	d := ql.drawn[cy]
	ql.cursorTo(d.line, d.col+cx)
	return true
}

// quickLookMatchCol is the column of the first match of q in line, 0 when
// there is none.
func quickLookMatchCol(line, q string) int {
	hay := line
	if quickLookFold(q) {
		hay = strings.ToLower(line)
	}
	i := strings.Index(hay, q)
	if i < 0 || len(hay) != len(line) {
		return 0
	}
	return quickLookColOf(line, i)
}

// drawQuickLookCursorRow marks a drawn row of the cursor's line: the quiet
// line highlight over the code, and the cursor cell when the row holds it.
// at and end are the bytes of the line the row shows, and col the column of
// the line it starts at.
func (m *OS) drawQuickLookCursorRow(c *ckCanvas, pal overlay.Palette, x, y, w, at, end, col int, last bool) {
	ql := &m.quickLook
	bg := pal.RowSelQuiet
	c.restyle(x, y, w, 1, func(cell *uv.Cell) { cell.Style.Bg = bg })
	ccol, cat := ql.cursorCell()
	if cat < at || cat >= end && !(last && cat >= at) {
		return
	}
	if cx := ccol - col; cx >= 0 && cx < w {
		c.restyle(x+cx, y, 1, 1, markCell(m.marks().cursor))
	}
}
