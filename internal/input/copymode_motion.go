// Package input implements vim-style copy mode for TUIOS.
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

// MoveLeft moves cursor left
func MoveLeft(cm *terminal.CopyMode, window *terminal.Window) {
	moveLeft(cm, window)
}

// MoveRight moves cursor right
func MoveRight(cm *terminal.CopyMode, window *terminal.Window) {
	moveRight(cm, window)
}

// MoveUp moves cursor up
func MoveUp(cm *terminal.CopyMode, window *terminal.Window) {
	moveUp(cm, window)
}

// MoveDown moves cursor down
func MoveDown(cm *terminal.CopyMode, window *terminal.Window) {
	moveDown(cm, window)
}

// Internal movement functions

func moveLeft(cm *terminal.CopyMode, window *terminal.Window) {
	if cm.CursorX > 0 {
		cm.CursorX--
		// Skip continuation cells (Width=0) of wide characters
		// Move left until we find a cell with Width > 0
		for cm.CursorX > 0 {
			cell := getCellAtCursor(cm, window)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX--
		}
	}
}

func moveRight(cm *terminal.CopyMode, window *terminal.Window) {
	maxX := window.Width - 3
	if cm.CursorX < maxX {
		cm.CursorX++
		// Skip continuation cells (Width=0) of wide characters
		// Move right until we find a cell with Width > 0
		for cm.CursorX < maxX {
			cell := getCellAtCursor(cm, window)
			if cell == nil || cell.Width > 0 {
				break
			}
			cm.CursorX++
		}
	}
}

// moveUp moves cursor up (k key) - keeps cursor in middle of viewport when possible
func moveUp(cm *terminal.CopyMode, window *terminal.Window) {
	midPoint := window.Height / 2

	if cm.CursorY > midPoint {
		// Cursor below middle - just move it up
		cm.CursorY--
	} else if cm.ScrollOffset < window.ScrollbackLen() {
		// Cursor at/above middle - scroll content instead (cursor stays in place)
		cm.ScrollOffset++
		window.ScrollbackOffset = cm.ScrollOffset
	} else if cm.CursorY > 0 {
		// At top of scrollback, cursor can still move
		cm.CursorY--
	}
}

// moveDown moves cursor down (j key) - keeps cursor in middle of viewport when possible
func moveDown(cm *terminal.CopyMode, window *terminal.Window) {
	midPoint := window.Height / 2

	if cm.CursorY < midPoint {
		// Cursor above middle - just move it down
		cm.CursorY++
	} else if cm.ScrollOffset > 0 {
		// Cursor at/below middle - scroll content instead (cursor stays in place)
		cm.ScrollOffset--
		window.ScrollbackOffset = cm.ScrollOffset
	} else if cm.CursorY < window.Height-3 {
		// At live content, cursor can move to bottom
		cm.CursorY++
	}
}

// moveWordForward moves cursor to next word
func moveWordForward(cm *terminal.CopyMode, window *terminal.Window) {
	maxWidth := window.Width - 3
	maxIterations := 1000 // Prevent infinite loops

	// Get current character type
	cell := getCellAtCursor(cm, window)
	currentType := getCharType(cellContent(cell))

	// Phase 1: Skip current word/punctuation group
	for range maxIterations {
		cell := getCellAtCursor(cm, window)
		charType := getCharType(cellContent(cell))

		// Stop if we hit a different type (but continue through same type)
		if charType != currentType || charType == 0 {
			break
		}

		// Move right, potentially wrapping to next line
		if cm.CursorX >= maxWidth {
			// Wrap to next line
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Skip whitespace to next word/punctuation
	for range maxIterations {
		cell := getCellAtCursor(cm, window)
		charType := getCharType(cellContent(cell))

		// Found a non-whitespace character - we're at start of next word
		if charType != 0 {
			break
		}

		// Move right, potentially wrapping to next line
		if cm.CursorX >= maxWidth {
			// Wrap to next line
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}
}

// moveWordBackward moves cursor to previous word
func moveWordBackward(cm *terminal.CopyMode, window *terminal.Window) {
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move left at least once to leave current position
	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		// Wrap to end of previous line
		moveUp(cm, window)
		cm.CursorX = maxWidth
	} else {
		return // Already at top-left
	}

	// Phase 1: Skip whitespace backward
	for range maxIterations {
		cell := getCellAtCursor(cm, window)
		charType := getCharType(cellContent(cell))

		// Found non-whitespace - move to phase 2
		if charType != 0 {
			break
		}

		// Move left, potentially wrapping
		if cm.CursorX > 0 {
			cm.CursorX--
		} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
			moveUp(cm, window)
			cm.CursorX = maxWidth
		} else {
			return // At top-left
		}
	}

	// Phase 2: Move to start of current word/punctuation group
	// Get the type of the current (non-whitespace) character
	cell := getCellAtCursor(cm, window)
	var currentContent string
	if cell != nil {
		currentContent = cell.Content
	}
	currentType := getCharType(currentContent)

	for range maxIterations {
		if cm.CursorX == 0 {
			// At start of line - this is the word start
			break
		}

		// Peek at previous character
		prevX := cm.CursorX - 1
		absY := getAbsoluteY(cm, window)
		var prevCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && prevX < len(line) {
				prevCell = &line[prevX]
			}
		} else {
			screenY := absY - scrollbackLen
			prevCell = window.Terminal.CellAt(prevX, screenY)
		}

		// Get previous character type
		var prevContent string
		if prevCell != nil {
			prevContent = prevCell.Content
		}
		prevType := getCharType(prevContent)

		// If previous char is different type, we're at word start
		if prevType != currentType {
			break
		}

		// Previous char is same type, move back
		cm.CursorX--
	}
}

