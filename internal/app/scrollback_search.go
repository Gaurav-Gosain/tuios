package app

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// ScrollbackSearchMatch represents a match found in the scrollback or screen buffer.
type ScrollbackSearchMatch struct {
	AbsLine int // absolute line (scrollback + screen)
	Col     int // column of match start
	Len     int // length of match in columns
}

// SearchScrollback searches the focused window's scrollback and screen for the given query.
// Returns matches sorted by position (top to bottom, left to right).
// The search is case-insensitive and uses simple substring matching for speed.
func (m *OS) SearchScrollback(query string) []ScrollbackSearchMatch {
	if query == "" {
		return nil
	}

	window := m.GetFocusedWindow()
	if window == nil || window.Terminal == nil {
		return nil
	}

	lowerQuery := strings.ToLower(query)
	var matches []ScrollbackSearchMatch

	scrollbackLen := window.ScrollbackLen()

	// Search scrollback lines
	for i := range scrollbackLen {
		line := window.ScrollbackLine(i)
		if line == nil {
			continue
		}
		lineText := extractCellText(line)
		lowerLine := strings.ToLower(lineText)

		if !strings.Contains(lowerLine, lowerQuery) {
			continue
		}

		// Find all occurrences
		queryRunes := []rune(lowerQuery)
		lineRunes := []rune(lowerLine)
		qLen := len(queryRunes)

		for pos := 0; pos <= len(lineRunes)-qLen; pos++ {
			if string(lineRunes[pos:pos+qLen]) == string(queryRunes) {
				// Convert rune position to column using cell widths
				col := runeIndexToColumn(line, pos)
				matchLen := runeIndexToColumn(line, pos+qLen) - col
				matches = append(matches, ScrollbackSearchMatch{
					AbsLine: i,
					Col:     col,
					Len:     matchLen,
				})
				pos += qLen - 1 // skip past this match
				if len(matches) >= 5000 {
					return matches
				}
			}
		}
	}

	// Search current screen lines
	screenHeight := window.Terminal.Height()
	screenWidth := window.Terminal.Width()
	for y := range screenHeight {
		var sb strings.Builder
		for x := range screenWidth {
			cell := window.Terminal.CellAt(x, y)
			if cell != nil && cell.Content != "" {
				sb.WriteString(string(cell.Content))
			} else {
				sb.WriteByte(' ')
			}
		}
		lineText := strings.TrimRight(sb.String(), " ")
		lowerLine := strings.ToLower(lineText)

		if !strings.Contains(lowerLine, lowerQuery) {
			continue
		}

		queryRunes := []rune(lowerQuery)
		lineRunes := []rune(lowerLine)
		qLen := len(queryRunes)

		// Build a cells slice for column calculation
		cells := make([]cellWidth, screenWidth)
		for x := range screenWidth {
			cell := window.Terminal.CellAt(x, y)
			if cell != nil {
				cells[x] = cellWidth{content: cell.Content, width: cell.Width}
			}
		}

		for pos := 0; pos <= len(lineRunes)-qLen; pos++ {
			if string(lineRunes[pos:pos+qLen]) == string(queryRunes) {
				col := pos // For screen lines, rune index is roughly the column
				matchLen := qLen
				matches = append(matches, ScrollbackSearchMatch{
					AbsLine: scrollbackLen + y,
					Col:     col,
					Len:     matchLen,
				})
				pos += qLen - 1
				if len(matches) >= 5000 {
					return matches
				}
			}
		}
	}

	return matches
}

// cellWidth is a helper for column calculation from screen cells.
type cellWidth struct {
	content string
	width   int
}

// extractCellText extracts the text content from a line of cells.
func extractCellText(cells uv.Line) string {
	var sb strings.Builder
	for _, cell := range cells {
		if cell.Content != "" {
			sb.WriteString(string(cell.Content))
		} else {
			sb.WriteByte(' ')
		}
	}
	return strings.TrimRight(sb.String(), " ")
}

// runeIndexToColumn converts a rune index to a column position, accounting for
// wide characters in the cell line.
func runeIndexToColumn(cells uv.Line, runeIdx int) int {
	col := 0
	ri := 0
	for _, cell := range cells {
		if ri >= runeIdx {
			break
		}
		content := string(cell.Content)
		if content == "" {
			content = " "
		}
		runes := []rune(content)
		for range runes {
			ri++
			if ri >= runeIdx {
				w := max(cell.Width, 1)
				col += w
				break
			}
		}
		if ri < runeIdx {
			w := max(cell.Width, 1)
			col += w
		}
	}
	return col
}

// ScrollToSearchMatch scrolls the focused window so the match at the given index is visible.
func (m *OS) ScrollToSearchMatch(idx int) {
	if idx < 0 || idx >= len(m.ScrollbackSearchMatches) {
		return
	}
	match := m.ScrollbackSearchMatches[idx]
	window := m.GetFocusedWindow()
	if window == nil {
		return
	}

	scrollbackLen := window.ScrollbackLen()
	viewportHeight := window.Terminal.Height()

	if match.AbsLine < scrollbackLen {
		// Match is in scrollback — set offset so the match line is roughly centered
		offset := scrollbackLen - match.AbsLine
		// Center the match in the viewport
		offset -= viewportHeight / 2
		if offset < 0 {
			offset = 0
		}
		// Make sure we don't scroll past the beginning
		if offset > scrollbackLen {
			offset = scrollbackLen
		}
		window.ScrollbackOffset = offset
		window.ScrollbackMode = true
	} else {
		// Match is on the current screen
		window.ScrollbackOffset = 0
		window.ScrollbackMode = false
	}
	window.InvalidateCache()
}

// renderScrollbackSearchBar renders the search bar at the bottom of the screen.
func (m *OS) renderScrollbackSearchBar() string {
	barStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#3a3a00")).
		Foreground(lipgloss.Color("#ffcc00")).
		Bold(true)

	matchInfo := ""
	if m.ScrollbackSearchQuery != "" {
		total := len(m.ScrollbackSearchMatches)
		if total > 0 {
			current := m.ScrollbackSearchCurrent + 1
			matchInfo = fmt.Sprintf(" [%d/%d]", current, total)
		} else {
			matchInfo = " [no matches]"
		}
	}

	prompt := "/" + m.ScrollbackSearchQuery + matchInfo

	// Pad to full width
	width := m.GetRenderWidth()
	if len(prompt) < width {
		prompt += strings.Repeat(" ", width-len(prompt))
	}

	return barStyle.Render(prompt)
}
