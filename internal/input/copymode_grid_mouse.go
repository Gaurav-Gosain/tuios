package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func gridPoint(cm *terminal.CopyMode, grid terminal.CopyGrid, x, y, rows int) int {
	dir := 0
	off := cm.ScrollOffset
	switch {
	case rows <= 0:
		return 0
	case y < 0:
		dir, off, y = -1, min(off+1, grid.ScrollbackLen()), 0
	case y >= rows:
		dir, off, y = 1, max(off-1, 0), rows-1
	}
	if off != cm.ScrollOffset {
		cm.ScrollOffset = off
		grid.SetScrollOffset(off)
	}
	cm.CursorY = y
	cm.CursorX = min(max(x, 0), grid.LastContentCol())
	for cm.CursorX > 0 {
		cell := getCellAtCursor(cm, grid)
		if cell == nil || cell.Width > 0 {
			break
		}
		cm.CursorX--
	}
	return dir
}

func gridPress(cm *terminal.CopyMode, grid terminal.CopyGrid, x, y, rows, clicks int, s *config.Settings) {
	if cm.State.InVisual() {
		cm.State = terminal.CopyModeNormal
	}
	gridPoint(cm, grid, x, y, rows)
	switch clicks {
	case 2:
		gridSelectWord(cm, grid, s)
	case 3:
		enterVisualLine(cm, grid)
	}
}

func gridDrag(cm *terminal.CopyMode, grid terminal.CopyGrid, x, y, rows int) int {
	if !cm.State.InVisual() {
		enterVisualChar(cm, grid)
	}
	dir := gridPoint(cm, grid, x, y, rows)
	updateVisualEnd(cm, grid)
	return dir
}

func gridSelectWord(cm *terminal.CopyMode, grid terminal.CopyGrid, s *config.Settings) {
	cell := getCellAtCursor(cm, grid)
	if cell == nil || !isSelectionWordChar(*cell, s) {
		return
	}
	in := func(x int) bool {
		c := getCellAtCol(cm, grid, x)
		return c != nil && (c.Width == 0 && c.Content == "" || isSelectionWordChar(*c, s))
	}
	x0, x1 := cm.CursorX, cm.CursorX
	for x0 > 0 && in(x0-1) {
		x0--
	}
	for x1 < grid.LastContentCol() && in(x1+1) {
		x1++
	}
	absY := getAbsoluteY(cm, grid)
	cm.State = terminal.CopyModeVisualChar
	cm.VisualStart = terminal.Position{X: x0, Y: absY}
	cm.VisualEnd = terminal.Position{X: x1, Y: absY}
	cm.CursorX = x1
}
