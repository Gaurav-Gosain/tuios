package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Character search-related functions for copy mode (f/F/t/T and ;/,)

// convertColumnToRuneIndex converts a column position to a rune index in the text
// accounting for wide characters (emoji, nerd fonts, CJK, etc.)
func convertColumnToRuneIndex(grid terminal.CopyGrid, absY int, col int) int {
	scrollbackLen := grid.ScrollbackLen()
	runeIndex := 0
	currentCol := 0

	if absY < scrollbackLen {
		line := grid.ScrollbackLine(absY)
		for _, cell := range line {
			if currentCol >= col {
				break
			}
			if cell.Width > 0 { // Skip continuation cells
				// Count runes in this cell's content
				for range cell.Content {
					runeIndex++
				}
				currentCol += cell.Width
			}
		}
	} else {
		screenY := absY - scrollbackLen
		for x := range grid.Text().Width() {
			if currentCol >= col {
				break
			}
			cell := grid.Text().CellAt(x, screenY)
			if cell != nil && cell.Width > 0 { // Skip continuation cells
				// Count runes in this cell's content
				for range cell.Content {
					runeIndex++
				}
				currentCol += cell.Width
			}
		}
	}

	return runeIndex
}

// findCharOnLine searches for a character across multiple lines
// direction: 1 for forward, -1 for backward
// till: true to stop before the character, false to land on it
func findCharOnLine(cm *terminal.CopyMode, grid terminal.CopyGrid, char rune, direction int, till bool) {
	startAbsY := getAbsoluteY(cm, grid)
	scrollbackLen := grid.ScrollbackLen()
	screenHeight := grid.Text().Height()
	totalLines := scrollbackLen + screenHeight

	maxIterations := 1000 // Prevent infinite loops

	if direction > 0 {
		// Search forward across lines
		for lineOffset := range maxIterations {
			absY := startAbsY + lineOffset
			if absY >= totalLines {
				break
			}

			lineText := getLineText(cm, grid, absY)
			if lineText == "" {
				continue
			}

			// Convert lineText to runes
			runes := []rune(lineText)

			// Determine starting position
			startCharIdx := 0
			if lineOffset == 0 {
				// On current line, get cursor's rune position and start from next rune
				currentRuneIdx := convertColumnToRuneIndex(grid, absY, cm.CursorX)
				startCharIdx = currentRuneIdx + 1
			}

			// Search this line
			for charIdx := startCharIdx; charIdx < len(runes); charIdx++ {
				if runes[charIdx] == char {
					// Found it!
					targetCharIdx := charIdx
					if till {
						targetCharIdx = charIdx - 1
						if targetCharIdx < 0 {
							continue // Can't stop before first character
						}
					}

					// Move to the target line using step-by-step movement
					// This ensures scroll offset is handled correctly
					linesToMove := absY - startAbsY
					for range linesToMove {
						moveDown(cm, grid)
					}

					// Set cursor X position
					cm.CursorX = convertRuneIndexToColumn(grid, absY, targetCharIdx)
					return
				}
			}
		}
	} else {
		// Search backward across lines
		for lineOffset := range maxIterations {
			absY := startAbsY - lineOffset
			if absY < 0 {
				break
			}

			lineText := getLineText(cm, grid, absY)
			if lineText == "" {
				continue
			}

			// Convert lineText to runes
			runes := []rune(lineText)

			// Determine starting position
			endCharIdx := len(runes) - 1
			if lineOffset == 0 {
				// On current line, get cursor's rune position and start from previous rune
				currentRuneIdx := convertColumnToRuneIndex(grid, absY, cm.CursorX)
				endCharIdx = currentRuneIdx - 1
			}

			// Search this line backward
			for charIdx := endCharIdx; charIdx >= 0; charIdx-- {
				if runes[charIdx] == char {
					// Found it!
					targetCharIdx := charIdx
					if till {
						targetCharIdx = charIdx + 1
						if targetCharIdx >= len(runes) {
							continue // Can't stop after last character
						}
					}

					// Move to the target line using step-by-step movement
					// This ensures scroll offset is handled correctly
					linesToMove := startAbsY - absY
					for range linesToMove {
						moveUp(cm, grid)
					}

					// Set cursor X position
					cm.CursorX = convertRuneIndexToColumn(grid, absY, targetCharIdx)
					return
				}
			}
		}
	}
	// Character not found: no movement
}

// repeatCharSearch repeats the last character search
func repeatCharSearch(cm *terminal.CopyMode, grid terminal.CopyGrid, reverse bool) {
	if cm.LastCharSearch == 0 {
		return // No previous character search
	}

	direction := cm.LastCharSearchDir
	if reverse {
		direction = -direction
	}

	findCharOnLine(cm, grid, cm.LastCharSearch, direction, cm.LastCharSearchTill)
}

// convertRuneIndexToColumn converts a rune index in the text string to a column position
// accounting for wide characters (emoji, nerd fonts, CJK, etc.)
func convertRuneIndexToColumn(grid terminal.CopyGrid, absY int, runeIndex int) int {
	scrollbackLen := grid.ScrollbackLen()

	if runeIndex < 0 {
		return 0
	}

	// Count actual columns (accounting for wide chars)
	col := 0
	runeCount := 0

	if absY < scrollbackLen {
		line := grid.ScrollbackLine(absY)
		for _, cell := range line {
			if runeCount >= runeIndex {
				break
			}
			if cell.Width > 0 { // Skip continuation cells
				// Count runes in this cell's content
				for range cell.Content {
					runeCount++
					if runeCount > runeIndex {
						return col
					}
				}
				// Advance column by cell width (handles wide chars like CJK)
				col += cell.Width
			}
		}
	} else {
		screenY := absY - scrollbackLen
		for x := range grid.Text().Width() {
			cell := grid.Text().CellAt(x, screenY)
			if cell != nil && cell.Width > 0 { // Skip continuation cells
				// Count runes in this cell's content
				for range cell.Content {
					runeCount++
					if runeCount > runeIndex {
						return col
					}
				}
				// Advance column by cell width (handles wide chars like CJK)
				col += cell.Width
			}
		}
	}

	return col
}
