// Package input implements vim-style copy mode for TUIOS.
package input

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Search-related functions for copy mode (/, ?, n, N, etc.)

// executeSearch performs a search operation and updates matches
func executeSearch(cm *terminal.CopyMode, window *terminal.Window) {
	// Check cache
	if cm.SearchQuery != "" && cm.SearchQuery == cm.SearchCache.Query && cm.SearchCache.Valid {
		cm.SearchMatches = cm.SearchCache.Matches
		if len(cm.SearchMatches) > 0 {
			cm.CurrentMatch = 0
			jumpToMatch(cm, window, 0)
		}
		return
	}

	cm.SearchMatches = nil
	if cm.SearchQuery == "" {
		return
	}

	query := cm.SearchQuery
	if !cm.CaseSensitive {
		query = strings.ToLower(query)
	}

	scrollbackLen := window.ScrollbackLen()
	screenHeight := window.Terminal.Height()

	// Search scrollback
	for i := range scrollbackLen {
		line := window.ScrollbackLine(i)
		if line == nil {
			continue
		}
		lineText := extractLineTextFromCells(line)

		if !cm.CaseSensitive {
			lineText = strings.ToLower(lineText)
		}

		// Find all occurrences
		// Note: strings.Index returns BYTE positions, not character positions
		byteIdx := 0
		queryCharLen := len([]rune(query)) // Character length, not byte length

		for {
			idx := strings.Index(lineText[byteIdx:], query)
			if idx == -1 {
				break
			}

			// Convert byte positions to character positions
			bytePos := byteIdx + idx
			charStart := byteIndexToCharIndex(lineText, bytePos)
			charEnd := charStart + queryCharLen

			// Convert character indices to column positions
			colStart := charIndexToColumn(line, charStart)
			colEnd := charIndexToColumn(line, charEnd)

			match := terminal.SearchMatch{
				Line:   i,
				StartX: colStart,
				EndX:   colEnd,
			}
			cm.SearchMatches = append(cm.SearchMatches, match)

			// Move to next position (in bytes)
			byteIdx = bytePos + len(query)

			// Limit matches
			if len(cm.SearchMatches) >= 1000 {
				break
			}
		}
		if len(cm.SearchMatches) >= 1000 {
			break
		}
	}

	// Search current screen
	if len(cm.SearchMatches) < 1000 {
		for y := range screenHeight {
			lineText := extractScreenLineText(window.Terminal, y)

			if !cm.CaseSensitive {
				lineText = strings.ToLower(lineText)
			}

			// Note: strings.Index returns BYTE positions, not character positions
			byteIdx := 0
			queryCharLen := len([]rune(query)) // Character length, not byte length

			for {
				idx := strings.Index(lineText[byteIdx:], query)
				if idx == -1 {
					break
				}

				// Convert byte positions to character positions
				bytePos := byteIdx + idx
				charStart := byteIndexToCharIndex(lineText, bytePos)
				charEnd := charStart + queryCharLen

				// Get cells for this screen line to calculate columns
				cells := getScreenLineCells(window.Terminal, y)
				colStart := charIndexToColumn(cells, charStart)
				colEnd := charIndexToColumn(cells, charEnd)

				match := terminal.SearchMatch{
					Line:   scrollbackLen + y,
					StartX: colStart,
					EndX:   colEnd,
				}
				cm.SearchMatches = append(cm.SearchMatches, match)

				// Move to next position (in bytes)
				byteIdx = bytePos + len(query)

				if len(cm.SearchMatches) >= 1000 {
					break
				}
			}
			if len(cm.SearchMatches) >= 1000 {
				break
			}
		}
	}

	// Update cache
	cm.SearchCache.Query = cm.SearchQuery
	cm.SearchCache.Matches = cm.SearchMatches
	cm.SearchCache.CacheTime = time.Now()
	cm.SearchCache.Valid = true

	// Jump to appropriate match based on search direction and current position
	if len(cm.SearchMatches) > 0 {
		currentAbsY := getAbsoluteY(cm, window)

		if cm.SearchBackward {
			// For backward search (?), find the closest match before current position
			// Start from the end and work backwards
			foundMatch := -1
			for i := len(cm.SearchMatches) - 1; i >= 0; i-- {
				match := cm.SearchMatches[i]
				if match.Line < currentAbsY || (match.Line == currentAbsY && match.StartX < cm.CursorX) {
					foundMatch = i
					break
				}
			}

			// If no match before cursor, wrap to last match
			if foundMatch == -1 {
				foundMatch = len(cm.SearchMatches) - 1
			}

			cm.CurrentMatch = foundMatch
			jumpToMatch(cm, window, foundMatch)
		} else {
			// For forward search (/), find the closest match after current position
			foundMatch := -1
			for i := range len(cm.SearchMatches) {
				match := cm.SearchMatches[i]
				if match.Line > currentAbsY || (match.Line == currentAbsY && match.StartX > cm.CursorX) {
					foundMatch = i
					break
				}
			}

			// If no match after cursor, wrap to first match
			if foundMatch == -1 {
				foundMatch = 0
			}

			cm.CurrentMatch = foundMatch
			jumpToMatch(cm, window, foundMatch)
		}
	}
}

// nextMatch jumps to next search match
func nextMatch(cm *terminal.CopyMode, window *terminal.Window) {
	if len(cm.SearchMatches) == 0 {
		return
	}

	cm.CurrentMatch = (cm.CurrentMatch + 1) % len(cm.SearchMatches)
	jumpToMatch(cm, window, cm.CurrentMatch)
}

// prevMatch jumps to previous search match
func prevMatch(cm *terminal.CopyMode, window *terminal.Window) {
	if len(cm.SearchMatches) == 0 {
		return
	}

	cm.CurrentMatch--
	if cm.CurrentMatch < 0 {
		cm.CurrentMatch = len(cm.SearchMatches) - 1
	}
	jumpToMatch(cm, window, cm.CurrentMatch)
}

// jumpToMatch jumps cursor to a specific match
func jumpToMatch(cm *terminal.CopyMode, window *terminal.Window, matchIdx int) {
	if matchIdx < 0 || matchIdx >= len(cm.SearchMatches) {
		return
	}

	match := cm.SearchMatches[matchIdx]
	scrollbackLen := window.ScrollbackLen()

	if match.Line < scrollbackLen {
		// Match is in scrollback
		cm.ScrollOffset = scrollbackLen - match.Line
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = 0
	} else {
		// Match is in current screen
		screenLine := match.Line - scrollbackLen
		cm.ScrollOffset = 0
		window.ScrollbackOffset = cm.ScrollOffset // Sync for rendering
		cm.CursorY = min(screenLine, window.Height-3)
	}

	cm.CursorX = match.StartX
}
