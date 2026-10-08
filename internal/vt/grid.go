package vt

import (
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// grid is the cell storage of one screen: a slice of rows of uv.Cell.
//
// It replaces uv.RenderBuffer for two reasons. The first is that a row is
// not allocated until something is written on it. A uv.Cell is 112 bytes, so
// a 207x55 grid of them is 1.3 MB, and a uv.Buffer allocates every row the
// moment it is made: that was the whole cost of an empty pane, paid on both
// sides of the socket, for a shell prompt that uses two rows. Here a nil row
// reads as a row of blanks, and a write to it allocates it. A row that has
// been written stays allocated, so a pane costs the rows it has used and a
// flood never allocates a row it already has.
//
// The second is that uv.RenderBuffer tracks which cells changed, for a
// renderer that diffs frames. Nothing in tuios reads that: the app diffs its
// own composed frame. The tracking cost a cell comparison on every write and
// a touch of every row on every scroll, for nothing.
//
// Cell semantics are uv's own. A write goes through uv.Line.Set, which is
// what keeps a double-width character and its spacer consistent, and the
// line-shifting operations follow uv.Buffer's step for step, with one change:
// when the shift spans the full width, rows move by header instead of cell
// by cell.
type grid struct {
	// rows holds the lines. A nil row is a row of blank cells.
	rows []uv.Line
	// width is the number of columns; every non-nil row has this length.
	width int
	// ext holds, for each row, a column from which every cell to the right
	// is a plain blank (isBlankCell). It is 0 for a row nothing has written.
	// It is an upper bound, not the exact end of the text: a write raises
	// it, and only blanking the whole row lowers it.
	//
	// A scroll blanks the rows it brings in and packs the rows it pushes into
	// the scrollback, and both used to walk the whole row. A shell's output
	// is short lines on a wide screen, so almost all of that walk was over
	// blanks: at 207 columns a 10-character line cost 22 KB of cell reads and
	// writes. With the extent they stop where the text does.
	ext []int
	// wrap holds, for each row, whether the row's text carries on to the
	// next row because autowrap moved it there. A line that ended with a
	// newline, however long it was, leaves it false. It moves with the row
	// wherever the row moves. A blank or a fill that reaches the row's last
	// column clears it, since the text that wrapped is gone, and so do DCH
	// and ECH, which ghostty clears it on too.
	//
	// It is a set of rowFlag bits: rowWrapped is that flag, and rowPadded
	// says the wrap left the last column blank because a wide character did
	// not fit in it, so the blank is not text. A reflow drops that column.
	wrap []rowFlag
	// tail holds, for a row of a shell prompt that a reflow kept on one row
	// (freezePrompt), the cells past the width. It is nil for every other
	// row, and the whole slice is nil until a reflow first needs it. Any
	// write to the row drops its tail: the shell has repainted it. A row that
	// moves takes its tail with it, and one that goes into the history takes
	// it there.
	tail []uv.Line
	// prot holds, for each row, which of its cells DECSCA protected from a
	// selective erase. Protection is not part of a uv.Cell, so it is kept
	// beside the cells and moves wherever they move. It is nil until a guest
	// first prints a protected cell, which most never do, and a row's entry
	// is nil while the row has none. A write through SetCell leaves the cell
	// unprotected; the print path marks it again when the pen protects it.
	// A reflow drops it: a reflowed row is a new row.
	prot [][]bool
	// The windows a whole-screen scroll slides rows, ext, wrap, tail and
	// prot through. See scrollWindow.
	rowsWin rowWindow[uv.Line]
	extWin  rowWindow[int]
	wrapWin rowWindow[rowFlag]
	tailWin rowWindow[uv.Line]
	protWin rowWindow[[]bool]
}

// rowWindow keeps a table indexed by row (the row headers, extents, wrap
// flags, tails) as a window into a backing array twice its length, so that a
// scroll of the whole screen moves the window instead of every entry.
//
// Sliding the table up one row was a copy of every entry but the first, and
// for the row headers a copy with a write barrier per pointer: a pane printing
// as fast as it can scrolls once per line, and those copies were 11% of the
// daemon in a `yes` flood. Here the rows leaving the top are written once into
// the free slots past the window's end, the window starts n slots later, and
// only when it reaches the end of the backing array is it copied back to the
// start, once every len(table) scrolls.
//
// The table the window hands out has its capacity cut at its length, so an
// append reallocates rather than writing into the free slots. Every other
// change to the table (a resize, a reflow, cutting rows off the bottom) either
// keeps its start, which the window recognises, or gives it a new one, which
// the window adopts at the next scroll.
type rowWindow[T any] struct {
	base []T
	off  int
}

// scroll returns cur with its first n entries moved to its end, the rest
// moved up n places, as slices.Concat(cur[n:], cur[:n]) would but in place
// when it can.
func (w *rowWindow[T]) scroll(cur []T, n int) []T {
	h := len(cur)
	if n <= 0 || n >= h {
		return cur
	}
	if w.off+h > len(w.base) || &w.base[w.off] != &cur[0] {
		// A table the window did not hand out: start a backing array for it.
		w.base = make([]T, 2*h)
		copy(w.base, cur)
		w.off = 0
	} else if w.off+h+n > len(w.base) {
		// No room past the end: move the window back to the start, and
		// clear what it leaves behind so no row header is held twice.
		copy(w.base, w.base[w.off:w.off+h])
		clear(w.base[h:])
		w.off = 0
	}
	copy(w.base[w.off+h:w.off+h+n], w.base[w.off:w.off+n])
	clear(w.base[w.off : w.off+n])
	w.off += n
	return w.base[w.off : w.off+h : w.off+h]
}

// scrollWindow scrolls every row of the grid up n rows, the rows leaving the
// top coming back at the bottom with their extents, wrap flags and tails
// unchanged, for the caller to blank.
func (g *grid) scrollWindow(n int) {
	g.rows = g.rowsWin.scroll(g.rows, n)
	g.ext = g.extWin.scroll(g.ext, n)
	g.wrap = g.wrapWin.scroll(g.wrap, n)
	if g.tail != nil {
		g.tail = g.tailWin.scroll(g.tail, n)
	}
	if g.prot != nil {
		g.prot = g.protWin.scroll(g.prot, n)
	}
}

// Protected reports whether DECSCA protected the cell at x, y.
func (g *grid) Protected(x, y int) bool {
	if y < 0 || y >= len(g.prot) || x < 0 {
		return false
	}
	row := g.prot[y]
	return x < len(row) && row[x]
}

// setProtected marks n cells of row y from column x protected or not.
func (g *grid) setProtected(x, y, n int, on bool) {
	if y < 0 || y >= len(g.rows) {
		return
	}
	x0, x1 := max(x, 0), min(x+n, g.width)
	if x0 >= x1 {
		return
	}
	if !on {
		g.clearProtected(y, x0, x1)
		return
	}
	if g.prot == nil {
		g.prot = make([][]bool, len(g.rows))
	}
	if g.prot[y] == nil {
		g.prot[y] = make([]bool, g.width)
	}
	for i := x0; i < x1; i++ {
		g.prot[y][i] = true
	}
}

// clearProtected unprotects columns x0 to x1-1 of row y.
func (g *grid) clearProtected(y, x0, x1 int) {
	if y < 0 || y >= len(g.prot) || g.prot[y] == nil {
		return
	}
	row := g.prot[y]
	for i := max(x0, 0); i < x1 && i < len(row); i++ {
		row[i] = false
	}
}

// clearProtectedRows unprotects every cell of rows y to end-1.
func (g *grid) clearProtectedRows(y, end int) {
	if g.prot == nil {
		return
	}
	clear(g.prot[max(y, 0):min(end, len(g.prot))])
}

// shiftProtected moves the protection of n columns of row y from column src
// to column dst, as ICH and DCH move the cells. The caller clears the
// columns the shift blanked.
func (g *grid) shiftProtected(y, dst, src, n int) {
	if y < 0 || y >= len(g.prot) || g.prot[y] == nil || n <= 0 {
		return
	}
	copy(g.prot[y][dst:dst+n], g.prot[y][src:src+n])
}

// protectedRuns lists the protected cells as runs along each row.
func (g *grid) protectedRuns() []CellRun {
	var runs []CellRun
	for y, row := range g.prot {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			runs = append(runs, CellRun{X: start, Y: y, N: x - start})
		}
	}
	return runs
}

