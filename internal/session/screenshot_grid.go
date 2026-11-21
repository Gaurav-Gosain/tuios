package session

import (
	"tuios.dev/shot"
	"tuios.dev/vt"
)

func (p *PTY) screenshotGrid(palette *shot.Palette, scrollbackRows int, cursor bool) *shot.Grid {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return nil
	}
	return gridOf(p.terminal, palette, scrollbackRows, cursor)
}

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
		line := vt.ClipHistoryRow(t.ScrollbackLine(held-scrollbackRows+i), cols)
		row := g.Cells[i]
		for x := 0; x < cols && x < len(line); x++ {
			cell := line[x]
			row[x] = shot.MakeCell(vt.CellText(cell.Content), cell.Width, cell.Style, cell.Link, palette)
		}
	}

	for y := range rows {
		row := g.Cells[scrollbackRows+y]
		for x := range cols {
			cell := t.CellAt(x, y)
			if cell == nil {
				continue
			}
			row[x] = shot.MakeCell(vt.CellText(cell.Content), cell.Width, cell.Style, cell.Link, palette)
		}
	}

	if cursor {
		pos := t.CursorPosition()
		g.ReverseCursor(pos.X, scrollbackRows+pos.Y)
	}
	return g
}
