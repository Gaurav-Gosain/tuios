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
	// blank is a row of blank cells of the grid's width, made on the first
	// read that needs a whole row for a nil one and shared by later reads.
	// It is never written.
	blank uv.Line
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
}

// gridBlank is the cell CellAt returns for a column of a row that has not
// been written. It is shared by every grid and must never be written to:
// CellAt hands out a pointer for reading, and a write through it would show
// on every blank cell everywhere.
var gridBlank = uv.EmptyCell

func newGrid(width, height int) *grid {
	return &grid{rows: make([]uv.Line, height), ext: make([]int, height), width: width}
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
	g.ext[y] = g.width
	if g.rows[y] == nil {
		g.rows[y] = newBlankLine(g.width)
	}
	return g.rows[y]
}

// rowOrBlank returns row y for reading as a whole line, substituting the
// shared blank row for one that has not been written.
func (g *grid) rowOrBlank(y int) uv.Line {
	if row := g.rows[y]; row != nil {
		return row
	}
	if len(g.blank) != g.width {
		g.blank = newBlankLine(g.width)
	}
	return g.blank
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
	if g.rows[y] == nil {
		if isBlankFill(c) || x < 0 || x >= g.width {
			return
		}
		g.rows[y] = newBlankLine(g.width)
	}
	g.rows[y].Set(x, c)
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
		g.blank = nil
		g.width = width
		// Columns added on the right are blank, so only a narrower grid
		// has to pull the extents in.
		for y := range g.ext {
			g.ext[y] = min(g.ext[y], width)
		}
	}
	if height > len(g.rows) {
		g.ext = append(g.ext, make([]int, height-len(g.rows))...)
		g.rows = append(g.rows, make([]uv.Line, height-len(g.rows))...)
	} else if height < len(g.rows) {
		clear(g.rows[height:])
		g.rows = g.rows[:height]
		g.ext = g.ext[:height]
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
	slices.Reverse(g.ext[y:mid])
	slices.Reverse(g.ext[mid:end])
	slices.Reverse(g.ext[y:end])
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
func (g *grid) String() string {
	var b strings.Builder
	for y := range g.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(g.rowOrBlank(y).String())
	}
	return b.String()
}

// Render returns the grid as styled text, as uv.Buffer.Render does.
func (g *grid) Render() string {
	var b strings.Builder
	for y := range g.rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(g.rowOrBlank(y).Render())
	}
	return b.String()
}