// moveWordEnd moves cursor to end of current word
func moveWordEnd(cm *terminal.CopyMode, window *terminal.Window) {
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move right at least once to leave current position
	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		// Wrap to next line
		cm.CursorX = 0
		moveDown(cm, window)
		return
	}

	// Phase 1: Skip whitespace
	for range maxIterations {
		cell := getCellAtCursor(cm, window)

		// Found non-whitespace - move to phase 2
		if cell != nil && cell.Content != "" && cell.Content != " " && cell.Content != "\t" {
			break
		}

		// Move right, potentially wrapping
		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Move to end of word (last non-whitespace character)
	for range maxIterations {
		// Peek at next character
		nextX := cm.CursorX + 1
		if nextX > maxWidth {
			// At end of line
			break
		}

		absY := getAbsoluteY(cm, window)
		var nextCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && nextX < len(line) {
				nextCell = &line[nextX]
			}
		} else {
			screenY := absY - scrollbackLen
			nextCell = window.Terminal.CellAt(nextX, screenY)
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
func moveWordForwardBig(cm *terminal.CopyMode, window *terminal.Window) {
	// Like 'w' but treats any whitespace-delimited sequence as a word
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Phase 1: Skip current WORD (any non-whitespace)
	for range maxIterations {
		cell := getCellAtCursor(cm, window)
		if cell == nil || cell.Content == " " || cell.Content == "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}

	// Phase 2: Skip whitespace to next WORD
	for range maxIterations {
		cell := getCellAtCursor(cm, window)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
		} else {
			cm.CursorX++
		}
	}
}

// moveWordBackwardBig moves cursor to previous WORD (whitespace-delimited)
func moveWordBackwardBig(cm *terminal.CopyMode, window *terminal.Window) {
	// Like 'b' but for WORDs
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move left at least once
	if cm.CursorX > 0 {
		cm.CursorX--
	} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
		moveUp(cm, window)
		cm.CursorX = maxWidth
	} else {
		return
	}

	// Phase 1: Skip whitespace backward
	for range maxIterations {
		cell := getCellAtCursor(cm, window)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX > 0 {
			cm.CursorX--
		} else if cm.CursorY > 0 || cm.ScrollOffset > 0 {
			moveUp(cm, window)
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

		// Peek at previous character
		prevX := cm.CursorX - 1
		absY := getAbsoluteY(cm, window)
		var prevCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && prevX < len(line) {
				prevCell = &line[prevX]
			}
		} else {
			screenY := absY - scrollbackLen
			prevCell = window.Terminal.CellAt(prevX, screenY)
		}

		if prevCell == nil || prevCell.Content == " " || prevCell.Content == "\t" {
			break
		}

		cm.CursorX--
	}
}

// moveWordEndBig moves cursor to end of current WORD
func moveWordEndBig(cm *terminal.CopyMode, window *terminal.Window) {
	// Like 'e' but for WORDs
	maxWidth := window.Width - 3
	maxIterations := 1000

	// Move right at least once
	if cm.CursorX < maxWidth {
		cm.CursorX++
	} else {
		cm.CursorX = 0
		moveDown(cm, window)
		return
	}

	// Phase 1: Skip whitespace
	for range maxIterations {
		cell := getCellAtCursor(cm, window)

		if cell != nil && cell.Content != " " && cell.Content != "\t" {
			break
		}

		if cm.CursorX >= maxWidth {
			cm.CursorX = 0
			moveDown(cm, window)
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

		absY := getAbsoluteY(cm, window)
		var nextCell *uv.Cell

		scrollbackLen := window.ScrollbackLen()
		if absY < scrollbackLen {
			line := window.ScrollbackLine(absY)
			if line != nil && nextX < len(line) {
				nextCell = &line[nextX]
			}
		} else {
			screenY := absY - scrollbackLen
			nextCell = window.Terminal.CellAt(nextX, screenY)
		}

		if nextCell == nil || nextCell.Content == " " || nextCell.Content == "\t" {
			break
		}

		cm.CursorX++
	}
}

// moveHalfPageUp moves cursor half page up
func moveHalfPageUp(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height/2)
	for range lines {
		moveUp(cm, window)
	}
}

// moveHalfPageDown moves cursor half page down
func moveHalfPageDown(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height/2)
	for range lines {
		moveDown(cm, window)
	}
}

