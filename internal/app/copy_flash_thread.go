package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// The copy sweep over the agents view's thread.
//
// The thread copies text three ways: y in copy mode, a mouse selection, and a
// code block's copy. Each one starts the sweep a pane copy starts, with the
// same clock, band and setting (copy_flash.go). Only the region differs: it
// is written down in the thread's own rows, not in screen cells, because the
// view can move between the copy and the next frame. Copy mode ends at a y,
// and the thread is then drawn from where it follows, not from where copy
// mode had scrolled it. Each frame maps the rows it shows to the screen, so
// the light stays on the text that was copied and a row out of view is
// left out.

// copyFlashSpan is the columns lo to hi of one thread row that a copy took.
// lo > hi when it took none.
type copyFlashSpan struct{ lo, hi int }

// CockpitCopyFlash starts the sweep over the selection of copy mode over the
// thread, from start to end: whole rows for line, the columns of start and
// end on each row for block. Input calls it for a yank, before copy mode
// ends and its rows go.
func (m *OS) CockpitCopyFlash(start, end terminal.Position, line, block bool) {
	q := m.threadCopy()
	if q == nil {
		return
	}
	top, spans := ckCopySpans(q.rows, q.w, start, end, line, block)
	m.noteThreadCopyFlash(q.key, top, spans)
}

// ckCopySpans is the cells of rows a selection from start to end takes, as
// the yank takes them (ckCopyRowsText), cut to the text on each row: the
// chrome copy mode leaves blank is not copied, so the light does not cross
// it. top is the first row.
func ckCopySpans(rows []ckCopyRow, w int, start, end terminal.Position, line, block bool) (int, []copyFlashSpan) {
	if start.Y > end.Y || start.Y == end.Y && start.X > end.X {
		start, end = end, start
	}
	x0, x1 := min(start.X, end.X), max(start.X, end.X)
	top := max(start.Y, 0)
	var spans []copyFlashSpan
	for y := top; y <= min(end.Y, len(rows)-1); y++ {
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
		first, last := ckCellsTextSpan(rows[y].cells)
		if !block {
			a = max(a, first)
		}
		spans = append(spans, copyFlashSpan{lo: a, hi: min(b, last)})
	}
	return top, spans
}

// ckCellsTextSpan is the first and last column of cells that hold text, a
// wide character counted to its last column. last < first for a blank row.
func ckCellsTextSpan(cells []uv.Cell) (first, last int) {
	first, last = len(cells), -1
	for x := range cells {
		c := &cells[x]
		if c.Width == 0 || c.Content == "" || c.Content == " " {
			continue
		}
		first = min(first, x)
		last = max(last, x+c.Width-1)
	}
	return first, last
}

// noteCodeCopyFlash starts the sweep over code block key of t: the block's
// box, from its header to its last row, across its ground.
func (m *OS) noteCodeCopyFlash(t *agentThread, key string) {
	if t == nil || len(t.lines) != len(t.body) || t.viewW <= 0 {
		return
	}
	top := -1
	var spans []copyFlashSpan
	for i, ln := range t.lines {
		if ln.code != key {
			if top >= 0 {
				break
			}
			continue
		}
		if top < 0 {
			top = i
		}
		spans = append(spans, copyFlashSpan{lo: ln.bgAt, hi: t.viewW - 1})
	}
	if top < 0 {
		return
	}
	m.noteThreadCopyFlash(t.key, top, spans)
}

// noteThreadCopyFlash starts the sweep over spans of the thread with this
// key, from row top. A copy of nothing but blank rows starts none.
func (m *OS) noteThreadCopyFlash(key string, top int, spans []copyFlashSpan) {
	lit := false
	for _, s := range spans {
		lit = lit || s.lo <= s.hi
	}
	if !lit {
		return
	}
	if m.noteCopyFlash(copyFlash{Thread: key, Start: terminal.Position{Y: top}, Spans: spans}) {
		m.cockpitTouch()
	}
}

// drawThreadCopyFlash draws the sweep over thread t, when one runs over it:
// n rows of the thread from row first are drawn w cells wide from canvas
// cell x, y0. Only the cells it shows are lit, and the band is sized to
// them. A copy wholly out of view ends the sweep, which would otherwise keep
// the motion clock running to draw nothing.
func (m *OS) drawThreadCopyFlash(c *ckCanvas, t *agentThread, x, y0, w, first, n int) {
	f := m.copyFlash
	if t == nil || f == nil || f.Thread != t.key || w <= 0 || n <= 0 {
		return
	}
	progress, ok := m.copyFlashClock()
	if !ok {
		return
	}
	grid := pool.GetHighlightGrid()
	defer pool.PutHighlightGrid(grid)
	grid.Init(n, w)
	for i, s := range f.Spans {
		row := f.Start.Y + i - first
		if row < 0 || row >= n {
			continue
		}
		for col := max(s.lo, 0); col <= s.hi && col < w; col++ {
			grid.Set(row, col)
		}
	}
	band, ok := m.copyFlashLight(grid, n, w, progress)
	if !ok {
		m.copyFlash = nil
		return
	}
	canvas := m.cockpitLook().canvas
	for row := range n {
		for col := range w {
			if !grid.Get(row, col) {
				continue
			}
			cell := c.buf.CellAt(x+col, y0+row)
			if cell == nil || cell.Width == 0 {
				continue
			}
			bg := cell.Style.Bg
			if bg == nil {
				bg = canvas
			}
			if g, lit := band.ground(col, row, bg); lit {
				cell.Style.Bg = g
			}
		}
	}
}