// restoreProtected replaces every cell's protection with the runs given,
// clipped to the grid.
func (g *grid) restoreProtected(runs []CellRun) {
	g.prot = nil
	for _, r := range runs {
		if r.N > 0 {
			g.setProtected(r.X, r.Y, min(r.N, g.width), true)
		}
	}
}

// rowFlag is what a row records about where its text ends.
type rowFlag uint8

const (
	// rowWrapped: the row's text carries on to the next row by autowrap.
	rowWrapped rowFlag = 1 << iota
	// rowPadded: the row wrapped early, before a double-width character
	// that did not fit in its last column, and that column is a blank the
	// guest never wrote.
	rowPadded
)

// wrapFlag is the flags of a row that does or does not wrap, with no other
// flag set.
func wrapFlag(wrapped bool) rowFlag {
	if wrapped {
		return rowWrapped
	}
	return 0
}

// rowTail returns row y's tail, or nil.
func (g *grid) rowTail(y int) uv.Line {
	if g.tail == nil || y < 0 || y >= len(g.tail) {
		return nil
	}
	return g.tail[y]
}

// setTail gives row y the tail t.
func (g *grid) setTail(y int, t uv.Line) {
	if g.tail == nil {
		g.tail = make([]uv.Line, len(g.rows))
	}
	g.tail[y] = t
}

