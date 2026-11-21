package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	vt "tuios.dev/vt"
)

func getAbsoluteY(cm *terminal.CopyMode, grid terminal.CopyGrid) int {
	scrollbackLen := grid.ScrollbackLen()
	if cm.ScrollOffset > 0 {
		return scrollbackLen - cm.ScrollOffset + cm.CursorY
	}
	return scrollbackLen + cm.CursorY
}

func isVimWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_'
}

func getCharType(content string) int {
	if content == "" || content == " " || content == "\t" {
		return 0
	}
	r := []rune(content)[0]
	if isVimWordChar(r) {
		return 1
	}
	return 2
}

func getCellAtCursor(cm *terminal.CopyMode, grid terminal.CopyGrid) *uv.Cell {
	return getCellAtCol(cm, grid, cm.CursorX)
}

func getCellAtCol(cm *terminal.CopyMode, grid terminal.CopyGrid, x int) *uv.Cell {
	absY := getAbsoluteY(cm, grid)
	scrollbackLen := grid.ScrollbackLen()

	if absY < scrollbackLen {
		line := grid.ScrollbackLine(absY)
		if line != nil && x < len(line) {
			return &line[x]
		}
		return nil
	}

	screenY := absY - scrollbackLen
	return grid.Text().CellAt(x, screenY)
}

func firstNonBlankCol(cm *terminal.CopyMode, grid terminal.CopyGrid) int {
	for x := 0; x <= grid.LastContentCol(); x++ {
		if cell := getCellAtCol(cm, grid, x); cell != nil && cell.Width > 0 && getCharType(cell.Content) != 0 {
			return x
		}
	}
	return 0
}

func lastTextCol(cm *terminal.CopyMode, grid terminal.CopyGrid) int {
	for x := grid.LastContentCol(); x > 0; x-- {
		if cell := getCellAtCol(cm, grid, x); cell != nil && cell.Width > 0 && getCharType(cell.Content) != 0 {
			return x
		}
	}
	return 0
}

func extractLineTextFromCells(cells []uv.Cell) string {
	var result []rune
	for _, cell := range cells {
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

func extractScreenLineText(term terminal.CopyText, y int) string {
	var result []rune
	width := term.Width()
	for x := range width {
		cell := term.CellAt(x, y)
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

func getScreenLineCells(term terminal.CopyText, y int) []uv.Cell {
	width := term.Width()
	cells := make([]uv.Cell, width)

	for x := range width {
		cell := term.CellAt(x, y)
		if cell != nil {
			cells[x] = *cell
			vt.BlankSixelCell(&cells[x])
		} else {
			cells[x] = uv.Cell{Content: " ", Width: 1}
		}
	}

	return cells
}

func isBlankLine(lineText string) bool {
	for _, r := range lineText {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}
