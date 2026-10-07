package input

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// Visual selection-related functions for copy mode (v/V/y and text extraction)

// enterVisualChar enters visual character selection mode
func enterVisualChar(cm *terminal.CopyMode, window *terminal.Window) {
	cm.State = terminal.CopyModeVisualChar
	absY := getAbsoluteY(cm, window)
	cm.VisualStart = terminal.Position{X: cm.CursorX, Y: absY}
	cm.VisualEnd = cm.VisualStart
}

// enterVisualLine enters visual line selection mode
func enterVisualLine(cm *terminal.CopyMode, window *terminal.Window) {
	cm.State = terminal.CopyModeVisualLine
	absY := getAbsoluteY(cm, window)

	// A line selection starts at the first column, so a line's indent is
	// part of it (#516), and ends at the line's last printed cell.
	_, endX := getLineContentBounds(cm, window, absY)

	cm.VisualStart = terminal.Position{X: 0, Y: absY}
	cm.VisualEnd = terminal.Position{X: endX, Y: absY}
}

// updateVisualEnd updates the visual selection end position
func updateVisualEnd(cm *terminal.CopyMode, window *terminal.Window) {
	absY := getAbsoluteY(cm, window)

	switch cm.State {
	case terminal.CopyModeVisualChar:
		cm.VisualEnd = terminal.Position{X: cm.CursorX, Y: absY}
	case terminal.CopyModeVisualLine:
		// For visual line mode, we need to select entire lines
		// Start Y stays fixed, we only update end Y
		cm.VisualEnd.Y = absY

		// The lower of the two lines sets where the selection ends.
		endY := max(cm.VisualStart.Y, cm.VisualEnd.Y)

		// The upper line is taken from its first column, indent included,
		// and the lower line to its last printed cell.
		const startLineStartX = 0
		_, endLineEndX := getLineContentBounds(cm, window, endY)

		// If moving upwards (current Y < original start Y), we want:
		// - Start to be at beginning of the upper line (current position)
		// - End to be at end of the lower line (original start)
		if absY < cm.VisualStart.Y {
			// Moving upwards
			cm.VisualEnd.X = startLineStartX
			cm.VisualStart.X = endLineEndX
		} else {
			// Moving downwards or same line
			cm.VisualStart.X = startLineStartX
			cm.VisualEnd.X = endLineEndX
		}
	}
}

// extractVisualText extracts the text from the current visual selection.
//
// The copy keeps every cell the selection covers up to the last printed cell
// of each row: leading spaces, interior runs, and the columns a tab moved
// over (the emulator stores a tab as the blanks it skipped). The blanks after
// a row's last printed cell are dropped. A cleared cell and a printed space
// are the same cell in the grid, so the end of the printed text is the only
// line that can be drawn there, and it is the one that keeps a copy free of
// the row's padding.
//
// A row the emulator soft-wrapped is a full row of printed text that carries
// on to the next, so it is kept whole, spaces at its edge included, and joined
// to the next row without a newline.
func extractVisualText(cm *terminal.CopyMode, window *terminal.Window) string {
	start, end := cm.VisualStart, cm.VisualEnd

	// Normalize selection
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}

	var text strings.Builder
	for y := start.Y; y <= end.Y; y++ {
		cells := selectionRowCells(window, y)
		wraps := y < end.Y && selectionRowWraps(window, y)
		if wraps && selectionRowPadded(window, y) && len(cells) > 0 {
			// The last column is the pad left by a wide character that
			// did not fit, not a printed space.
			cells = cells[:len(cells)-1]
		}

		lo, hi := 0, len(cells)-1
		if y == start.Y {
			lo = start.X
		}
		if y == end.Y {
			hi = min(hi, end.X)
		}
		if !wraps {
			hi = min(hi, lastPrintedCell(cells))
		}
		writeSelectionCells(&text, cells, lo, hi)

		if y < end.Y && !wraps {
			text.WriteByte('\n')
		}
	}

	// Rows below the last printed line are padding too: a drag that runs on
	// into the empty screen under the output adds no newlines.
	return strings.TrimRight(text.String(), "\n")
}