// dropTail forgets row y's tail, because the row was written to.
func (g *grid) dropTail(y int) {
	if g.tail != nil && y >= 0 && y < len(g.tail) {
		g.tail[y] = nil
	}
}

// hasTail reports whether any row has a tail.
func (g *grid) hasTail() bool {
	for _, t := range g.tail {
		if t != nil {
			return true
		}
	}
	return false
}

// withTail is row y with its tail after it, for a reader that keeps a row
// whole, such as the scrollback. Without a tail it is the row itself.
func (g *grid) withTail(y int, row uv.Line) uv.Line {
	t := g.rowTail(y)
	if len(t) == 0 {
		return row
	}
	return append(row[:len(row):len(row)], t...)
}

// gridBlank is the cell CellAt returns for a column of a row that has not
// been written. It is shared by every grid and must never be written to:
// CellAt hands out a pointer for reading, and a write through it would show
// on every blank cell everywhere.
var gridBlank = uv.EmptyCell

func newGrid(width, height int) *grid {
	return &grid{rows: make([]uv.Line, height), ext: make([]int, height), wrap: make([]rowFlag, height), width: width}
}

// raiseExt records that row y may hold something other than a blank up to
// column end.
func (g *grid) raiseExt(y, end int) {
	if end > g.ext[y] {
		g.ext[y] = min(end, g.width)
	}
}

// Width returns the number of columns.
func (g *grid) Width() int { return g.width }

// Height returns the number of rows.
func (g *grid) Height() int { return len(g.rows) }

// Bounds returns the rectangle the grid covers, with its origin at (0, 0).
func (g *grid) Bounds() uv.Rectangle { return uv.Rect(0, 0, g.width, len(g.rows)) }

// CellAt returns the cell at x, y for reading, or nil when the position is
// off the grid. The pointer is into the grid's storage, or to the shared
// blank for a row that has not been written; callers must not write through
// it. Writes go through SetCell.
func (g *grid) CellAt(x, y int) *uv.Cell {
	if y < 0 || y >= len(g.rows) || x < 0 || x >= g.width {
		return nil
	}
	row := g.rows[y]
	if row == nil {
		return &gridBlank
	}
	return &row[x]
}

