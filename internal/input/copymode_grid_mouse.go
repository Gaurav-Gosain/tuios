package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The mouse over a copy grid that is not a pane: the agents view's thread
// and quick look's text. A pane's mouse selection (mouse_select.go) reads the
// window's screen coordinates; these read a cell of the grid's view, which
// the caller works out from where it drew the grid. Past that they move the
// cursor and the selection with the code a key moves them with.

// gridPoint puts the cursor on cell x, y of the view, which shows rows rows.
// A cell above or below the view scrolls the view a row toward it, with the
// cursor on the edge row, and the result says which way: -1 up, 1 down, 0
// when the cell is in the view.
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
	// Off the second half of a wide cell, onto the cell itself.
	for cm.CursorX > 0 {
		cell := getCellAtCursor(cm, grid)
		if cell == nil || cell.Width > 0 {
			break
		}
		cm.CursorX--
	}
	return dir
}

// gridPress is a press on cell x, y: one click puts the cursor there and
// drops a selection, two select the word under it, three the line.
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

// gridDrag moves the selection's end to cell x, y, starting a character
// selection at the cursor when there is none. It returns gridPoint's scroll
// direction.
func gridDrag(cm *terminal.CopyMode, grid terminal.CopyGrid, x, y, rows int) int {
	if !cm.State.InVisual() {
		enterVisualChar(cm, grid)
	}
	dir := gridPoint(cm, grid, x, y, rows)
	updateVisualEnd(cm, grid)
	return dir
}

// gridSelectWord selects the word under the cursor, the way a double click
// on a pane does: letters, digits and appearance.word_characters, so a path
// or a URL comes out whole. A cursor on a space selects nothing.
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