// movePageUp moves cursor full page up
func movePageUp(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height-2)
	for range lines {
		moveUp(cm, window)
	}
}

// movePageDown moves cursor full page down
func movePageDown(cm *terminal.CopyMode, window *terminal.Window) {
	lines := max(1, window.Height-2)
	for range lines {
		moveDown(cm, window)
	}
}

// moveToTop moves cursor to beginning of scrollback
func moveToTop(cm *terminal.CopyMode, window *terminal.Window) {
	cm.ScrollOffset = window.ScrollbackLen()
	window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
	cm.CursorY = 0
	cm.CursorX = 0
}

// moveToBottom moves cursor to end of live content
func moveToBottom(cm *terminal.CopyMode, window *terminal.Window) {
	cm.ScrollOffset = 0
	window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
	cm.CursorY = window.Height - 3
	cm.CursorX = 0
}

// moveParagraphUp moves cursor to start of previous paragraph
func moveParagraphUp(cm *terminal.CopyMode, window *terminal.Window) {
	// Move up until we find a blank line, then skip blank lines
	maxIterations := 1000
	foundNonBlank := false

	for range maxIterations {
		// Check if current line is blank
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		isBlank := len([]rune(lineText)) == 0 || isBlankLine(lineText)

		if foundNonBlank && isBlank {
			// Found the blank line separating paragraphs
			break
		}
		if !isBlank {
			foundNonBlank = true
		}

		// Move up
		if cm.CursorY > 0 {
			cm.CursorY--
		} else if cm.ScrollOffset < window.ScrollbackLen() {
			cm.ScrollOffset++
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break // At top
		}
	}

	// Skip any additional blank lines
	for range maxIterations {
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		if !isBlankLine(lineText) {
			break
		}

		// Move up
		if cm.CursorY > 0 {
			cm.CursorY--
		} else if cm.ScrollOffset < window.ScrollbackLen() {
			cm.ScrollOffset++
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break
		}
	}
}

// moveParagraphDown moves cursor to end of next paragraph
func moveParagraphDown(cm *terminal.CopyMode, window *terminal.Window) {
	// Move down until we find a blank line, then skip blank lines
	maxIterations := 1000
	foundNonBlank := false

	for range maxIterations {
		// Check if current line is blank
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		isBlank := len([]rune(lineText)) == 0 || isBlankLine(lineText)

		if foundNonBlank && isBlank {
			// Found the blank line separating paragraphs
			break
		}
		if !isBlank {
			foundNonBlank = true
		}

		// Move down
		if cm.CursorY < window.Height-3 {
			cm.CursorY++
		} else if cm.ScrollOffset > 0 {
			cm.ScrollOffset--
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break // At bottom
		}
	}

	// Skip any additional blank lines
	for range maxIterations {
		absY := getAbsoluteY(cm, window)
		lineText := getLineText(cm, window, absY)
		if !isBlankLine(lineText) {
			break
		}

		// Move down
		if cm.CursorY < window.Height-3 {
			cm.CursorY++
		} else if cm.ScrollOffset > 0 {
			cm.ScrollOffset--
			window.ScrollbackOffset = cm.ScrollOffset
		} else {
			break
		}
	}
}

// moveToMatchingBracket moves cursor to matching bracket
func moveToMatchingBracket(cm *terminal.CopyMode, window *terminal.Window) {
	// Get character at cursor
	cell := getCellAtCursor(cm, window)
	if cell == nil || cell.Content == "" {
		return
	}

	char := cell.Content
	var matchChar string
	var direction int // 1 for forward, -1 for backward

	// Determine matching bracket and search direction
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

	// Search for matching bracket
	depth := 1
	maxIterations := 10000

	for i := 0; i < maxIterations && depth > 0; i++ {
		// Move in search direction
		if direction > 0 {
			// Moving forward
			if cm.CursorX < window.Width-3 {
				cm.CursorX++
			} else {
				// Wrap to next line
				cm.CursorX = 0
				if cm.CursorY < window.Height-3 {
					cm.CursorY++
				} else if cm.ScrollOffset > 0 {
					cm.ScrollOffset--
					window.ScrollbackOffset = cm.ScrollOffset
				} else {
					break // At end
				}
			}
		} else {
			// Moving backward
			if cm.CursorX > 0 {
				cm.CursorX--
			} else {
				// Wrap to previous line
				cm.CursorX = window.Width - 3
				if cm.CursorY > 0 {
					cm.CursorY--
				} else if cm.ScrollOffset < window.ScrollbackLen() {
					cm.ScrollOffset++
					window.ScrollbackOffset = cm.ScrollOffset
				} else {
					break // At start
				}
			}
		}

		// Check current character
		currentCell := getCellAtCursor(cm, window)
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