// Row returns row y as it is stored, which is nil when nothing has been
// written on it, or nil when y is off the grid.
func (g *grid) Row(y int) uv.Line {
	if y < 0 || y >= len(g.rows) {
		return nil
	}
	return g.rows[y]
}

// row returns row y for writing, allocating it if it has not been written.
// The caller may write any column, so the row's extent becomes its width.
func (g *grid) row(y int) uv.Line {
	g.dropTail(y)
	g.ext[y] = g.width
	if g.rows[y] == nil {
		g.rows[y] = newBlankLine(g.width)
	}
	return g.rows[y]
}

func newBlankLine(width int) uv.Line {
	line := make(uv.Line, width)
	for x := range line {
		line[x] = uv.EmptyCell
	}
	return line
}

// isBlankFill reports whether c is what a nil row already holds, so writing
// it there changes nothing.
func isBlankFill(c *uv.Cell) bool {
	return c == nil || isBlankCell(c)
}

// SetCell writes c at x, y, with uv.Line.Set's handling of double-width
// characters. A nil c is a blank.
func (g *grid) SetCell(x, y int, c *uv.Cell) {
	if y < 0 || y >= len(g.rows) {
		return
	}
	g.dropTail(y)
	if g.rows[y] == nil {
		if isBlankFill(c) || x < 0 || x >= g.width {
			return
		}
		g.rows[y] = newBlankLine(g.width)
	}
	g.rows[y].Set(x, c)
	if g.prot != nil {
		w := 1
		if c != nil {
			w = max(c.Width, 1)
		}
		g.clearProtected(y, x, x+w)
	}
	// A blank written over a wide character leaves its other half as a
	// styled space, but that half was already inside the extent the wide
	// character raised it to, so only a non-blank write can move it.
	if !isBlankFill(c) && x >= 0 {
		g.raiseExt(y, x+max(c.Width, 1))
	}
}

// Resize changes the grid to width columns and height rows. Rows added at
// the bottom start unwritten, columns added on the right start blank, and
// what falls outside the new size is dropped.
func (g *grid) Resize(width, height int) {
	if width != g.width {
		for y, row := range g.rows {
			if row == nil {
				continue
			}
			if width > len(row) {
				g.rows[y] = append(row, newBlankLine(width-len(row))...)
			} else {
				g.rows[y] = row[:width]
			}
		}
		g.width = width
		// Columns added on the right are blank, so only a narrower grid
		// has to pull the extents in.
		for y := range g.ext {
			g.ext[y] = min(g.ext[y], width)
		}
		// Rows are cut or padded, not reflowed, so a row that wrapped at
		// the old width does not wrap at the new one.
		clear(g.wrap)
		g.tail = nil
		for y, row := range g.prot {
			if row == nil {
				continue
			}
			if width > len(row) {
				g.prot[y] = append(row, make([]bool, width-len(row))...)
			} else {
				g.prot[y] = row[:width]
			}
		}
	}
	if height > len(g.rows) {
		g.ext = append(g.ext, make([]int, height-len(g.rows))...)
		g.wrap = append(g.wrap, make([]rowFlag, height-len(g.rows))...)
		if g.tail != nil {
			g.tail = append(g.tail, make([]uv.Line, height-len(g.rows))...)
		}
		if g.prot != nil {
			g.prot = append(g.prot, make([][]bool, height-len(g.rows))...)
		}
		g.rows = append(g.rows, make([]uv.Line, height-len(g.rows))...)
	} else if height < len(g.rows) {
		clear(g.rows[height:])
		g.rows = g.rows[:height]
		g.ext = g.ext[:height]
		g.wrap = g.wrap[:height]
		if g.tail != nil {
			clear(g.tail[height:])
			g.tail = g.tail[:height]
		}
		if g.prot != nil {
			clear(g.prot[height:])
			g.prot = g.prot[:height]
		}
	}
}

