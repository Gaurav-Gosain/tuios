// Package input implements vim-style copy mode for TUIOS.
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

	// Get line content bounds (first to last non-empty character)
	startX, endX := getLineContentBounds(cm, window, absY)

	cm.VisualStart = terminal.Position{X: startX, Y: absY}
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

		// Determine which line is earlier and which is later
		startY := cm.VisualStart.Y
		endY := cm.VisualEnd.Y

		// Normalize: make sure startY <= endY for bounds calculation
		if startY > endY {
			startY, endY = endY, startY
		}

		// Get line content bounds for both lines
		startLineStartX, _ := getLineContentBounds(cm, window, startY)
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

// extractVisualText extracts the text from the current visual selection
func extractVisualText(cm *terminal.CopyMode, window *terminal.Window) string {
	start, end := cm.VisualStart, cm.VisualEnd

	// Normalize selection
	if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
		start, end = end, start
	}

	var text strings.Builder
	scrollbackLen := window.ScrollbackLen()

	// Single line
	if start.Y == end.Y {
		// Clamp selection to line content bounds to avoid copying empty cells
		_, lineEndX := getLineContentBounds(cm, window, start.Y)
		clampedEndX := min(end.X, lineEndX)

		if start.Y < scrollbackLen {
			line := window.ScrollbackLine(start.Y)
			for x := start.X; x <= clampedEndX && line != nil && x < len(line); x++ {
				if line[x].Content != "" && line[x].Content != " " {
					text.WriteString(line[x].Content)
				} else if line[x].Content == " " {
					// Preserve internal spaces but not empty cells
					text.WriteRune(' ')
				}
			}
		} else {
			screenY := start.Y - scrollbackLen
			for x := start.X; x <= clampedEndX && x < window.Width; x++ {
				cell := window.Terminal.CellAt(x, screenY)
				if cell != nil && cell.Content != "" && cell.Content != " " {
					text.WriteString(cell.Content)
				} else if cell != nil && cell.Content == " " {
					// Preserve internal spaces but not empty cells
					text.WriteRune(' ')
				}
			}
		}
		return strings.TrimSpace(text.String())
	}

	// Multi-line
	for y := start.Y; y <= end.Y; y++ {
		startX, endX := 0, window.Width-1

		if y == start.Y {
			startX = start.X
		}
		if y == end.Y {
			endX = end.X
		}

		// Clamp to line content bounds to avoid copying empty cells at end
		lineStartX, lineEndX := getLineContentBounds(cm, window, y)
		switch y {
		case start.Y:
			// First line: keep user's start but clamp end to content
			endX = min(endX, lineEndX)
		case end.Y:
			// Last line: keep user's end but clamp to content
			endX = min(endX, lineEndX)
		default:
			// Middle lines: use full content bounds
			startX = lineStartX
			endX = lineEndX
		}

		// Extract line content
		var lineCells []uv.Cell
		if y < scrollbackLen {
			lineCells = window.ScrollbackLine(y)
		} else {
			screenY := y - scrollbackLen
			// Build cells array from screen
			for x := range window.Width {
				cell := window.Terminal.CellAt(x, screenY)
				if cell != nil {
					lineCells = append(lineCells, *cell)
				} else {
					lineCells = append(lineCells, uv.Cell{})
				}
			}
		}

		// Append line content
		if lineCells != nil {
			for x := startX; x <= endX && x < len(lineCells); x++ {
				if lineCells[x].Content != "" && lineCells[x].Content != " " {
					text.WriteString(lineCells[x].Content)
				} else if lineCells[x].Content == " " {
					// Preserve internal spaces but not empty cells
					text.WriteRune(' ')
				}
			}
		}

		// Add newline only if this is NOT a soft-wrapped line
		if y < end.Y {
			// Check if this line is soft-wrapped (continues on next line)
			// Heuristic: if line content extends to terminal width and doesn't end with whitespace,
			// it's likely wrapped
			isSoftWrapped := false
			if len(lineCells) > 0 {
				// Find last non-empty cell
				lastNonEmptyX := -1
				for x := len(lineCells) - 1; x >= 0; x-- {
					if lineCells[x].Content != "" && lineCells[x].Content != " " {
						lastNonEmptyX = x
						break
					}
				}
				// If line extends close to terminal width, it's probably wrapped
				if lastNonEmptyX >= window.Width-5 {
					isSoftWrapped = true
				}
			}

			if isSoftWrapped {
				// Remove trailing whitespace since this line continues on the next
				currentText := text.String()
				text.Reset()
				text.WriteString(strings.TrimRight(currentText, " "))
			} else {
				text.WriteRune('\n')
			}
		}
	}

	return strings.TrimSpace(text.String())
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
