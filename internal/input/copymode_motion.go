package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

func cellContent(cell *uv.Cell) string {
	if cell != nil {
		return cell.Content
	}
	return ""
}

func moveLeft(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	if cm.CursorX > 0 {
		cm.CursorX--
		for cm.CursorX > 0 {
			cell := getCellAtCursor(cm, grid)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX--
		}
	}
}

func moveRight(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxX := grid.LastContentCol()
	if cm.CursorX < maxX {
		cm.CursorX++
		for cm.CursorX < maxX {
			cell := getCellAtCursor(cm, grid)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX++
		}
	}
}

func moveUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	midPoint := grid.ViewHeight() / 2

	if cm.CursorY > midPoint {
		cm.CursorY--
	} else if cm.ScrollOffset < grid.ScrollbackLen() {
		cm.ScrollOffset++
		grid.SetScrollOffset(cm.ScrollOffset)
	} else if cm.CursorY > 0 {
		cm.CursorY--
	}
}

func moveDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	midPoint := grid.ViewHeight() / 2

	if cm.CursorY < midPoint {
		cm.CursorY++
	} else if cm.ScrollOffset > 0 {
		cm.ScrollOffset--
		grid.SetScrollOffset(cm.ScrollOffset)
	} else if cm.CursorY < grid.LastContentRow() {
		cm.CursorY++
	}
}

func moveWordForward(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	cell := getCellAtCursor(cm, grid)
	currentType := getCharType(cellContent(cell))

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)
		charType := getCharType(cellContent(cell))

		if charType != currentType || charType == 0 {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)
		charType := getCharType(cellContent(cell))

		if charType != 0 {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}
}

func moveWordBackward(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		moveUp(cm, grid)
		cm.CursorX = maxWidth
	} else {
		return
	}

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)
		charType := getCharType(cellContent(cell))

		if charType != 0 {
			break
		}

		if cm.CursorX > 0 {
			cm.CursorX--
		} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
			moveUp(cm, grid)
			cm.CursorX = maxWidth
		} else {
			return
		}
	}

	cell := getCellAtCursor(cm, grid)
	var currentContent string
	if cell != nil {
		currentContent = cell.Content
	}
	currentType := getCharType(currentContent)

	for range maxIterations {
		if cm.CursorX == 0 {
			break
		}

		prevX := cm.CursorX - 1
		absY := getAbsoluteY(cm, grid)
		var prevCell *uv.Cell

		scrollbackLen := grid.ScrollbackLen()
		if absY < scrollbackLen {
			line := grid.ScrollbackLine(absY)
			if line != nil && prevX < len(line) {
				prevCell = &line[prevX]
			}
		} else {
			screenY := absY - scrollbackLen
			prevCell = grid.Text().CellAt(prevX, screenY)
		}

		var prevContent string
		if prevCell != nil {
			prevContent = prevCell.Content
		}
		prevType := getCharType(prevContent)

		if prevType != currentType {
			break
		}

		cm.CursorX--
	}
}

func moveWordEnd(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		cm.CursorX = 0
		moveDown(cm, grid)
		return
	}

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)

		if cell != nil && cell.Content != "" && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}

	for range maxIterations {
		nextX := cm.CursorX + 1
		if nextX > maxWidth {
			break
		}

		absY := getAbsoluteY(cm, grid)
		var nextCell *uv.Cell

		scrollbackLen := grid.ScrollbackLen()
		if absY < scrollbackLen {
			line := grid.ScrollbackLine(absY)
			if line != nil && nextX < len(line) {
				nextCell = &line[nextX]
			}
		} else {
			screenY := absY - scrollbackLen
			nextCell = grid.Text().CellAt(nextX, screenY)
		}

		if nextCell == nil || nextCell.Content == "" || nextCell.Content == " " || nextCell.Content == "\t" {
			break
		}

		cm.CursorX++
	}
}

func moveWordForwardBig(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)
		if cell == nil || cell.Content == " " || cell.Content == "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}
}

func moveWordBackwardBig(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		moveUp(cm, grid)
		cm.CursorX = maxWidth
	} else {
		return
	}

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX > 0 {
			cm.CursorX--
		} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
			moveUp(cm, grid)
			cm.CursorX = maxWidth
		} else {
			return
		}
	}

	for range maxIterations {
		if cm.CursorX == 0 {
			break
		}

		prevX := cm.CursorX - 1
		absY := getAbsoluteY(cm, grid)
		var prevCell *uv.Cell

		scrollbackLen := grid.ScrollbackLen()
		if absY < scrollbackLen {
			line := grid.ScrollbackLine(absY)
			if line != nil && prevX < len(line) {
				prevCell = &line[prevX]
			}
		} else {
			screenY := absY - scrollbackLen
			prevCell = grid.Text().CellAt(prevX, screenY)
		}

		if prevCell == nil || prevCell.Content == " " || prevCell.Content == "\t" {
			break
		}

		cm.CursorX--
	}
}