// Clear sets every cell to a blank, as uv.Buffer.Clear does: by assignment,
// without the wide-cell handling of Set, because every cell goes.
func (g *grid) Clear() {
	for y, row := range g.rows {
		for x := range row[:g.ext[y]] {
			row[x] = uv.EmptyCell
		}
		g.ext[y] = 0
	}
	clear(g.wrap)
	clear(g.tail)
	g.prot = nil
}

// SoftWrapped reports whether row y carries on to row y+1 by autowrap.
func (g *grid) SoftWrapped(y int) bool {
	return y >= 0 && y < len(g.wrap) && g.wrap[y]&rowWrapped != 0
}

// setSoftWrapped records whether row y carries on to row y+1 by autowrap.
func (g *grid) setSoftWrapped(y int, wrapped bool) {
	if y >= 0 && y < len(g.wrap) {
		// A new wrap: a padding flag from what the row held before does not
		// carry over.
		g.wrap[y] = wrapFlag(wrapped)
	}
}

// setPadded records that row y, which has just wrapped, wrapped early
// because a wide character did not fit in its last column.
func (g *grid) setPadded(y int) {
	if y >= 0 && y < len(g.wrap) {
		g.wrap[y] |= rowPadded
	}
}

// ClearArea sets every cell in area to a blank.
func (g *grid) ClearArea(area uv.Rectangle) {
	g.FillArea(nil, area)
}

// FillArea writes c to every cell in area, stepping by c's width as
// uv.Buffer.FillArea does.
//
// The result is that of a uv.Line.Set on every cell of the span in turn, but
// only the two end cells go through Set. Set's wide-character repair is the
// only thing that reaches outside the cell it writes, and for a cell inside
// the span whatever it does lands inside the span, which the fill overwrites.
// So Set on the first cell (for a wide character the span cuts on the left)
// and on the last (for one it cuts on the right), then a plain store of every
// cell of the span, leaves the row as the sequence of Sets does. A blank fill
// also stops at the row's extent, past which every cell is blank already.
// ED and EL reach here for every frame most full-screen programs draw.
func (g *grid) FillArea(c *uv.Cell, area uv.Rectangle) {
	// A fill that reaches the last column replaces the text that wrapped.
	if area.Max.X >= g.width && area.Min.X < g.width {
		for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.wrap); y++ {
			g.wrap[y] = 0
		}
	}
	for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.tail); y++ {
		g.tail[y] = nil
	}
	for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.prot); y++ {
		g.clearProtected(y, area.Min.X, area.Max.X)
	}
	blank := isBlankFill(c)
	if c != nil && c.Width > 1 {
		// A wide fill steps by its width. No emulator path fills with one.
		for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.rows); y++ {
			if g.rows[y] == nil && blank {
				continue
			}
			for x := area.Min.X; x < area.Max.X; x += c.Width {
				g.SetCell(x, y, c)
			}
		}
		return
	}
	fill := uv.EmptyCell
	if c != nil {
		fill = *c
	}
	x0 := max(area.Min.X, 0)
	for y := max(area.Min.Y, 0); y < area.Max.Y && y < len(g.rows); y++ {
		x1 := min(area.Max.X, g.width)
		if blank {
			x1 = min(x1, g.ext[y])
		}
		if x0 >= x1 {
			continue
		}
		if g.rows[y] == nil {
			g.rows[y] = newBlankLine(g.width)
		}
		row := g.rows[y]
		row.Set(x0, c)
		row.Set(x1-1, c)
		for x := x0; x < x1; x++ {
			row[x] = fill
		}
		switch {
		case !blank:
			g.raiseExt(y, x1)
		case x1 == g.ext[y]:
			// The row is blank from x0 on. A wide character the first Set
			// cut is left of x0, so it does not move the extent.
			g.ext[y] = x0
		}
	}
}