// selectionRowCells returns the cells of absolute row y, from the scrollback
// or the screen. A missing screen cell reads as a blank.
func selectionRowCells(window *terminal.Window, y int) []uv.Cell {
	scrollbackLen := window.ScrollbackLen()
	if y < scrollbackLen {
		return window.ScrollbackLine(y)
	}
	screenY := y - scrollbackLen
	cells := make([]uv.Cell, window.Terminal.Width())
	for x := range cells {
		if cell := window.Terminal.CellAt(x, screenY); cell != nil {
			cells[x] = *cell
		} else {
			cells[x] = uv.EmptyCell
		}
	}
	return cells
}

// selectionRowWraps reports whether absolute row y carries on to the next row
// because the emulator wrapped it. A backend that cannot tell reads as a row
// that ends, which copies a newline rather than joining two lines.
func selectionRowWraps(window *terminal.Window, y int) bool {
	if window.Terminal == nil {
		return false
	}
	var wrapped, known bool
	if scrollbackLen := window.ScrollbackLen(); y < scrollbackLen {
		wrapped, known = window.Terminal.ScrollbackSoftWrapped(y)
	} else {
		wrapped, known = window.Terminal.RowSoftWrapped(y - scrollbackLen)
	}
	return known && wrapped
}

// selectionRowPadded reports whether absolute row y wrapped a column early,
// because a wide character did not fit in its last column.
func selectionRowPadded(window *terminal.Window, y int) bool {
	if window.Terminal == nil {
		return false
	}
	if scrollbackLen := window.ScrollbackLen(); y < scrollbackLen {
		return window.Terminal.ScrollbackPadded(y)
	}
	return window.Terminal.RowPadded(y - window.ScrollbackLen())
}

// lastPrintedCell is the index of the last cell in cells that holds more than
// a blank, or -1 when the row is blank.
func lastPrintedCell(cells []uv.Cell) int {
	for x := len(cells) - 1; x >= 0; x-- {
		if c := cells[x].Content; c != "" && c != " " {
			return x
		}
	}
	return -1
}

// writeSelectionCells appends the text of cells[lo..hi]. A blank cell is a
// space. The cells a wide character covers past its first are skipped, since
// the character was already written.
func writeSelectionCells(text *strings.Builder, cells []uv.Cell, lo, hi int) {
	lo = max(lo, 0)
	hi = min(hi, len(cells)-1)
	// A selection that starts inside a wide character starts at its first
	// cell, so the character is not lost.
	for lo > 0 && lo <= hi && cells[lo].Width == 0 && cells[lo].Content == "" && cells[lo-1].Width > 1 {
		lo--
	}
	covered := 0
	for x := lo; x <= hi; x++ {
		c := cells[x]
		if covered > 0 {
			covered--
			if c.Content == "" {
				continue
			}
		}
		if c.Content == "" {
			text.WriteByte(' ')
			continue
		}
		text.WriteString(c.Content)
		if c.Width > 1 {
			covered = c.Width - 1
		}
	}
}

// getLineContentBounds returns the X positions of the first and last non-empty characters on a line
func getLineContentBounds(_ *terminal.CopyMode, window *terminal.Window, absY int) (int, int) {
	scrollbackLen := window.ScrollbackLen()

	// Get cells for this line
	var cells []uv.Cell
	if absY < scrollbackLen {
		cells = window.ScrollbackLine(absY)
	} else {
		screenY := absY - scrollbackLen
		cells = getScreenLineCells(window.Terminal, screenY)
	}

	if len(cells) == 0 {
		return 0, 0
	}

	// Find first non-empty, non-continuation cell
	startX := 0
	for i, cell := range cells {
		if cell.Width > 0 && cell.Content != "" && cell.Content != " " {
			startX = i
			break
		}
	}

	// Find last non-empty, non-continuation cell
	endX := len(cells) - 1
	for i := len(cells) - 1; i >= 0; i-- {
		if cells[i].Width > 0 && cells[i].Content != "" && cells[i].Content != " " {
			endX = i
			break
		}
	}

	// If entire line is empty, just return 0, 0
	if endX < startX {
		return 0, 0
	}

	return startX, endX
}

// getLineText retrieves the text content of a line
func getLineText(_ *terminal.CopyMode, window *terminal.Window, absY int) string {
	scrollbackLen := window.ScrollbackLen()

	if absY < scrollbackLen {
		line := window.ScrollbackLine(absY)
		if line != nil {
			return extractLineTextFromCells(line)
		}
	} else {
		screenY := absY - scrollbackLen
		return extractScreenLineText(window.Terminal, screenY)
	}

	return ""
}
