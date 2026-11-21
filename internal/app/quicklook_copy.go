package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

type quickLookCopyRow struct {
	line, at, end int
	more          bool
}

type quickLookCopy struct {
	cm     terminal.CopyMode
	res    *quickLookResult
	lines  []string
	rows   []quickLookCopyRow
	cells  [][]uv.Cell
	w, h   int
	offset int
	wrap   bool
	x0, y0 int
	md     []ckCopyRow
}

func newQuickLookCopy(lines []string, w, h int, cut bool) *quickLookCopy {
	q := &quickLookCopy{lines: lines, w: max(w, 1), h: max(h, 1)}
	for n, line := range lines {
		if cut {
			q.rows = append(q.rows, quickLookCopyRow{line: n, end: quickLookCutEnd(line, q.w)})
			continue
		}
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

func newQuickLookMDCopy(rows []ckCopyRow, w, h int) *quickLookCopy {
	q := &quickLookCopy{md: rows, w: max(w, 1), h: max(h, 1)}
	q.rows = make([]quickLookCopyRow, len(rows))
	for i := range q.rows {
		q.rows[i] = quickLookCopyRow{line: i, more: rows[i].more}
	}
	return q
}

func (q *quickLookCopy) rowCells(i int) []uv.Cell {
	if i < 0 || i >= len(q.rows) {
		return nil
	}
	if q.md != nil {
		return q.md[i].cells
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

func (q *quickLookCopy) top() int { return q.ScrollbackLen() - q.offset }

func (q *quickLookCopy) screenRow(y int) int { return q.ScrollbackLen() + y }

func (q *quickLookCopy) Text() terminal.CopyText { return q }

func (q *quickLookCopy) ScrollbackLen() int { return max(len(q.rows)-q.h, 0) }

func (q *quickLookCopy) ScrollbackLine(i int) uv.Line { return q.rowCells(i) }

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

func (q *quickLookCopy) ScrollbackGeneration() uint64 { return 1 }

func (q *quickLookCopy) CellAt(x, y int) *uv.Cell {
	cells := q.rowCells(q.screenRow(y))
	if x < 0 || x >= len(cells) {
		return nil
	}
	return &cells[x]
}

func (q *quickLookCopy) Width() int { return q.w }

func (q *quickLookCopy) Height() int { return q.h }

func (q *quickLookCopy) wraps(i int) (bool, bool) {
	if i < 0 || i >= len(q.rows) {
		return false, true
	}
	return q.rows[i].more, true
}

func (q *quickLookCopy) RowSoftWrapped(y int) (bool, bool) { return q.wraps(q.screenRow(y)) }

func (q *quickLookCopy) ScrollbackSoftWrapped(i int) (bool, bool) { return q.wraps(i) }

func (q *quickLookCopy) RowPadded(int) bool { return false }

func (q *quickLookCopy) ScrollbackPadded(int) bool { return false }

func (q *quickLookCopy) ScrollOffset() int { return q.offset }

func (q *quickLookCopy) SetScrollOffset(offset int) {
	q.offset = max(min(offset, q.ScrollbackLen()), 0)
}

func (q *quickLookCopy) ViewHeight() int { return q.h }

func (q *quickLookCopy) ContentHeight() int { return q.h }

func (q *quickLookCopy) LastContentRow() int { return q.h - 1 }

func (q *quickLookCopy) LastContentCol() int { return q.w - 1 }

// QuickLookCopyStart starts copy mode over the text in the preview, with the
// cursor on the first row in view. It reports false when the keyboard is not
// inside a text preview, or before the text has been drawn once.
func (m *OS) QuickLookCopyStart() bool {
	return m.quickLookCopyBegin(false)
}

func (m *OS) quickLookCopyBegin(implicit bool) bool {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || ql.copy != nil || r == nil || ql.textW <= 0 || ql.bodyRows <= 0 {
		return false
	}
	var q *quickLookCopy
	switch {
	case r.kind == "text" && len(r.lines) > 0 && (implicit || ql.inside):
		q = newQuickLookCopy(r.lines, ql.textW, ql.bodyRows, implicit && !ql.wrap)
	case r.kind == "markdown" && implicit && len(ql.mdLines) > 0:
		rows := ckCopyOf(ql.look, ql.mdLines, nil, ql.textW)
		for i, ln := range ql.mdLines {
			if strings.HasPrefix(ln.hit, "qimg:") {
				for x := range rows[i].cells {
					rows[i].cells[x] = uv.Cell{Content: " ", Width: 1}
				}
			}
		}
		q = newQuickLookMDCopy(rows, ql.textW, ql.bodyRows)
	default:
		return false
	}
	q.res, q.wrap = r, ql.wrap
	q.x0, q.y0 = ql.textX, ql.textY
	line := max(min(ql.scroll, len(q.rows)-1), 0)
	if q.md == nil {
		line = max(min(ql.scroll, len(r.lines)-1), 0)
	}
	top := 0
	for top < len(q.rows) && q.rows[top].line < line {
		top++
	}
	q.SetScrollOffset(q.ScrollbackLen() - top)
	q.cm = terminal.CopyMode{Active: true, State: terminal.CopyModeNormal, ScrollOffset: q.offset, Implicit: implicit}
	q.cm.CursorY = max(top-q.top(), 0)
	ql.copy = q
	if !implicit {
		ql.wrap = true
	}
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

func (m *OS) drawQuickLookCopy(c *ckCanvas, pal overlay.Palette, dv *diffview.Theme, r *quickLookResult, q *quickLookCopy, codeX, gutter, bodyTop int) {
	ql := &m.quickLook
	q.x0, q.y0 = codeX, bodyTop
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
	drawCopyMarks(c, m.marks(), &q.cm, codeX, bodyTop, q.w, q.h, top)
}

func (m *OS) drawQuickLookMDCopy(c *ckCanvas, q *quickLookCopy, x, bodyTop int) {
	q.x0, q.y0 = x, bodyTop
	lines := m.quickLook.mdLines
	top := q.top()
	for y := 0; y < q.h && top+y < len(lines); y++ {
		drawCkLineAt(c, x, bodyTop+y, q.w, lines[top+y])
	}
	drawCopyMarks(c, m.marks(), &q.cm, x, bodyTop, q.w, q.h, top)
	m.quickLookMDMarks(lines, top, q.h, bodyTop)
}

func drawCopyMarks(c *ckCanvas, marks *markStyles, cm *terminal.CopyMode, x0, y0, w, h, first int) {
	view := func(abs int) int {
		if y := abs - first; y >= 0 && y < h {
			return y
		}
		return -1
	}
	search, current := markCell(marks.search), markCell(marks.match)
	for i, sm := range cm.SearchMatches {
		if y := view(sm.Line); y >= 0 && sm.EndX > sm.StartX {
			fn := search
			if i == cm.CurrentMatch {
				fn = current
			}
			c.restyle(x0+sm.StartX, y0+y, min(sm.EndX, w)-sm.StartX, 1, fn)
		}
	}
	if cm.State.InVisual() {
		sel := markCell(marks.selection)
		start, end := cm.VisualStart, cm.VisualEnd
		if cm.State == terminal.CopyModeVisualBlock {
			x1, x2 := min(start.X, end.X), max(start.X, end.X)
			for abs := min(start.Y, end.Y); abs <= max(start.Y, end.Y); abs++ {
				if y := view(abs); y >= 0 {
					c.restyle(x0+x1, y0+y, x2-x1+1, 1, sel)
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
				x1, x2 := 0, w-1
				if cm.State != terminal.CopyModeVisualLine {
					if abs == start.Y {
						x1 = start.X
					}
					if abs == end.Y {
						x2 = end.X
					}
				}
				c.restyle(x0+x1, y0+y, x2-x1+1, 1, sel)
			}
		}
	}
	if cm.CursorY >= 0 && cm.CursorY < h {
		c.restyle(x0+cm.CursorX, y0+cm.CursorY, 1, 1, markCell(marks.cursor))
	}
}

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
