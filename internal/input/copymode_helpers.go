package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	vt "github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// getAbsoluteY calculates the absolute Y position in the entire scrollback+screen
func getAbsoluteY(cm *terminal.CopyMode, window *terminal.Window) int {
	scrollbackLen := window.ScrollbackLen()
	if cm.ScrollOffset > 0 {
		return scrollbackLen - cm.ScrollOffset + cm.CursorY
	}
	return scrollbackLen + cm.CursorY
}

// isVimWordChar returns true if the rune is part of a vim "word" (alphanumeric or underscore)
func isVimWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_'
}

// getCharType returns the type of character: 0=whitespace, 1=word char, 2=punctuation
func getCharType(content string) int {
	if content == "" || content == " " || content == "\t" {
		return 0 // whitespace
	}
	r := []rune(content)[0]
	if isVimWordChar(r) {
		return 1 // word character
	}
	return 2 // punctuation/special
}

// getCellAtCursor returns the cell at the current cursor position
func getCellAtCursor(cm *terminal.CopyMode, window *terminal.Window) *uv.Cell {
	absY := getAbsoluteY(cm, window)
	scrollbackLen := window.ScrollbackLen()

	if absY < scrollbackLen {
		line := window.ScrollbackLine(absY)
		if line != nil && cm.CursorX < len(line) {
			return &line[cm.CursorX]
		}
		return nil
	}

	screenY := absY - scrollbackLen
	return window.Terminal.CellAt(cm.CursorX, screenY)
}

// extractLineTextFromCells builds text string from cell array
func extractLineTextFromCells(cells []uv.Cell) string {
	var result []rune
	for _, cell := range cells {
		// Skip continuation cells (Width=0) of wide characters
		// These are placeholder cells for emoji, CJK, nerd fonts, etc.
		if cell.Width == 0 {
			continue
		}
		if cell.Content != "" {
			for _, r := range vt.CellText(cell.Content) {
				result = append(result, r)
			}
		} else {
			result = append(result, ' ')
		}
	}
	return string(result)
}

// extractScreenLineText builds text string from terminal screen line
func extractScreenLineText(term vt.Terminal, y int) string {
	var result []rune
	width := term.Width()
	for x := range width {
		cell := term.CellAt(x, y)
		// Skip continuation cells (Width=0) of wide characters
		// These are placeholder cells for emoji, CJK, nerd fonts, etc.
		if cell != nil && cell.Width == 0 {
			continue
		}
		if cell != nil && cell.Content != "" {
			for _, r := range vt.CellText(cell.Content) {
				result = append(result, r)
			}
		} else {
			result = append(result, ' ')
		}
	}
	return string(result)
}

// getScreenLineCells returns all cells for a screen line
func getScreenLineCells(term vt.Terminal, y int) []uv.Cell {
	width := term.Width()
	cells := make([]uv.Cell, width)

	for x := range width {
		cell := term.CellAt(x, y)
		if cell != nil {
			cells[x] = *cell
			vt.BlankSixelCell(&cells[x])
		} else {
			// Empty cell
			cells[x] = uv.Cell{Content: " ", Width: 1}
		}
	}

	return cells
}

// isBlankLine returns true if a line contains only whitespace
func isBlankLine(lineText string) bool {
	for _, r := range lineText {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}
