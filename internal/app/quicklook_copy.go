package app

import (
	"strconv"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

// Copy mode in the preview. Inside a text preview, the copy_mode key starts
// the copy mode a pane has, over the text: the same motions, selections,
// search and yanks, run by the same code in internal/input. The preview is a
// terminal.CopyGrid for it: the rows of the text as the panel wraps them,
// the rows above the view as the history and the view as the screen.
//
// Copy mode needs every character on a row it can reach, so it wraps long
// lines while it runs. The text it moves over is what the preview has read;
// no more is read while it runs, so a row keeps its number.

// quickLookCopyRow is one row of the text: the bytes [at, end) of line.
type quickLookCopyRow struct {
	line, at, end int
	// more says the next row is more of the same line, so a selection that
	// crosses the edge copies no newline there.
	more bool
}

// quickLookCopy is copy mode over the preview's text.
type quickLookCopy struct {
	cm terminal.CopyMode
	// res is the result the rows were laid out from. Copy mode ends when
	// the preview shows another.
	res   *quickLookResult
	lines []string
	rows  []quickLookCopyRow
	// cells is each row as cells, built when copy mode first reads it.
	cells [][]uv.Cell
	// w and h are the code column's width and the rows of the view.
	w, h   int
	offset int
	// wrap is what the preview's wrap was before copy mode turned it on.
	wrap bool
}

// newQuickLookCopy lays the lines out as rows of width w.
func newQuickLookCopy(lines []string, w, h int) *quickLookCopy {
	q := &quickLookCopy{lines: lines, w: max(w, 1), h: max(h, 1)}
	for n, line := range lines {
		at := 0
		for {
			end, _ := quickLookWrapNext(line, at, q.w)
			q.rows = append(q.rows, quickLookCopyRow{line: n, at: at, end: end})
			if end >= len(line) {
				break
			}
			q.rows[len(q.rows)-1].more = true
			at = end
		}
	}
	q.cells = make([][]uv.Cell, len(q.rows))
	return q
}

// rowCells is row i as w cells: a cluster's cell, a blank cell after it for
// each column more it takes, and spaces to the edge.
func (q *quickLookCopy) rowCells(i int) []uv.Cell {
	if i < 0 || i >= len(q.rows) {
		return nil
	}
	if q.cells[i] != nil {
		return q.cells[i]
	}
	r := q.rows[i]
	s := q.lines[r.line][r.at:r.end]
	out := make([]uv.Cell, 0, q.w)
	for len(s) > 0 && len(out) < q.w {
		g, gw := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if g == "" {
			g = s[:1]
		}
		s = s[len(g):]
		gw = max(gw, 1)
		out = append(out, uv.Cell{Content: g, Width: gw})
		for k := 1; k < gw && len(out) < q.w; k++ {
			out = append(out, uv.Cell{})
		}
	}
	for len(out) < q.w {
		out = append(out, uv.Cell{Content: " ", Width: 1})
	}
	q.cells[i] = out
	return out
}

// top is the row at the top of the view.
func (q *quickLookCopy) top() int { return q.ScrollbackLen() - q.offset }

// screenRow is the row on line y of the view, the screen.
func (q *quickLookCopy) screenRow(y int) int { return q.ScrollbackLen() + y }

// Text is the copy grid's text, the preview itself.
func (q *quickLookCopy) Text() terminal.CopyText { return q }

// ScrollbackLen is the rows above the view when it is at the bottom.
func (q *quickLookCopy) ScrollbackLen() int { return max(len(q.rows)-q.h, 0) }

// ScrollbackLine is row i above the bottom view.
func (q *quickLookCopy) ScrollbackLine(i int) uv.Line { return q.rowCells(i) }

// ScrollbackText calls fn with the rows from to end-1 as text cells.
func (q *quickLookCopy) ScrollbackText(from, end int, fn func(index, width int, cells []vt.TextCell) bool) {
	var buf []vt.TextCell
	for i := max(from, 0); i < min(end, q.ScrollbackLen()); i++ {
		buf = buf[:0]
		for _, c := range q.rowCells(i) {
			buf = append(buf, vt.TextCell{Content: []byte(c.Content), Width: c.Width})
		}
		if !fn(i, q.w, buf) {
			return
		}
	}
}

// ScrollbackGeneration does not change: the rows are fixed while copy mode
// runs.
func (q *quickLookCopy) ScrollbackGeneration() uint64 { return 1 }

// CellAt is the cell at column x of line y of the bottom view.
func (q *quickLookCopy) CellAt(x, y int) *uv.Cell {
	cells := q.rowCells(q.screenRow(y))
	if x < 0 || x >= len(cells) {
		return nil
	}
	return &cells[x]
}

// Width is the code column's width.
func (q *quickLookCopy) Width() int { return q.w }

// Height is the view's rows.
func (q *quickLookCopy) Height() int { return q.h }

func (q *quickLookCopy) wraps(i int) (bool, bool) {
	if i < 0 || i >= len(q.rows) {
		return false, true
	}
	return q.rows[i].more, true
}

// RowSoftWrapped says line y of the bottom view goes on in the next row.
func (q *quickLookCopy) RowSoftWrapped(y int) (bool, bool) { return q.wraps(q.screenRow(y)) }

// ScrollbackSoftWrapped says row i goes on in the next row.
func (q *quickLookCopy) ScrollbackSoftWrapped(i int) (bool, bool) { return q.wraps(i) }

// RowPadded is false: a row ends where its last whole cluster does.
func (q *quickLookCopy) RowPadded(int) bool { return false }

// ScrollbackPadded is false, as RowPadded.
func (q *quickLookCopy) ScrollbackPadded(int) bool { return false }

// ScrollOffset is how many rows the view is above the bottom.
func (q *quickLookCopy) ScrollOffset() int { return q.offset }

// SetScrollOffset moves the view offset rows above the bottom.
func (q *quickLookCopy) SetScrollOffset(offset int) {
	q.offset = max(min(offset, q.ScrollbackLen()), 0)
}

// ViewHeight is the view's rows.
func (q *quickLookCopy) ViewHeight() int { return q.h }

// ContentHeight is the view's rows.
func (q *quickLookCopy) ContentHeight() int { return q.h }

// LastContentRow is the view's last row.
func (q *quickLookCopy) LastContentRow() int { return q.h - 1 }

// LastContentCol is the code column's last column.
func (q *quickLookCopy) LastContentCol() int { return q.w - 1 }

// QuickLookCopyStart starts copy mode over the text in the preview, with the
// cursor on the first row in view. It reports false when the keyboard is not
// inside a text preview, or before the text has been drawn once.
func (m *OS) QuickLookCopyStart() bool {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || !ql.inside || ql.copy != nil || r == nil || r.kind != "text" || len(r.lines) == 0 || ql.codeW <= 0 || ql.bodyRows <= 0 {
		return false
	}
	q := newQuickLookCopy(r.lines, ql.codeW, ql.bodyRows)
	q.res, q.wrap = r, ql.wrap
	line := max(min(ql.scroll, len(r.lines)-1), 0)
	top := 0
	for top < len(q.rows) && q.rows[top].line < line {
		top++
	}
	q.SetScrollOffset(q.ScrollbackLen() - top)
	q.cm = terminal.CopyMode{Active: true, State: terminal.CopyModeNormal, ScrollOffset: q.offset}
	q.cm.CursorY = max(top-q.top(), 0)
	ql.copy, ql.wrap = q, true
	return true
}

// QuickLookCopy is copy mode's state and the grid it moves over, or nil and
// nil when copy mode is off in the preview.
func (m *OS) QuickLookCopy() (*terminal.CopyMode, terminal.CopyGrid) {
	q := m.quickLook.copy
	if q == nil || !m.quickLook.open || m.quickLook.res != q.res {
		return nil, nil
	}
	return &q.cm, q
}

// QuickLookCopyEnd ends copy mode in the preview. The text stays where copy
// mode had it, and wrap goes back to what it was.
func (m *OS) QuickLookCopyEnd() {
	ql := &m.quickLook
	q := ql.copy
	if q == nil {
		return
	}
	ql.copy = nil
	ql.wrap = q.wrap
	if t := q.top(); t >= 0 && t < len(q.rows) {
		ql.scroll = q.rows[t].line
	}
}

// drawQuickLookCopy draws the text as copy mode lays it out, with the search
// matches, the selection and the cursor on it.
func (m *OS) drawQuickLookCopy(c *ckCanvas, pal overlay.Palette, dv *diffview.Theme, r *quickLookResult, q *quickLookCopy, codeX, gutter, bodyTop int) {
	ql := &m.quickLook
	top := q.top()
	for y := 0; y < q.h && top+y < len(q.rows); y++ {
		row := q.rows[top+y]
		if r.firstLine > 0 && row.at == 0 {
			num := strconv.Itoa(r.firstLine + row.line)
			c.text(1+gutter-len(num), bodyTop+y, 0, ckSeg{t: num, fg: pal.FgMute})
		}
		var sp []diffview.Span
		if row.line < len(r.spans) {
			sp = r.spans[row.line]
		}
		ql.rowSpans = quickLookRowSpans(ql.rowSpans, sp, row.at, row.end)
		line := q.lines[row.line]
		c.draw(codeX, bodyTop+y, q.w, dv.Code(line[row.at:row.end], ql.rowSpans, diffview.Range{}, diffview.Context, false, 0, q.w))
	}
	cm := &q.cm
	// A row on the screen of absolute row abs, or -1 when it is out of view.
	view := func(abs int) int {
		if y := abs - top; y >= 0 && y < q.h {
			return y
		}
		return -1
	}
	marks := m.marks()
	search, current := markCell(marks.search), markCell(marks.match)
	for i, sm := range cm.SearchMatches {
		if y := view(sm.Line); y >= 0 && sm.EndX > sm.StartX {
			fn := search
			if i == cm.CurrentMatch {
				fn = current
			}
			c.restyle(codeX+sm.StartX, bodyTop+y, min(sm.EndX, q.w)-sm.StartX, 1, fn)
		}
	}
	if cm.State.InVisual() {
		sel := markCell(marks.selection)
		start, end := cm.VisualStart, cm.VisualEnd
		if cm.State == terminal.CopyModeVisualBlock {
			x0, x1 := min(start.X, end.X), max(start.X, end.X)
			for abs := min(start.Y, end.Y); abs <= max(start.Y, end.Y); abs++ {
				if y := view(abs); y >= 0 {
					c.restyle(codeX+x0, bodyTop+y, x1-x0+1, 1, sel)
				}
			}
		} else {
			if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
				start, end = end, start
			}
			for abs := start.Y; abs <= end.Y; abs++ {
				y := view(abs)
				if y < 0 {
					continue
				}
				x0, x1 := 0, q.w-1
				if cm.State != terminal.CopyModeVisualLine {
					if abs == start.Y {
						x0 = start.X
					}
					if abs == end.Y {
						x1 = end.X
					}
				}
				c.restyle(codeX+x0, bodyTop+y, x1-x0+1, 1, sel)
			}
		}
	}
	if cm.CursorY >= 0 && cm.CursorY < q.h {
		c.restyle(codeX+cm.CursorX, bodyTop+cm.CursorY, 1, 1, markCell(marks.cursor))
	}
}

// markCell paints a cell with a mark style: its ground and ink where the style
// sets them, and bold.
func markCell(st lipgloss.Style) func(*uv.Cell) {
	bg, fg, bold := st.GetBackground(), st.GetForeground(), st.GetBold()
	_, noBg := bg.(lipgloss.NoColor)
	_, noFg := fg.(lipgloss.NoColor)
	return func(cell *uv.Cell) {
		if !noBg && bg != nil {
			cell.Style.Bg = bg
		}
		if !noFg && fg != nil {
			cell.Style.Fg = fg
		}
		if bold {
			cell.Style.Attrs |= uv.AttrBold
		}
	}
}