func moveWordEndBig(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		cm.CursorX = 0
		moveDown(cm, grid)
		return
	}

	for range maxIterations {
		cell := getCellAtCursor(cm, grid)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}

	for range maxIterations {
		nextX := cm.CursorX + 1
		if nextX > maxWidth {
			break
		}

		absY := getAbsoluteY(cm, grid)
		var nextCell *uv.Cell

		scrollbackLen := grid.ScrollbackLen()
		if absY < scrollbackLen {
			line := grid.ScrollbackLine(absY)
			if line != nil && nextX < len(line) {
				nextCell = &line[nextX]
			}
		} else {
			screenY := absY - scrollbackLen
			nextCell = grid.Text().CellAt(nextX, screenY)
		}

		if nextCell == nil || nextCell.Content == " " || nextCell.Content == "\t" {
			break
		}

		cm.CursorX++
	}
}

func moveHalfPageUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ViewHeight()/2)
	for range lines {
		moveUp(cm, grid)
	}
}

func moveHalfPageDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ViewHeight()/2)
	for range lines {
		moveDown(cm, grid)
	}
}

func movePageUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ContentHeight())
	for range lines {
		moveUp(cm, grid)
	}
}

func movePageDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ContentHeight())
	for range lines {
		moveDown(cm, grid)
	}
}

func moveToTop(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.ScrollOffset = grid.ScrollbackLen()
	grid.SetScrollOffset(cm.ScrollOffset)
	cm.CursorY = 0
	cm.CursorX = 0
}

func moveToBottom(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.ScrollOffset = 0
	grid.SetScrollOffset(cm.ScrollOffset)
	cm.CursorY = grid.LastContentRow()
	cm.CursorX = 0
}

func moveParagraphUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxIterations := 1000
	foundNonBlank := false

	for range maxIterations {
		absY := getAbsoluteY(cm, grid)
		lineText := getLineText(cm, grid, absY)
		isBlank := len([]rune(lineText)) == 0 || isBlankLine(lineText)

		if foundNonBlank && isBlank {
			break
		}
		if !isBlank {
			foundNonBlank = true
		}

		if cm.CursorY > 0 {
			cm.CursorY--
		} else if cm.ScrollOffset < grid.ScrollbackLen() {
			cm.ScrollOffset++
			grid.SetScrollOffset(cm.ScrollOffset)
		} else {
			break
		}
	}

	for range maxIterations {
		absY := getAbsoluteY(cm, grid)
		lineText := getLineText(cm, grid, absY)
		if !isBlankLine(lineText) {
			break
		}

		if cm.CursorY > 0 {
			cm.CursorY--
		} else if cm.ScrollOffset < grid.ScrollbackLen() {
			cm.ScrollOffset++
			grid.SetScrollOffset(cm.ScrollOffset)
		} else {
			break
		}
	}
}

func moveParagraphDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxIterations := 1000
	foundNonBlank := false

	for range maxIterations {
		absY := getAbsoluteY(cm, grid)
		lineText := getLineText(cm, grid, absY)
		isBlank := len([]rune(lineText)) == 0 || isBlankLine(lineText)

		if foundNonBlank && isBlank {
			break
		}
		if !isBlank {
			foundNonBlank = true
		}

		if cm.CursorY < grid.LastContentRow() {
			cm.CursorY++
		} else if cm.ScrollOffset > 0 {
			cm.ScrollOffset--
			grid.SetScrollOffset(cm.ScrollOffset)
		} else {
			break
		}
	}

	for range maxIterations {
		absY := getAbsoluteY(cm, grid)
		lineText := getLineText(cm, grid, absY)
		if !isBlankLine(lineText) {
			break
		}

		if cm.CursorY < grid.LastContentRow() {
			cm.CursorY++
		} else if cm.ScrollOffset > 0 {
			cm.ScrollOffset--
			grid.SetScrollOffset(cm.ScrollOffset)
		} else {
			break
		}
	}
}

func moveToMatchingBracket(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cell := getCellAtCursor(cm, grid)
	if cell == nil || cell.Content == "" {
		return
	}

	char := cell.Content
	var matchChar string
	var direction int

	switch char {
	case "(":
		matchChar = ")"
		direction = 1
	case ")":
		matchChar = "("
		direction = -1
	case "[":
		matchChar = "]"
		direction = 1
	case "]":
		matchChar = "["
		direction = -1
	case "{":
		matchChar = "}"
		direction = 1
	case "}":
		matchChar = "{"
		direction = -1
	case "<":
		matchChar = ">"
		direction = 1
	case ">":
		matchChar = "<"
		direction = -1
	default:
		return
	}

	depth := 1
	maxIterations := 10000

	for i := 0; i < maxIterations && depth > 0; i++ {
		if direction > 0 {
			if cm.CursorX < grid.LastContentCol() {
				cm.CursorX++
			} else {
				cm.CursorX = 0
				if cm.CursorY < grid.LastContentRow() {
					cm.CursorY++
				} else if cm.ScrollOffset > 0 {
					cm.ScrollOffset--
					grid.SetScrollOffset(cm.ScrollOffset)
				} else {
					break
				}
			}
		} else {
			if cm.CursorX > 0 {
				cm.CursorX--
			} else {
				cm.CursorX = grid.LastContentCol()
				if cm.CursorY > 0 {
					cm.CursorY--
				} else if cm.ScrollOffset < grid.ScrollbackLen() {
					cm.ScrollOffset++
					grid.SetScrollOffset(cm.ScrollOffset)
				} else {
					break
				}
			}
		}

		currentCell := getCellAtCursor(cm, grid)
		if currentCell != nil && currentCell.Content != "" {
			currentChar := currentCell.Content
			switch currentChar {
			case char:
				depth++
			case matchChar:
				depth--
			}
		}
	}
}
