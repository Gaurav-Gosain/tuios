package vt

import uv "github.com/charmbracelet/ultraviolet"

// Buffer is a terminal cell buffer.
type Buffer struct {
	uv.Buffer
}

// InsertLine inserts n lines at the given line position, with the given
// optional cell, within the specified rectangles. If no rectangles are
// specified, it inserts lines in the entire buffer. Only cells within the
// rectangle's horizontal bounds are affected. Lines are pushed out of the
// rectangle bounds and lost. This follows terminal [ansi.IL] behavior.
// It returns the pushed out lines.
func (b *Buffer) InsertLine(y, n int, c *uv.Cell) {
	b.InsertLineRect(y, n, c, b.Bounds())
}

// InsertLineRect inserts new lines at the given line position, with the
// given optional cell, within the rectangle bounds. Only cells within the
// rectangle's horizontal bounds are affected. Lines are pushed out of the
// rectangle bounds and lost. This follows terminal [ansi.IL] behavior.
func (b *Buffer) InsertLineRect(y, n int, c *uv.Cell, rect uv.Rectangle) {
	if n <= 0 || y < rect.Min.Y || y >= rect.Max.Y || y >= b.Height() {
		return
	}

	// Limit number of lines to insert to available space
	if y+n > rect.Max.Y {
		n = rect.Max.Y - y
	}

	// Move existing lines down within the bounds
	for i := rect.Max.Y - 1; i >= y+n; i-- {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			// We don't need to clone c here because we're just moving lines down.
			b.Lines[i][x] = b.Lines[i-n][x]
		}
	}

	// Clear the newly inserted lines within bounds
	for i := y; i < y+n; i++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			b.SetCell(x, i, c)
		}
	}
}

// DeleteLineRect deletes lines at the given line position, with the given
// optional cell, within the rectangle bounds. Only cells within the
// rectangle's bounds are affected. Lines are shifted up within the bounds and
// new blank lines are created at the bottom. This follows terminal [ansi.DL]
// behavior.
func (b *Buffer) DeleteLineRect(y, n int, c *uv.Cell, rect uv.Rectangle) {
	if n <= 0 || y < rect.Min.Y || y >= rect.Max.Y || y >= b.Height() {
		return
	}

	// Limit deletion count to available space in scroll region
	if n > rect.Max.Y-y {
		n = rect.Max.Y - y
	}

	// Shift cells up within the bounds
	for dst := y; dst < rect.Max.Y-n; dst++ {
		src := dst + n
		for x := rect.Min.X; x < rect.Max.X; x++ {
			// We don't need to clone c here because we're just moving cells up.
			b.Lines[dst][x] = b.Lines[src][x]
		}
	}

	// Fill the bottom n lines with blank cells
	for i := rect.Max.Y - n; i < rect.Max.Y; i++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			b.SetCell(x, i, c)
		}
	}
}

// DeleteLine deletes n lines at the given line position, with the given
// optional cell, within the specified rectangles. If no rectangles are
// specified, it deletes lines in the entire buffer.
func (b *Buffer) DeleteLine(y, n int, c *uv.Cell) {
	b.DeleteLineRect(y, n, c, b.Bounds())
}

// InsertCell inserts new cells at the given position, with the given optional
// cell, within the specified rectangles. If no rectangles are specified, it
// inserts cells in the entire buffer. This follows terminal [ansi.ICH]
// behavior.
func (b *Buffer) InsertCell(x, y, n int, c *uv.Cell) {
	b.InsertCellRect(x, y, n, c, b.Bounds())
}

// InsertCellRect inserts new cells at the given position, with the given
// optional cell, within the rectangle bounds. Only cells within the
// rectangle's bounds are affected, following terminal [ansi.ICH] behavior.
func (b *Buffer) InsertCellRect(x, y, n int, c *uv.Cell, rect uv.Rectangle) {
	if n <= 0 || y < rect.Min.Y || y >= rect.Max.Y || y >= b.Height() ||
		x < rect.Min.X || x >= rect.Max.X || x >= b.Width() {
		return
	}

	// Limit number of cells to insert to available space
	if x+n > rect.Max.X {
		n = rect.Max.X - x
	}

	// Move existing cells within rectangle bounds to the right
	for i := rect.Max.X - 1; i >= x+n && i-n >= rect.Min.X; i-- {
		// We don't need to clone c here because we're just moving cells to the
		// right.
		// b.lines[y][i] = b.lines[y][i-n]
		b.Lines[y][i] = b.Lines[y][i-n]
	}

	// Clear the newly inserted cells within rectangle bounds
	for i := x; i < x+n && i < rect.Max.X; i++ {
		b.SetCell(i, y, c)
	}
}

// DeleteCell deletes cells at the given position, with the given optional
// cell, within the specified rectangles. If no rectangles are specified, it
// deletes cells in the entire buffer. This follows terminal [ansi.DCH]
// behavior.
func (b *Buffer) DeleteCell(x, y, n int, c *uv.Cell) {
	b.DeleteCellRect(x, y, n, c, b.Bounds())
}

// DeleteCellRect deletes cells at the given position, with the given
// optional cell, within the rectangle bounds. Only cells within the
// rectangle's bounds are affected, following terminal [ansi.DCH] behavior.
func (b *Buffer) DeleteCellRect(x, y, n int, c *uv.Cell, rect uv.Rectangle) {
	if n <= 0 || y < rect.Min.Y || y >= rect.Max.Y || y >= b.Height() ||
		x < rect.Min.X || x >= rect.Max.X || x >= b.Width() {
		return
	}

	// Calculate how many positions we can actually delete
	remainingCells := rect.Max.X - x
	if n > remainingCells {
		n = remainingCells
	}

	// Shift the remaining cells to the left
	for i := x; i < rect.Max.X-n; i++ {
		if i+n < rect.Max.X {
			// We don't need to clone c here because we're just moving cells to
			// the left.
			// b.lines[y][i] = b.lines[y][i+n]
			b.Lines[y][i] = b.Lines[y][i+n]
		}
	}

	// Fill the vacated positions with the given cell
	for i := rect.Max.X - n; i < rect.Max.X; i++ {
		b.SetCell(i, y, c)
	}
}