// fullWidth reports whether area spans every column, which is when rows can
// move by header.
func (g *grid) fullWidth(area uv.Rectangle) bool {
	return area.Min.X <= 0 && area.Max.X >= g.width
}

// blankRows writes c across every column of rows y to end-1, in place where
// the row exists and by leaving it nil where it does not and c is a blank.
// A blank fill writes only up to each row's extent: past it the row is blank
// already.
func (g *grid) blankRows(y, end int, c *uv.Cell) {
	clear(g.wrap[y:end])
	if g.tail != nil {
		clear(g.tail[y:end])
	}
	g.clearProtectedRows(y, end)
	if isBlankFill(c) {
		for i := y; i < end; i++ {
			row := g.rows[i]
			for x := range row[:g.ext[i]] {
				row[x] = uv.EmptyCell
			}
			g.ext[i] = 0
		}
		return
	}
	if c.Width > 1 {
		// uv.Buffer's line shifts fill with a cell-by-cell Set that does
		// not step by the cell's width, and the result of that for a wide
		// cell is what this has to reproduce. No caller fills with one.
		for i := y; i < end; i++ {
			for x := 0; x < g.width; x++ {
				g.SetCell(x, i, c)
			}
		}
		return
	}
	for i := y; i < end; i++ {
		row := g.row(i)
		for x := range row {
			row[x] = *c
		}
	}
}

// InsertLineArea inserts n blank lines at row y within area, pushing the
// rows below it down and the last n rows of the area off it. It follows
// uv.Buffer.InsertLineArea, moving rows by header when the area spans the
// full width.
func (g *grid) InsertLineArea(y, n int, c *uv.Cell, area uv.Rectangle) {
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= len(g.rows) {
		return
	}
	if y+n > area.Max.Y {
		n = area.Max.Y - y
	}
	end := min(area.Max.Y, len(g.rows))
	if y+n > end {
		n = end - y
	}

	if g.fullWidth(area) {
		var scratch [16]uv.Line
		var dropped []uv.Line
		if n <= len(scratch) {
			dropped = scratch[:n]
		} else {
			dropped = make([]uv.Line, n)
		}
		copy(dropped, g.rows[end-n:end])
		copy(g.rows[y+n:end], g.rows[y:end-n])
		copy(g.rows[y:y+n], dropped)
		g.rotateExt(y, end, end-n)
		g.blankRows(y, y+n, c)
		return
	}

	if !g.anyRow(y, end) && isBlankFill(c) {
		return
	}
	for i := y; i < end; i++ {
		g.row(i)
	}
	for i := end - 1; i >= y+n; i-- {
		for x := area.Min.X; x < area.Max.X; x++ {
			g.rows[i][x] = g.rows[i-n][x]
		}
		g.moveProtected(i, i-n, area.Min.X, area.Max.X)
	}
	for i := y; i < y+n; i++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			g.SetCell(x, i, c)
		}
	}
}

// DeleteLineArea deletes n lines at row y within area, pulling the rows
// below it up and filling the bottom of the area with blanks. It follows
// uv.Buffer.DeleteLineArea, moving rows by header when the area spans the
// full width.
func (g *grid) DeleteLineArea(y, n int, c *uv.Cell, area uv.Rectangle) {
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= len(g.rows) {
		return
	}
	end := min(area.Max.Y, len(g.rows))
	if n > end-y {
		n = end - y
	}

	if g.fullWidth(area) {
		var scratch [16]uv.Line
		var dropped []uv.Line
		if n <= len(scratch) {
			dropped = scratch[:n]
		} else {
			dropped = make([]uv.Line, n)
		}
		copy(dropped, g.rows[y:y+n])
		copy(g.rows[y:end-n], g.rows[y+n:end])
		copy(g.rows[end-n:end], dropped)
		g.rotateExt(y, end, y+n)
		g.blankRows(end-n, end, c)
		return
	}

	if !g.anyRow(y, end) && isBlankFill(c) {
		return
	}
	for i := y; i < end; i++ {
		g.row(i)
	}
	for dst := y; dst < end-n; dst++ {
		src := dst + n
		for x := area.Min.X; x < area.Max.X; x++ {
			g.rows[dst][x] = g.rows[src][x]
		}
		g.moveProtected(dst, src, area.Min.X, area.Max.X)
	}
	for i := end - n; i < end; i++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			g.SetCell(x, i, c)
		}
	}
}

