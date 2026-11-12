package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

// Motion-related functions for copy mode (hjkl, page navigation, jumps, etc.)

// cellContent returns the content string from a cell, or empty string if cell is nil.
func cellContent(cell *uv.Cell) string {
	if cell != nil {
		return cell.Content
	}
	return ""
}

// Internal movement functions

func moveLeft(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	if cm.CursorX > 0 {
		cm.CursorX--
		// Skip continuation cells (Width=0) of wide characters
		// Move left until we find a cell with Width > 0
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
		// Skip continuation cells (Width=0) of wide characters
		// Move right until we find a cell with Width > 0
		for cm.CursorX < maxX {
			cell := getCellAtCursor(cm, grid)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX++
		}
	}
}

// moveUp moves cursor up (k key). It keeps cursor in middle of viewport when possible
func moveUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	midPoint := grid.ViewHeight() / 2

	if cm.CursorY > midPoint {
		// Cursor below middle: just move it up
		cm.CursorY--
	} else if cm.ScrollOffset < grid.ScrollbackLen() {
		// Cursor at/above middle: scroll content instead (cursor stays in place)
		cm.ScrollOffset++
		grid.SetScrollOffset(cm.ScrollOffset)
	} else if cm.CursorY > 0 {
		// At top of scrollback, cursor can still move
		cm.CursorY--
	}
}

// moveDown moves cursor down (j key). It keeps cursor in middle of viewport when possible
func moveDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	midPoint := grid.ViewHeight() / 2

	if cm.CursorY < midPoint {
		// Cursor above middle: just move it down
		cm.CursorY++
	} else if cm.ScrollOffset > 0 {
		// Cursor at/below middle: scroll content instead (cursor stays in place)
		cm.ScrollOffset--
		grid.SetScrollOffset(cm.ScrollOffset)
	} else if cm.CursorY < grid.LastContentRow() {
		// At live content, cursor can move to bottom
		cm.CursorY++
	}
}

// moveWordForward moves cursor to next word
func moveWordForward(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000 // Prevent infinite loops

	cell := getCellAtCursor(cm, grid)
	currentType := getCharType(cellContent(cell))

	// Phase 1: Skip current word/punctuation group
	for range maxIterations {
		cell := getCellAtCursor(cm, grid)
		charType := getCharType(cellContent(cell))

		// Stop if we hit a different type (but continue through same type)
		if charType != currentType || charType == 0 {
			break
		}

		if cm.CursorX >= maxWidth {
			// Wrap to next line
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Skip whitespace to next word/punctuation
	for range maxIterations {
		cell := getCellAtCursor(cm, grid)
		charType := getCharType(cellContent(cell))

		// Found a non-whitespace character: we're at start of next word
		if charType != 0 {
			break
		}

		if cm.CursorX >= maxWidth {
			// Wrap to next line
			cm.CursorX = 0
			moveDown(cm, grid)
		} else {
			cm.CursorX++
		}
	}
}

// moveWordBackward moves cursor to previous word
func moveWordBackward(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	// Move left at least once to leave current position
	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		moveUp(cm, grid)
		cm.CursorX = maxWidth
	} else {
		return // Already at top-left
	}

	// Phase 1: Skip whitespace backward
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
			return // At top-left
		}
	}

	// Phase 2: Move to start of current word/punctuation group
	// Get the type of the current (non-whitespace) character
	cell := getCellAtCursor(cm, grid)
	var currentContent string
	if cell != nil {
		currentContent = cell.Content
	}
	currentType := getCharType(currentContent)

	for range maxIterations {
		if cm.CursorX == 0 {
			// At start of line: this is the word start
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

		// If previous char is different type, we're at word start
		if prevType != currentType {
			break
		}

		cm.CursorX--
	}
}

// moveWordEnd moves cursor to end of current word
func moveWordEnd(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	// Move right at least once to leave current position
	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		cm.CursorX = 0
		moveDown(cm, grid)
		return
	}

	// Phase 1: Skip whitespace
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

	// Phase 2: Move to end of word (last non-whitespace character)
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

		// If next char is whitespace/empty, we're at word end
		if nextCell == nil || nextCell.Content == "" || nextCell.Content == " " || nextCell.Content == "\t" {
			break
		}

		// Next char is part of word, move forward
		cm.CursorX++
	}
}

// moveWordForwardBig moves cursor to next WORD (whitespace-delimited)
func moveWordForwardBig(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	// Like 'w' but treats any whitespace-delimited sequence as a word
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	// Phase 1: Skip current WORD (any non-whitespace)
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

	// Phase 2: Skip whitespace to next WORD
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

// moveWordBackwardBig moves cursor to previous WORD (whitespace-delimited)
func moveWordBackwardBig(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	// Like 'b' but for WORDs
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

	// Phase 1: Skip whitespace backward
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

	// Phase 2: Move to start of WORD
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

// moveWordEndBig moves cursor to end of current WORD
func moveWordEndBig(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	// Like 'e' but for WORDs
	maxWidth := grid.LastContentCol()
	maxIterations := 1000

	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		cm.CursorX = 0
		moveDown(cm, grid)
		return
	}

	// Phase 1: Skip whitespace
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

	// Phase 2: Move to end of WORD
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

// moveHalfPageUp moves cursor half page up
func moveHalfPageUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ViewHeight()/2)
	for range lines {
		moveUp(cm, grid)
	}
}

// moveHalfPageDown moves cursor half page down
func moveHalfPageDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ViewHeight()/2)
	for range lines {
		moveDown(cm, grid)
	}
}

// movePageUp moves cursor full page up
func movePageUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ContentHeight())
	for range lines {
		moveUp(cm, grid)
	}
}

// movePageDown moves cursor full page down
func movePageDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	lines := max(1, grid.ContentHeight())
	for range lines {
		moveDown(cm, grid)
	}
}

// moveToTop moves cursor to beginning of scrollback
func moveToTop(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.ScrollOffset = grid.ScrollbackLen()
	grid.SetScrollOffset(cm.ScrollOffset) // Sync for rendering
	cm.CursorY = 0
	cm.CursorX = 0
}

// moveToBottom moves cursor to end of live content
func moveToBottom(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cm.ScrollOffset = 0
	grid.SetScrollOffset(cm.ScrollOffset) // Sync for rendering
	cm.CursorY = grid.LastContentRow()
	cm.CursorX = 0
}

// moveParagraphUp moves cursor to start of previous paragraph
func moveParagraphUp(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	// Move up until we find a blank line, then skip blank lines
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
			break // At top
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

// moveParagraphDown moves cursor to end of next paragraph
func moveParagraphDown(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	// Move down until we find a blank line, then skip blank lines
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
			break // At bottom
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

// moveToMatchingBracket moves cursor to matching bracket
func moveToMatchingBracket(cm *terminal.CopyMode, grid terminal.CopyGrid) {
	cell := getCellAtCursor(cm, grid)
	if cell == nil || cell.Content == "" {
		return
	}

	char := cell.Content
	var matchChar string
	var direction int // 1 for forward, -1 for backward

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
		return // Not on a bracket
	}

	depth := 1
	maxIterations := 10000

	for i := 0; i < maxIterations && depth > 0; i++ {
		if direction > 0 {
			// Moving forward
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
					break // At end
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
					break // At start
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
