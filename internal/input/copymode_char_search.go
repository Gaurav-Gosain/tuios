package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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
			if cell.Width > 0 {
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
			if cell != nil && cell.Width > 0 {
				for range cell.Content {
					runeIndex++
				}
				currentCol += cell.Width
			}
		}
	}

	return runeIndex
}

func findCharOnLine(cm *terminal.CopyMode, grid terminal.CopyGrid, char rune, direction int, till bool) {
	startAbsY := getAbsoluteY(cm, grid)
	scrollbackLen := grid.ScrollbackLen()
	screenHeight := grid.Text().Height()
	totalLines := scrollbackLen + screenHeight

	maxIterations := 1000

	if direction > 0 {
		for lineOffset := range maxIterations {
			absY := startAbsY + lineOffset
			if absY >= totalLines {
				break
			}

			lineText := getLineText(cm, grid, absY)
			if lineText == "" {
				continue
			}

			runes := []rune(lineText)

			startCharIdx := 0
			if lineOffset == 0 {
				currentRuneIdx := convertColumnToRuneIndex(grid, absY, cm.CursorX)
				startCharIdx = currentRuneIdx + 1
			}

			for charIdx := startCharIdx; charIdx < len(runes); charIdx++ {
				if runes[charIdx] == char {
					targetCharIdx := charIdx
					if till {
						targetCharIdx = charIdx - 1
						if targetCharIdx < 0 {
							continue
						}
					}

					linesToMove := absY - startAbsY
					for range linesToMove {
						moveDown(cm, grid)
					}

					cm.CursorX = convertRuneIndexToColumn(grid, absY, targetCharIdx)
					return
				}
			}
		}
	} else {
		for lineOffset := range maxIterations {
			absY := startAbsY - lineOffset
			if absY < 0 {
				break
			}

			lineText := getLineText(cm, grid, absY)
			if lineText == "" {
				continue
			}

			runes := []rune(lineText)

			endCharIdx := len(runes) - 1
			if lineOffset == 0 {
				currentRuneIdx := convertColumnToRuneIndex(grid, absY, cm.CursorX)
				endCharIdx = currentRuneIdx - 1
			}

			for charIdx := endCharIdx; charIdx >= 0; charIdx-- {
				if runes[charIdx] == char {
					targetCharIdx := charIdx
					if till {
						targetCharIdx = charIdx + 1
						if targetCharIdx >= len(runes) {
							continue
						}
					}

					linesToMove := startAbsY - absY
					for range linesToMove {
						moveUp(cm, grid)
					}

					cm.CursorX = convertRuneIndexToColumn(grid, absY, targetCharIdx)
					return
				}
			}
		}
	}
}

func repeatCharSearch(cm *terminal.CopyMode, grid terminal.CopyGrid, reverse bool) {
	if cm.LastCharSearch == 0 {
		return
	}

	direction := cm.LastCharSearchDir
	if reverse {
		direction = -direction
	}

	findCharOnLine(cm, grid, cm.LastCharSearch, direction, cm.LastCharSearchTill)
}

func convertRuneIndexToColumn(grid terminal.CopyGrid, absY int, runeIndex int) int {
	scrollbackLen := grid.ScrollbackLen()

	if runeIndex < 0 {
		return 0
	}

	col := 0
	runeCount := 0

	if absY < scrollbackLen {
		line := grid.ScrollbackLine(absY)
		for _, cell := range line {
			if runeCount >= runeIndex {
				break
			}
			if cell.Width > 0 {
				for range cell.Content {
					runeCount++
					if runeCount > runeIndex {
						return col
					}
				}
				col += cell.Width
			}
		}
	} else {
		screenY := absY - scrollbackLen
		for x := range grid.Text().Width() {
			cell := grid.Text().CellAt(x, screenY)
			if cell != nil && cell.Width > 0 {
				for range cell.Content {
					runeCount++
					if runeCount > runeIndex {
						return col
					}
				}
				col += cell.Width
			}
		}
	}

	return col
}