// rotateExt moves the extents of rows y to end-1 the way a full-width line
// shift moved the rows: rotated left so the extent at mid comes first. The
// rows about to be blanked keep the extents they carried, which is what
// lets blankRows stop at the text they held.
func (g *grid) rotateExt(y, end, mid int) {
	rotateLeft(g.ext[y:end], mid-y)
	// The wrap flags travel with their rows the same way, and so do tails.
	rotateLeft(g.wrap[y:end], mid-y)
	if g.tail != nil {
		rotateLeft(g.tail[y:end], mid-y)
	}
	if g.prot != nil {
		rotateLeft(g.prot[y:end], mid-y)
	}
}

// moveProtected copies the protection of columns x0 to x1-1 from row src to
// row dst, as a line move inside side margins copies the cells.
func (g *grid) moveProtected(dst, src, x0, x1 int) {
	if g.prot == nil {
		return
	}
	if g.prot[src] == nil {
		g.clearProtected(dst, x0, x1)
		return
	}
	if g.prot[dst] == nil {
		g.prot[dst] = make([]bool, g.width)
	}
	copy(g.prot[dst][x0:x1], g.prot[src][x0:x1])
}

// rotateLeft moves s[k:] to the front of s and s[:k] to the back.
//
// A scroll moves one row at a time almost always, from either end, so the
// short side goes through a small stack buffer and the rest is one copy.
// Three reversals touch every element twice and cost about 9% of the
// process under a `yes` flood. A long rotation on both sides, such as a
// large CSI S inside a region, still takes the reversals.
func rotateLeft[T any](s []T, k int) {
	n := len(s)
	if k <= 0 || k >= n {
		return
	}
	var buf [16]T
	switch {
	case k <= len(buf):
		copy(buf[:k], s[:k])
		copy(s, s[k:])
		copy(s[n-k:], buf[:k])
	case n-k <= len(buf):
		m := n - k
		copy(buf[:m], s[k:])
		copy(s[m:], s[:k])
		copy(s, buf[:m])
	default:
		slices.Reverse(s[:k])
		slices.Reverse(s[k:])
		slices.Reverse(s)
	}
}

// anyRow reports whether any of rows y to end-1 has been written.
func (g *grid) anyRow(y, end int) bool {
	for i := y; i < end; i++ {
		if g.rows[i] != nil {
			return true
		}
	}
	return false
}

// String returns the text of the grid, as uv.Buffer.String does.
//
// It runs under a read lock, alongside other readers, so it must not write to
// the grid. A row that has not been written is all blanks, and a line's text
// drops trailing blanks, so its text is empty.
func (g *grid) String() string {
	var b strings.Builder
	for y, row := range g.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		if row != nil {
			b.WriteString(row.String())
		}
	}
	return b.String()
}

// Render returns the grid as styled text, one line per row, with a cluster
// break between neighbouring cells that would otherwise re-parse as one
// cluster (see Emulator.Render). Both backends render through here: the
// ghostty one used uv.Line.Render, which builds a string per row and
// allocates for every style change, and does not break clusters.
func (g *grid) Render() string {
	var b strings.Builder
	for y, line := range g.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		if line == nil {
			// A row nothing has written renders as the blanks it holds.
			for range g.width {
				b.WriteByte(' ')
			}
			continue
		}
		renderRowBreakingClusters(&b, line)
	}
	return b.String()
}
