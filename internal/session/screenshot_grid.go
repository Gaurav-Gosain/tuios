package session

import (
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Cell-grid capture for the screenshot verb.
//
// It reads the emulator straight into a shot.Grid rather than going through
// CaptureContent's ANSI string. Both would work, but the string route resolves
// colours to SGR and then asks the renderer to parse them back, which loses
// the colour kind: an indexed cell would come back as whatever RGB this
// process guessed, and --theme could no longer re-map it. Reading the cells is
// also shorter.

// screenshotGrid builds the render grid for a pane. scrollbackRows rows of
// history are prepended above the visible screen, bounded by what the pane
// actually holds. cursor draws the cursor cell as a block.
//
// It takes the same read lock GetTerminalState takes, and holds it for the
// walk only: the render happens after, off the lock.
func (p *PTY) screenshotGrid(palette *shot.Palette, scrollbackRows int, cursor bool) *shot.Grid {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil
	}
	return gridOf(p.terminal, palette, scrollbackRows, cursor)
}

// gridOf is screenshotGrid without the lock, so a test can drive it against a
// bare emulator.
func gridOf(t vt.Terminal, palette *shot.Palette, scrollbackRows int, cursor bool) *shot.Grid {
	if palette == nil {
		palette = shot.XTermPalette()
	}
	cols, rows := t.Width(), t.Height()
	if cols <= 0 || rows <= 0 {
		return nil
	}

	held := t.ScrollbackLen()
	if scrollbackRows < 0 || scrollbackRows > held {
		scrollbackRows = held
	}
	g := shot.NewGrid(cols, rows+scrollbackRows, palette.FG, palette.BG)

	for i := range scrollbackRows {
		line := t.ScrollbackLine(held - scrollbackRows + i)
		row := g.Cells[i]
		for x := 0; x < cols && x < len(line); x++ {
			cell := line[x]
			row[x] = shot.MakeCell(cell.Content, cell.Width, cell.Style, cell.Link, palette)
		}
	}

	for y := range rows {
		row := g.Cells[scrollbackRows+y]
		for x := range cols {
			cell := t.CellAt(x, y)
			if cell == nil {
				continue
			}
			row[x] = shot.MakeCell(cell.Content, cell.Width, cell.Style, cell.Link, palette)
		}
	}

	if cursor {
		pos := t.CursorPosition()
		g.ReverseCursor(pos.X, scrollbackRows+pos.Y)
	}
	return g
}

// gridFromCells builds a grid from an already-walked rectangle of cells, which
// is the shape a client-side region capture arrives in.
func gridFromCells(cells [][]*uv.Cell, palette *shot.Palette) *shot.Grid {
	if palette == nil {
		palette = shot.XTermPalette()
	}
	rows := len(cells)
	if rows == 0 {
		return nil
	}
	cols := 0
	for _, row := range cells {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return nil
	}
	g := shot.NewGrid(cols, rows, palette.FG, palette.BG)
	for y, row := range cells {
		for x, cell := range row {
			if cell == nil {
				continue
			}
			g.Cells[y][x] = shot.MakeCell(cell.Content, cell.Width, cell.Style, cell.Link, palette)
		}
	}
	return g
}
