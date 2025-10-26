package app

import (
	"fmt"
	"image/color"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	// LeftHalfCircle is the Unicode character for a left half circle.
	LeftHalfCircle string = string(rune(0xe0b6))
	// RightHalfCircle is the Unicode character for a right half circle.
	RightHalfCircle string = string(rune(0xe0b4))
	// BorderTopLeft is the Unicode character for the top-left border.
	BorderTopLeft string = string(rune(0x256d))
	// BorderTopRight is the Unicode character for the top-right border.
	BorderTopRight string = string(rune(0x256e))
	// BorderHorizontal is the Unicode character for a horizontal border.
	BorderHorizontal string = string(rune(0x2500))
)

// Style cache for common border elements
var (
	baseButtonStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000"))
)

// RightString returns a right-aligned string with decorative borders.
func RightString(str string, width int, color color.Color) string {
	spaces := width - lipgloss.Width(str)
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	fg := style.Foreground(color)

	if spaces < 0 {
		return ""
	}

	return fg.Render(BorderTopLeft+strings.Repeat(BorderHorizontal, spaces)) +
		str +
		fg.Render(BorderTopRight)
}

func makeRounded(content string, color color.Color) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	render := style.Foreground(color).Render
	content = render(LeftHalfCircle) + content + render(RightHalfCircle)
	return content
}

func addToBorder(content string, color color.Color, window *terminal.Window, isRenaming bool, renameBuffer string, isTiling bool) string {
	width := max(
		// Ensure width is never negative
		lipgloss.Width(content)-2, 0)

	// Use cached base style with color applied
	buttonStyle := baseButtonStyle.Background(color)
	cross := buttonStyle.Render(" ⤫ ")
	dash := buttonStyle.Render(" — ")

	// Only show maximize button if not in tiling mode
	var border string
	if isTiling {
		// Use string concatenation instead of JoinHorizontal
		border = makeRounded(dash+cross, color)
	} else {
		square := buttonStyle.Render(" □ ")
		border = makeRounded(dash+square+cross, color)
	}
	centered := RightString(border, width, color)

	// DEBUG: Log button positions
	if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" {
		if f, err := os.OpenFile("/tmp/tuios-render-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			borderWidth := lipgloss.Width(border)
			buttonStartX := window.X + 1 + (width - borderWidth)
			fmt.Fprintf(f, "[RENDER DEBUG] Window %s at X=%d Y=%d Width=%d, border width=%d, buttons start at X=%d\n",
				window.ID, window.X, window.Y, window.Width, borderWidth, buttonStartX)
			fmt.Fprintf(f, "[RENDER DEBUG] Title bar is at Y=%d, dash=%q cross=%q\n", window.Y, dash, cross)
			f.Close()
		}
	}

	// Add bottom border with window name
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	bottomBorderStyle := style.Foreground(color)

	// Get the window name to display (only show custom names)
	windowName := ""
	if window.CustomName != "" {
		windowName = window.CustomName
	}

	// If renaming, show the rename buffer with cursor
	if isRenaming {
		windowName = renameBuffer + "_"
	}

	// Only process name if we have one to show
	var bottomBorder string
	if windowName != "" {
		// Truncate if too long (leave space for pill style)
		maxNameLen := width - 6 // Space for circles and padding
		if maxNameLen > 0 && len(windowName) > maxNameLen {
			if maxNameLen > 3 {
				windowName = windowName[:maxNameLen-3] + "..."
			} else {
				windowName = "..."
			}
		}

		// Create pill-style name badge using cached base style
		nameStyle := baseButtonStyle.Background(color)

		leftCircle := bottomBorderStyle.Render(LeftHalfCircle)
		nameText := nameStyle.Render(" " + windowName + " ")
		rightCircle := bottomBorderStyle.Render(RightHalfCircle)
		nameBadge := leftCircle + nameText + rightCircle

		// Calculate padding for centering the badge
		badgeWidth := lipgloss.Width(nameBadge)
		totalPadding := width - badgeWidth

		// Ensure padding is never negative
		if totalPadding < 0 {
			// If badge is too wide, just use plain border
			bottomBorder = bottomBorderStyle.Render("╰" + strings.Repeat("─", width) + "╯")
		} else {
			leftPadding := totalPadding / 2
			rightPadding := totalPadding - leftPadding

			// Create bottom border with centered name badge
			bottomBorder = bottomBorderStyle.Render("╰"+strings.Repeat("─", leftPadding)) +
				nameBadge +
				bottomBorderStyle.Render(strings.Repeat("─", rightPadding)+"╯")
		}
	} else {
		// Plain bottom border without name
		bottomBorder = bottomBorderStyle.Render("╰" + strings.Repeat("─", width) + "╯")
	}

	// Use string concatenation instead of lipgloss.JoinVertical for better performance
	// Replace the last line (original bottom border from box.Render) with our custom bottom border
	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		lines[len(lines)-1] = bottomBorder
	}
	return centered + "\n" + strings.Join(lines, "\n")
}

// styleToANSI converts a lipgloss.Style to ANSI codes using the internal ansi.Style
// This bypasses all the border/padding/width/alignment logic in Style.Render()
func styleToANSI(s lipgloss.Style) (prefix string, suffix string) {
	var te ansi.Style

	// Extract colors directly
	fg := s.GetForeground()
	bg := s.GetBackground()

	if _, ok := fg.(lipgloss.NoColor); !ok && fg != nil {
		te = te.ForegroundColor(ansi.Color(fg))
	}
	if _, ok := bg.(lipgloss.NoColor); !ok && bg != nil {
		te = te.BackgroundColor(ansi.Color(bg))
	}

	// Extract text attributes
	if s.GetBold() {
		te = te.Bold()
	}
	if s.GetItalic() {
		te = te.Italic()
	}
	if s.GetUnderline() {
		te = te.Underline()
	}
	if s.GetStrikethrough() {
		te = te.Strikethrough()
	}
	if s.GetBlink() {
		te = te.SlowBlink()
	}
	if s.GetFaint() {
		te = te.Faint()
	}
	if s.GetReverse() {
		te = te.Reverse()
	}

	// Convert to string and split into prefix/suffix
	ansiStr := te.String()
	if ansiStr != "" {
		return ansiStr, "\x1b[0m"
	}
	return "", ""
}

// renderStyledText applies ANSI styling to text without using Style.Render()
func renderStyledText(style lipgloss.Style, text string) string {
	prefix, suffix := styleToANSI(style)
	if prefix == "" {
		return text
	}
	return prefix + text + suffix
}

func (m *OS) renderTerminal(window *terminal.Window, isFocused bool, inTerminalMode bool) string {
	// Smart caching: use cache if window is being manipulated OR if content hasn't changed
	if (window.IsBeingManipulated || !window.ContentDirty) && window.CachedContent != "" {
		return window.CachedContent
	}

	// For non-focused windows with rapidly changing content, use cache more aggressively
	if !isFocused && window.CachedContent != "" && len(window.CachedContent) > 0 {
		// Only update non-focused windows every few frames to reduce CPU usage
		return window.CachedContent
	}

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	if window.Terminal == nil {
		window.CachedContent = "Terminal not initialized"
		return window.CachedContent
	}

	screen := window.Terminal
	if screen == nil {
		window.CachedContent = "No screen"
		return window.CachedContent
	}

	// Get cursor position
	cursor := screen.CursorPosition()
	cursorX := cursor.X
	cursorY := cursor.Y

	// Use string builder pool for efficient string building
	builder := pool.GetStringBuilder()
	defer pool.PutStringBuilder(builder)

	// Pre-allocate capacity for better performance
	estimatedSize := (window.Width - 2) * (window.Height - 2)
	builder.Grow(estimatedSize)

	// Build the terminal output with colors and styling
	maxY := min(window.Height-2, screen.Height())
	maxX := min(window.Width-2, screen.Width())

	// Use optimized rendering for background windows (preserve colors but skip expensive operations)
	useOptimizedRendering := !isFocused && !inTerminalMode

	// Determine if we're in scrollback mode or viewing scrollback (e.g., in selection mode)
	scrollbackLen := window.ScrollbackLen()
	inScrollbackMode := window.ScrollbackOffset > 0

	// Check if in copy mode and get cursor position
	inCopyMode := window.CopyMode != nil && window.CopyMode.Active
	var copyModeCursorX, copyModeCursorY int
	if inCopyMode {
		copyModeCursorX = window.CopyMode.CursorX
		copyModeCursorY = window.CopyMode.CursorY
	}

	// Build search match highlight maps for copy mode
	var searchHighlights map[int]map[int]bool      // All matches - yellow
	var currentMatchHighlight map[int]map[int]bool // Current match - orange

	if inCopyMode && len(window.CopyMode.SearchMatches) > 0 {
		searchHighlights = make(map[int]map[int]bool)
		currentMatchHighlight = make(map[int]map[int]bool)

		for i, match := range window.CopyMode.SearchMatches {
			// Convert absolute line to viewport Y
			var viewportY int
			if match.Line < scrollbackLen {
				// Match in scrollback
				if window.ScrollbackOffset > 0 {
					if match.Line >= scrollbackLen-window.ScrollbackOffset {
						viewportY = match.Line - (scrollbackLen - window.ScrollbackOffset)
					} else {
						continue // Not visible
					}
				} else {
					continue // Scrollback not visible
				}
			} else {
				// Match in current screen
				screenLine := match.Line - scrollbackLen
				if window.ScrollbackOffset > 0 {
					viewportY = window.ScrollbackOffset + screenLine
				} else {
					viewportY = screenLine
				}
			}

			// Check if visible
			if viewportY >= 0 && viewportY < maxY {
				isCurrentMatch := (i == window.CopyMode.CurrentMatch)

				if isCurrentMatch {
					if currentMatchHighlight[viewportY] == nil {
						currentMatchHighlight[viewportY] = make(map[int]bool)
					}
				} else {
					if searchHighlights[viewportY] == nil {
						searchHighlights[viewportY] = make(map[int]bool)
					}
				}

				// Mark all cells in match range
				for x := match.StartX; x < match.EndX && x < maxX; x++ {
					if isCurrentMatch {
						currentMatchHighlight[viewportY][x] = true
					} else {
						searchHighlights[viewportY][x] = true
					}
				}
			}
		}
	}

	// Build visual selection map for copy mode
	var visualSelection map[int]map[int]bool
	inVisualMode := inCopyMode &&
		(window.CopyMode.State == terminal.CopyModeVisualChar ||
			window.CopyMode.State == terminal.CopyModeVisualLine)

	if inVisualMode {
		visualSelection = make(map[int]map[int]bool)

		start := window.CopyMode.VisualStart
		end := window.CopyMode.VisualEnd

		// Normalize selection
		if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
			start, end = end, start
		}

		for absY := start.Y; absY <= end.Y; absY++ {
			// Convert absolute Y to viewport Y
			var viewportY int
			if absY < scrollbackLen {
				// Selection in scrollback
				if window.ScrollbackOffset > 0 {
					if absY >= scrollbackLen-window.ScrollbackOffset {
						viewportY = absY - (scrollbackLen - window.ScrollbackOffset)
					} else {
						continue
					}
				} else {
					continue
				}
			} else {
				// Selection in screen
				screenY := absY - scrollbackLen
				if window.ScrollbackOffset > 0 {
					viewportY = window.ScrollbackOffset + screenY
				} else {
					viewportY = screenY
				}
			}

			if viewportY >= 0 && viewportY < maxY {
				if visualSelection[viewportY] == nil {
					visualSelection[viewportY] = make(map[int]bool)
				}

				startX, endX := 0, maxX-1
				if absY == start.Y {
					startX = start.X
				}
				if absY == end.Y {
					endX = end.X
				}

				for x := startX; x <= endX && x < maxX; x++ {
					visualSelection[viewportY][x] = true
				}
			}
		}
	}

	// Batching approach: accumulate runs of same-styled characters
	var batchBuilder strings.Builder
	var currentStyle lipgloss.Style
	var batchHasStyle bool
	var prevCell *uv.Cell
	var prevIsCursor, prevIsSelected, prevIsSelectionCursor bool

	// Helper to flush the current batch
	flushBatch := func(lineBuilder *strings.Builder) {
		if batchBuilder.Len() > 0 {
			if batchHasStyle {
				lineBuilder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
			} else {
				lineBuilder.WriteString(batchBuilder.String())
			}
			batchBuilder.Reset()
			batchHasStyle = false
		}
	}

	// Helper to check if style matches previous
	styleMatches := func(cell *uv.Cell, isCursorPos, isSelected, isSelectionCursor bool) bool {
		if prevCell == nil && cell == nil {
			return prevIsCursor == isCursorPos && prevIsSelected == isSelected && prevIsSelectionCursor == isSelectionCursor
		}
		if prevCell == nil || cell == nil {
			return false
		}
		return prevIsCursor == isCursorPos &&
			prevIsSelected == isSelected &&
			prevIsSelectionCursor == isSelectionCursor &&
			prevCell.Style.Fg == cell.Style.Fg &&
			prevCell.Style.Bg == cell.Style.Bg &&
			prevCell.Style.Attrs == cell.Style.Attrs
	}

	for y := range maxY {
		if y > 0 {
			builder.WriteRune('\n')
		}

		// Use line builder for all windows to preserve styling
		lineBuilder := pool.GetStringBuilder()
		defer pool.PutStringBuilder(lineBuilder)

		// Reset batch state at start of line
		batchBuilder.Reset()
		batchHasStyle = false
		prevCell = nil

		// Calculate line end position once per line for visual selection highlighting
		var lineEndX int = maxX - 1
		if inVisualMode && visualSelection[y] != nil {
			// Find the last non-empty character on this line by checking cells directly
			// We need to determine which line we're rendering
			if inScrollbackMode {
				if y < window.ScrollbackOffset {
					// Scrollback line
					scrollbackIndex := scrollbackLen - window.ScrollbackOffset + y
					if scrollbackIndex >= 0 && scrollbackIndex < scrollbackLen {
						lineCells := window.ScrollbackLine(scrollbackIndex)
						if lineCells != nil {
							for i := len(lineCells) - 1; i >= 0; i-- {
								if lineCells[i].Width > 0 && lineCells[i].Content != "" && lineCells[i].Content != " " {
									lineEndX = i
									break
								}
							}
						}
					}
				} else {
					// Screen line
					screenY := y - window.ScrollbackOffset
					if screenY >= 0 && screenY < screen.Height() {
						for i := maxX - 1; i >= 0; i-- {
							cell := screen.CellAt(i, screenY)
							if cell != nil && cell.Width > 0 && cell.Content != "" && cell.Content != " " {
								lineEndX = i
								break
							}
						}
					}
				}
			} else {
				// Normal mode - just current screen
				for i := maxX - 1; i >= 0; i-- {
					cell := screen.CellAt(i, y)
					if cell != nil && cell.Width > 0 && cell.Content != "" && cell.Content != " " {
						lineEndX = i
						break
					}
				}
			}
		}

		for x := 0; x < maxX; {
			var cell *uv.Cell

			// Render copy mode cursor as highlighted cell (takes priority over everything)
			if inCopyMode && x == copyModeCursorX && y == copyModeCursorY {
				// Skip getting cell from scrollback, just render cursor
				char := " "
				var cursorCell *uv.Cell
				charWidth := 1

				// Try to get the actual character at cursor position
				// Calculate which line to render based on scrollback offset
				if inScrollbackMode {
					if y < window.ScrollbackOffset {
						// Show scrollback content at the top
						scrollbackIndex := scrollbackLen - window.ScrollbackOffset + y
						if scrollbackIndex >= 0 && scrollbackIndex < scrollbackLen {
							scrollbackLine := window.ScrollbackLine(scrollbackIndex)
							if scrollbackLine != nil && x < len(scrollbackLine) {
								cursorCell = &scrollbackLine[x]
								if cursorCell.Content != "" {
									char = cursorCell.Content
								}
								if cursorCell.Width > 0 {
									charWidth = cursorCell.Width
								}
							}
						}
					} else {
						// Show current screen content at the bottom
						screenY := y - window.ScrollbackOffset
						if screenY >= 0 && screenY < screen.Height() {
							cursorCell = screen.CellAt(x, screenY)
							if cursorCell != nil && cursorCell.Content != "" {
								char = cursorCell.Content
							}
							if cursorCell != nil && cursorCell.Width > 0 {
								charWidth = cursorCell.Width
							}
						}
					}
				} else {
					// Normal mode - just render current screen
					cursorCell = screen.CellAt(x, y)
					if cursorCell != nil && cursorCell.Content != "" {
						char = cursorCell.Content
					}
					if cursorCell != nil && cursorCell.Width > 0 {
						charWidth = cursorCell.Width
					}
				}

				// Render cursor with cyan/teal background (less harsh than green)
				cursorStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#00D7FF")). // Bright cyan
					Foreground(lipgloss.Color("#000000")).
					Bold(true)

				// Flush any pending batch before cursor
				if batchBuilder.Len() > 0 {
					if batchHasStyle {
						lineBuilder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
					} else {
						lineBuilder.WriteString(batchBuilder.String())
					}
					batchBuilder.Reset()
					batchHasStyle = false
				}

				// Render cursor
				lineBuilder.WriteString(renderStyledText(cursorStyle, char))

				// Reset prev cell state
				prevCell = nil
				prevIsCursor = false
				prevIsSelected = false
				prevIsSelectionCursor = false

				// Increment x by character width (handles wide characters correctly)
				x += charWidth
				continue
			}

			// Calculate which line to render based on scrollback offset
			if inScrollbackMode {
				// We're viewing scrollback
				// ScrollbackOffset is how many lines back from the bottom we are
				// Show scrollback at the top, current screen at the bottom

				if y < window.ScrollbackOffset {
					// Show scrollback content at the top
					// y=0 shows the oldest line in our view (ScrollbackOffset lines back)
					// y=ScrollbackOffset-1 shows the newest scrollback line
					scrollbackIndex := scrollbackLen - window.ScrollbackOffset + y
					if scrollbackIndex >= 0 && scrollbackIndex < scrollbackLen {
						scrollbackLine := window.ScrollbackLine(scrollbackIndex)
						if scrollbackLine != nil && x < len(scrollbackLine) {
							cell = &scrollbackLine[x]
						}
					}
				} else {
					// Show current screen content at the bottom
					screenY := y - window.ScrollbackOffset
					if screenY >= 0 && screenY < screen.Height() {
						cell = screen.CellAt(x, screenY)
					}
				}
			} else {
				// Normal mode - just render current screen
				cell = screen.CellAt(x, y)
			}

			// Get the character to display
			char := " "
			if cell != nil && cell.Content != "" {
				char = string(cell.Content)
			}

			// COPY MODE RENDERING (priority order: selection < search < cursor)

			// Check visual selection highlighting (lowest priority)
			// Only highlight if we're at or before the last non-empty character (no trailing empty cells)
			if inVisualMode && visualSelection[y] != nil && visualSelection[y][x] && x <= lineEndX {
				selStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#5F5FAF")). // Purple-ish blue
					Foreground(lipgloss.Color("#FFFFFF")).
					Bold(true)

				// Flush any pending batch
				if batchBuilder.Len() > 0 {
					if batchHasStyle {
						lineBuilder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
					} else {
						lineBuilder.WriteString(batchBuilder.String())
					}
					batchBuilder.Reset()
					batchHasStyle = false
				}

				lineBuilder.WriteString(renderStyledText(selStyle, char))
				prevCell = cell
				prevIsCursor = false
				prevIsSelected = false
				prevIsSelectionCursor = false
				// Increment x before continuing (prevent infinite loop)
				cellWidth := 1
				if cell != nil && cell.Width > 1 {
					cellWidth = cell.Width
				}
				x += cellWidth
				continue
			}

			// Check search highlighting (medium priority)
			if inCopyMode && !inVisualMode {
				if currentMatchHighlight[y] != nil && currentMatchHighlight[y][x] {
					// Current match - bright magenta/pink
					matchStyle := lipgloss.NewStyle().
						Background(lipgloss.Color("#FF00FF")).
						Foreground(lipgloss.Color("#000000")).
						Bold(true)

					// Flush any pending batch
					if batchBuilder.Len() > 0 {
						if batchHasStyle {
							lineBuilder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
						} else {
							lineBuilder.WriteString(batchBuilder.String())
						}
						batchBuilder.Reset()
						batchHasStyle = false
					}

					lineBuilder.WriteString(renderStyledText(matchStyle, char))
					prevCell = cell
					prevIsCursor = false
					prevIsSelected = false
					prevIsSelectionCursor = false
					// Increment x before continuing (prevent infinite loop)
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}

				if searchHighlights[y] != nil && searchHighlights[y][x] {
					// Other matches - orange
					matchStyle := lipgloss.NewStyle().
						Background(lipgloss.Color("#FF8700")).
						Foreground(lipgloss.Color("#000000"))

					// Flush any pending batch
					if batchBuilder.Len() > 0 {
						if batchHasStyle {
							lineBuilder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
						} else {
							lineBuilder.WriteString(batchBuilder.String())
						}
						batchBuilder.Reset()
						batchHasStyle = false
					}

					lineBuilder.WriteString(renderStyledText(matchStyle, char))
					prevCell = cell
					prevIsCursor = false
					prevIsSelected = false
					prevIsSelectionCursor = false
					// Increment x before continuing (prevent infinite loop)
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}
			}

			// Check if current position is within selection (either actively selecting or has selected text)
			isSelected := (window.IsSelecting || window.SelectedText != "") && m.isPositionInSelection(window, x, y)
			// Don't render terminal cursor when in copy mode
			isCursorPos := isFocused && inTerminalMode && !inCopyMode && x == cursorX && y == cursorY

			// Check if current position is the selection cursor (only in selection mode and NOT in terminal mode)
			isSelectionCursor := m.SelectionMode && !inTerminalMode && isFocused &&
				x == window.SelectionCursor.X && y == window.SelectionCursor.Y

			// Determine if we need styling
			needsStyling := shouldApplyStyle(cell) || isCursorPos || isSelected || isSelectionCursor

			// Check if style changed
			if x > 0 && !styleMatches(cell, isCursorPos, isSelected, isSelectionCursor) {
				flushBatch(lineBuilder)
			}

			if needsStyling {
				// Build style for this batch if starting new batch
				if batchBuilder.Len() == 0 {
					// Use cached styles for better performance
					if isSelected || isSelectionCursor {
						// Selection highlighting needs to be applied on top, so build base style first
						if useOptimizedRendering {
							currentStyle = buildOptimizedCellStyleCached(cell)
						} else {
							currentStyle = buildCellStyleCached(cell, isCursorPos)
						}

						// Apply selection highlighting
						if isSelected {
							currentStyle = currentStyle.Background(lipgloss.Color("62")).Foreground(lipgloss.Color("15")) // Blue background, white text
						}

						// Apply selection cursor highlighting
						if isSelectionCursor {
							currentStyle = currentStyle.Background(lipgloss.Color("208")).Foreground(lipgloss.Color("0")) // Orange background, black text
						}
					} else {
						// Normal cell rendering - use cached styles directly
						if useOptimizedRendering {
							currentStyle = buildOptimizedCellStyleCached(cell)
						} else {
							currentStyle = buildCellStyleCached(cell, isCursorPos)
						}
					}
					batchHasStyle = true
				}

				batchBuilder.WriteString(char)
			} else {
				// No style - just accumulate plain text
				batchBuilder.WriteString(char)
			}

			// Remember current cell state for next iteration
			prevCell = cell
			prevIsCursor = isCursorPos
			prevIsSelected = isSelected
			prevIsSelectionCursor = isSelectionCursor

			// Increment x by cell width to handle wide characters (e.g., CJK)
			// Wide characters occupy multiple cells but should only be rendered once
			cellWidth := 1
			if cell != nil && cell.Width > 1 {
				cellWidth = cell.Width
			}
			x += cellWidth
		}

		// Flush remaining batch at end of line
		flushBatch(lineBuilder)
		builder.WriteString(lineBuilder.String())
	}

	// Cache the result more intelligently
	content := builder.String()
	window.CachedContent = content
	window.ContentDirty = false // Mark content as clean after rendering
	return content
}

// shouldApplyStyle checks if a cell has styling (optimization #7: early exit for nil)
func shouldApplyStyle(cell *uv.Cell) bool {
	// Most common case: cell is nil
	if cell == nil {
		return false
	}
	// Check in order of likelihood (Fg most common, Bg next, Attrs least)
	return cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0
}

// buildOptimizedCellStyleCached is the cached version of buildOptimizedCellStyle.
// It uses the global style cache to avoid rebuilding identical styles.
func buildOptimizedCellStyleCached(cell *uv.Cell) lipgloss.Style {
	return GetGlobalStyleCache().Get(cell, false, true)
}

// buildCellStyleCached is the cached version of buildCellStyle.
// It uses the global style cache to avoid rebuilding identical styles.
func buildCellStyleCached(cell *uv.Cell, isCursor bool) lipgloss.Style {
	return GetGlobalStyleCache().Get(cell, isCursor, false)
}

func buildOptimizedCellStyle(cell *uv.Cell) lipgloss.Style {
	// Fast styling for background windows - only colors, skip expensive attributes
	cellStyle := lipgloss.NewStyle()

	if cell == nil {
		return cellStyle
	}

	// Apply colors only (preserve the visual appearance)
	// Check for nil and validate the color is usable
	if cell.Style.Fg != nil {
		if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Foreground(ansiColor)
		} else if isColorSafe(cell.Style.Fg) {
			cellStyle = cellStyle.Foreground(cell.Style.Fg)
		}
	}
	if cell.Style.Bg != nil {
		if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Background(ansiColor)
		} else if isColorSafe(cell.Style.Bg) {
			cellStyle = cellStyle.Background(cell.Style.Bg)
		}
	}

	// Skip expensive attributes (bold, italic, etc.) for performance
	// This preserves colors while improving performance significantly

	return cellStyle
}

// isColorSafe checks if a color can be safely used without panicking
func isColorSafe(c color.Color) bool {
	if c == nil {
		return false
	}
	// Try to call RGBA safely with panic recovery
	defer func() {
		_ = recover() // Silently catch any panic from nil pointer dereference
	}()
	_, _, _, _ = c.RGBA()
	return true
}

func buildCellStyle(cell *uv.Cell, isCursor bool) lipgloss.Style {
	// Build style efficiently
	cellStyle := lipgloss.NewStyle()

	if cell == nil {
		return cellStyle
	}

	// Handle cursor rendering first (most common fast path)
	if isCursor {
		// Show cursor by inverting colors
		fg := lipgloss.Color("#FFFFFF")
		bg := lipgloss.Color("#000000")
		if cell.Style.Fg != nil {
			if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
				fg = ansiColor
			} else if isColorSafe(cell.Style.Fg) {
				fg = cell.Style.Fg
			}
		}
		if cell.Style.Bg != nil {
			if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
				bg = ansiColor
			} else if isColorSafe(cell.Style.Bg) {
				bg = cell.Style.Bg
			}
		}
		return cellStyle.Background(fg).Foreground(bg)
	}

	// Apply colors only if needed
	if cell.Style.Fg != nil {
		if ansiColor, ok := cell.Style.Fg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Foreground(ansiColor)
		} else if isColorSafe(cell.Style.Fg) {
			cellStyle = cellStyle.Foreground(cell.Style.Fg)
		}
	}
	if cell.Style.Bg != nil {
		if ansiColor, ok := cell.Style.Bg.(lipgloss.ANSIColor); ok {
			cellStyle = cellStyle.Background(ansiColor)
		} else if isColorSafe(cell.Style.Bg) {
			cellStyle = cellStyle.Background(cell.Style.Bg)
		}
	}

	// Apply attributes only if set (optimize common case)
	if cell.Style.Attrs != 0 {
		attrs := cell.Style.Attrs
		if attrs&1 != 0 { // Bold
			cellStyle = cellStyle.Bold(true)
		}
		if attrs&2 != 0 { // Faint
			cellStyle = cellStyle.Faint(true)
		}
		if attrs&4 != 0 { // Italic
			cellStyle = cellStyle.Italic(true)
		}
		if attrs&32 != 0 { // Reverse
			cellStyle = cellStyle.Reverse(true)
		}
		if attrs&128 != 0 { // Strikethrough
			cellStyle = cellStyle.Strikethrough(true)
		}
	}

	return cellStyle
}

// GetCanvas returns the main rendering canvas with all layers.
func (m *OS) GetCanvas(render bool) *lipgloss.Canvas {
	canvas := lipgloss.NewCanvas()

	// Get layers slice from pool
	layersPtr := pool.GetLayerSlice()
	layers := (*layersPtr)[:0] // Reset length but keep capacity
	defer pool.PutLayerSlice(layersPtr)

	// Pre-compute viewport bounds for culling
	viewportWidth := m.Width
	viewportHeight := m.GetUsableHeight()

	// Create consistent window style
	box := lipgloss.NewStyle().
		Align(lipgloss.Left).
		AlignVertical(lipgloss.Top).
		Foreground(lipgloss.Color("#FFFFFF")).
		Border(lipgloss.RoundedBorder()).
		BorderTop(false)

	for i := range m.Windows {
		window := m.Windows[i]

		// Skip windows not in current workspace
		if window.Workspace != m.CurrentWorkspace {
			continue
		}

		// Check if this window is being animated
		isAnimating := false
		for _, anim := range m.Animations {
			if anim.Window == m.Windows[i] && !anim.Complete {
				isAnimating = true
				break
			}
		}

		// Skip minimized windows unless they're animating
		if window.Minimized && !isAnimating {
			continue
		}

		// Skip minimized windows (they're shown in the dock)
		// No animation needed - minimize is instant

		// Enhanced visibility culling with tighter bounds for better performance
		// Skip windows completely outside viewport (with small margin for animations)
		margin := 5
		if isAnimating {
			margin = 20 // Larger margin for animating windows
		}

		isVisible := window.X+window.Width >= -margin &&
			window.X <= viewportWidth+margin &&
			window.Y+window.Height >= -margin &&
			window.Y <= viewportHeight+margin

		if !isVisible {
			continue
		}

		// Additional optimization: skip expensive operations for barely visible windows
		isFullyVisible := window.X >= 0 && window.Y >= 0 &&
			window.X+window.Width <= viewportWidth &&
			window.Y+window.Height <= viewportHeight

		// Ensure focused window index is valid
		isFocused := m.FocusedWindow == i && m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows)
		borderColor := "#FAAAAA"
		if isFocused {
			borderColor = "#AFFFFF"
			if m.Mode == TerminalMode {
				borderColor = "#AAFFAA" // Green when in terminal mode
			}
		}

		// Enhanced cache checking with early exit for clean windows
		if window.CachedLayer != nil && !window.Dirty && !window.ContentDirty && !window.PositionDirty {
			// Fast path: window is completely clean, use cached layer
			layers = append(layers, window.CachedLayer)
			continue
		}

		// More detailed cache validation only for potentially dirty windows
		needsRedraw := window.CachedLayer == nil ||
			window.Dirty || window.ContentDirty || window.PositionDirty ||
			window.CachedLayer.GetX() != window.X ||
			window.CachedLayer.GetY() != window.Y ||
			window.CachedLayer.GetZ() != window.Z

		// Background window optimization: defer expensive redraws unless critical
		if !needsRedraw || (!isFocused && !isFullyVisible && !window.ContentDirty && window.CachedLayer != nil) {
			layers = append(layers, window.CachedLayer)
			continue
		}

		// Get terminal content
		content := m.renderTerminal(window, isFocused, m.Mode == TerminalMode)

		// Check if this window is being renamed
		isRenaming := m.RenamingWindow && i == m.FocusedWindow

		boxContent := addToBorder(
			box.Width(window.Width).
				Height(window.Height-1).
				BorderForeground(lipgloss.Color(borderColor)).
				Render(content),
			lipgloss.Color(borderColor),
			window,
			isRenaming,
			m.RenameBuffer,
			m.AutoTiling,
		)

		// Give animating windows highest Z-index so they appear on top
		zIndex := window.Z
		if isAnimating {
			zIndex = config.ZIndexAnimating // High z-index for animating windows
		}

		// Cache the layer
		window.CachedLayer = lipgloss.NewLayer(boxContent).X(window.X).Y(window.Y).Z(zIndex).ID(window.ID)
		layers = append(layers, window.CachedLayer)

		// Clear dirty flags after rendering
		window.ClearDirtyFlags()
	}

	if render {
		// Add overlays
		overlays := m.renderOverlays()
		layers = append(layers, overlays...)

		// Always add dock layer (shows empty dock area when no minimized windows)
		dockLayer := m.renderDock()
		layers = append(layers, dockLayer)
	}

	canvas.AddLayers(layers...)
	return canvas
}

func (m *OS) renderOverlays() []*lipgloss.Layer {
	var layers []*lipgloss.Layer

	// Time and status overlay in top-left corner (always visible)
	currentTime := time.Now().Format("15:04:05")
	var statusText string

	if m.PrefixActive {
		// Show prefix indicator with time
		statusText = "PREFIX | " + currentTime
	} else {
		statusText = currentTime
	}

	timeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a0a0b0")).
		Bold(true).
		Padding(0, 1)

	// Highlight when prefix is active
	if m.PrefixActive {
		timeStyle = timeStyle.
			Background(lipgloss.Color("#ff6b6b")).
			Foreground(lipgloss.Color("#ffffff"))
	} else {
		timeStyle = timeStyle.
			Background(lipgloss.Color("#1a1a2e"))
	}

	renderedTime := timeStyle.Render(statusText)

	// Position time in top-left corner
	timeX := 1
	timeLayer := lipgloss.NewLayer(renderedTime).
		X(timeX).
		Y(0).
		Z(config.ZIndexTime). // High Z to appear above windows
		ID("time")

	layers = append(layers, timeLayer)

	// Welcome message when no windows exist
	if len(m.Windows) == 0 {
		// Clean ASCII art with Unicode
		asciiArt := `████████╗██╗   ██╗██╗ ██████╗ ███████╗
╚══██╔══╝██║   ██║██║██╔═══██╗██╔════╝
   ██║   ██║   ██║██║██║   ██║███████╗
   ██║   ██║   ██║██║██║   ██║╚════██║
   ██║   ╚██████╔╝██║╚██████╔╝███████║
   ╚═╝    ╚═════╝ ╚═╝ ╚═════╝ ╚══════╝`

		// Styled title
		title := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render(asciiArt)

		subtitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Render("Terminal UI Operating System")

		instruction := lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Render("Press 'n' to create a window, '?' for help")

		content := lipgloss.JoinVertical(lipgloss.Center,
			title,
			"",
			subtitle,
			"",
			instruction,
		)

		// Simple border with subtle color
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2)

		// Use lipgloss.Place for proper centering
		centeredContent := lipgloss.Place(
			m.Width, m.Height,
			lipgloss.Center, lipgloss.Center,
			boxStyle.Render(content),
		)

		welcomeLayer := lipgloss.NewLayer(centeredContent).
			X(0).Y(0).Z(1).ID("welcome")

		layers = append(layers, welcomeLayer)
	}

	// Help overlay - always available regardless of windows
	if m.ShowHelp {
		// Use new table-based help menu (it handles its own sizing and centering)
		helpContent := m.RenderHelpMenu(m.Width, m.Height)

		helpLayer := lipgloss.NewLayer(helpContent).
			X(0).Y(0).Z(config.ZIndexHelp).ID("help")

		layers = append(layers, helpLayer)
	}

	// Cache statistics overlay
	if m.ShowCacheStats {
		stats := GetGlobalStyleCache().GetStats()

		// Styled cache stats content
		statsTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render("Style Cache Statistics")

		labelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Render

		valueStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true).
			Render

		// Build stats lines
		var statsLines []string
		statsLines = append(statsLines, statsTitle)
		statsLines = append(statsLines, "")
		statsLines = append(statsLines, labelStyle("Hit Rate:      ")+valueStyle(fmt.Sprintf("%.2f%%", stats.HitRate)))
		statsLines = append(statsLines, labelStyle("Cache Hits:    ")+valueStyle(fmt.Sprintf("%d", stats.Hits)))
		statsLines = append(statsLines, labelStyle("Cache Misses:  ")+valueStyle(fmt.Sprintf("%d", stats.Misses)))
		statsLines = append(statsLines, labelStyle("Total Lookups: ")+valueStyle(fmt.Sprintf("%d", stats.Hits+stats.Misses)))
		statsLines = append(statsLines, labelStyle("Evictions:     ")+valueStyle(fmt.Sprintf("%d", stats.Evicts)))
		statsLines = append(statsLines, "")
		statsLines = append(statsLines, labelStyle("Cache Size:    ")+valueStyle(fmt.Sprintf("%d / %d entries", stats.Size, stats.Capacity)))
		statsLines = append(statsLines, labelStyle("Fill Rate:     ")+valueStyle(fmt.Sprintf("%.1f%%", float64(stats.Size)/float64(stats.Capacity)*100.0)))
		statsLines = append(statsLines, "")

		// Performance indicators
		perfLabel := "Performance: "
		var perfText, perfColor string
		if stats.HitRate >= 95.0 {
			perfText = "Excellent"
			perfColor = "10" // Green
		} else if stats.HitRate >= 85.0 {
			perfText = "Good"
			perfColor = "11" // Yellow
		} else if stats.HitRate >= 70.0 {
			perfText = "Fair"
			perfColor = "214" // Orange
		} else {
			perfText = "Poor"
			perfColor = "9" // Red
		}

		statsLines = append(statsLines, labelStyle(perfLabel)+lipgloss.NewStyle().
			Foreground(lipgloss.Color(perfColor)).
			Bold(true).
			Render(perfText))

		// Add hint
		statsLines = append(statsLines, "")
		statsLines = append(statsLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render("Press 'q'/'esc' to exit, 'r' to reset stats"))

		// Join lines
		statsContent := strings.Join(statsLines, "\n")

		// Create bordered box
		statsBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("13")). // Magenta border
			Padding(1, 2).
			Background(lipgloss.Color("#1a1a2a")).
			Render(statsContent)

		// Center the stats viewer
		centeredStats := lipgloss.Place(m.Width, m.Height,
			lipgloss.Center, lipgloss.Center, statsBox)

		statsLayer := lipgloss.NewLayer(centeredStats).
			X(0).Y(0).Z(config.ZIndexLogs).ID("cache-stats") // Use same Z as logs

		layers = append(layers, statsLayer)
	}

	// Log viewer overlay
	if m.ShowLogs {
		// Build log viewer content
		logTitle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Render("System Logs")

		// Calculate available height for content inside the box
		// Border (2) + Padding top/bottom (2) + some margin for centering (4) = 8
		maxDisplayHeight := max(m.Height-8, 8)
		totalLogs := len(m.LogMessages)

		// Calculate how many log lines can fit
		// Fixed overhead: title (1) + blank after title (1) + blank before hint (1) + hint (1) = 4
		fixedLines := 4

		// If scrollable, add scroll indicator: blank (1) + indicator (1) = 2
		if totalLogs > maxDisplayHeight-fixedLines {
			fixedLines = 6 // Add scroll indicator lines
		}

		logsPerPage := maxDisplayHeight - fixedLines
		if logsPerPage < 1 {
			logsPerPage = 1 // Ensure at least 1 log is visible
		}

		// Clamp scroll offset to valid range
		maxScroll := totalLogs - logsPerPage
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.LogScrollOffset > maxScroll {
			m.LogScrollOffset = maxScroll
		}
		if m.LogScrollOffset < 0 {
			m.LogScrollOffset = 0
		}

		var logLines []string
		logLines = append(logLines, logTitle)
		logLines = append(logLines, "")

		// Add log messages with color coding
		startIdx := m.LogScrollOffset

		displayCount := 0
		for i := startIdx; i < len(m.LogMessages) && displayCount < logsPerPage; i++ {
			msg := m.LogMessages[i]

			// Color code by level
			var levelColor string
			switch msg.Level {
			case "ERROR":
				levelColor = "9" // Red
			case "WARN":
				levelColor = "11" // Yellow
			default:
				levelColor = "10" // Green
			}

			timeStr := msg.Time.Format("15:04:05")
			levelStr := lipgloss.NewStyle().
				Foreground(lipgloss.Color(levelColor)).
				Render(fmt.Sprintf("[%s]", msg.Level))

			logLine := fmt.Sprintf("%s %s %s", timeStr, levelStr, msg.Message)
			logLines = append(logLines, logLine)
			displayCount++
		}

		// Add scroll indicator if needed
		if maxScroll > 0 {
			scrollInfo := fmt.Sprintf("Showing %d-%d of %d logs (↑/↓ to scroll)",
				startIdx+1, startIdx+displayCount, len(m.LogMessages))
			logLines = append(logLines, "")
			logLines = append(logLines, lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Render(scrollInfo))
		}

		// Add navigation hint
		logLines = append(logLines, "")
		logLines = append(logLines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Render("Press 'q'/'esc' to exit, j/k or ↑/↓ to scroll"))

		// Join and style
		logContent := strings.Join(logLines, "\n")

		// Create bordered box
		logBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(1, 2).
			Width(80).
			Background(lipgloss.Color("#1a1a2a")).
			Render(logContent)

		// Center the log viewer
		centeredLogs := lipgloss.Place(m.Width, m.Height,
			lipgloss.Center, lipgloss.Center, logBox)

		logLayer := lipgloss.NewLayer(centeredLogs).
			X(0).Y(0).Z(config.ZIndexLogs).ID("logs")

		layers = append(layers, logLayer)
	}

	// Which-key style overlay for prefix commands (appears after delay)
	if m.PrefixActive && !m.ShowHelp && time.Since(m.LastPrefixTime) > config.WhichKeyDelay {
		keyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true)
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

		var helpLines []string
		var title string
		var bindings []config.Keybinding

		if m.WorkspacePrefixActive {
			title = "Workspace Commands:"
			bindings = config.GetPrefixKeybindings("workspace")
		} else if m.MinimizePrefixActive {
			title = "Minimize Commands:"
			bindings = config.GetPrefixKeybindings("minimize")
			// Add count of minimized windows to the 1-9 description
			minimizedCount := 0
			for _, win := range m.Windows {
				if win.Minimized && win.Workspace == m.CurrentWorkspace {
					minimizedCount++
				}
			}
			// Update the description for 1-9 binding
			for i := range bindings {
				if bindings[i].Key == "1-9" {
					bindings[i].Description = fmt.Sprintf("Restore window (%d minimized)", minimizedCount)
					break
				}
			}
		} else if m.TilingPrefixActive {
			title = "Window Commands:"
			bindings = config.GetPrefixKeybindings("window")
		} else if m.DebugPrefixActive {
			title = "Debug Commands:"
			bindings = config.GetPrefixKeybindings("debug")
		} else {
			title = "Prefix Commands:"
			bindings = config.GetPrefixKeybindings("")
		}

		// Build help lines from bindings
		helpLines = append(helpLines, keyStyle.Render(title))
		helpLines = append(helpLines, "")

		// Find max key length for padding
		maxKeyLen := 0
		for _, binding := range bindings {
			if len(binding.Key) > maxKeyLen {
				maxKeyLen = len(binding.Key)
			}
		}

		for _, binding := range bindings {
			padding := maxKeyLen - len(binding.Key) + 2
			if padding < 2 {
				padding = 2
			}
			paddingStr := strings.Repeat(" ", padding)
			helpLines = append(helpLines, keyStyle.Render(binding.Key)+paddingStr+descStyle.Render(binding.Description))
		}

		content := lipgloss.JoinVertical(lipgloss.Left, helpLines...)

		// Style the overlay with border
		overlayStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#ff6b6b")).
			Background(lipgloss.Color("#1a1a2e")).
			Padding(1, 2)

		renderedOverlay := overlayStyle.Render(content)

		// Position in bottom-right corner with some padding
		overlayWidth := lipgloss.Width(renderedOverlay)
		overlayHeight := lipgloss.Height(renderedOverlay)
		overlayX := m.Width - overlayWidth - 2
		overlayY := m.Height - overlayHeight - 3 // Above status bar

		whichKeyLayer := lipgloss.NewLayer(renderedOverlay).
			X(overlayX).
			Y(overlayY).
			Z(config.ZIndexWhichKey). // Above other overlays
			ID("whichkey")

		layers = append(layers, whichKeyLayer)
	}

	// Render notifications
	if len(m.Notifications) > 0 {
		// Clean up expired notifications
		m.CleanupNotifications()

		// Render active notifications
		notifY := 1       // Start position from top
		notifSpacing := 4 // Space between notifications
		for i, notif := range m.Notifications {
			if i >= 3 { // Max 3 notifications visible
				break
			}

			// Calculate opacity based on animation and lifetime
			opacity := 1.0
			if notif.Animation != nil {
				elapsed := time.Since(notif.Animation.StartTime)
				if elapsed < notif.Animation.Duration {
					opacity = float64(elapsed) / float64(notif.Animation.Duration)
				}
			}

			// Fade out in last 500ms
			timeLeft := notif.Duration - time.Since(notif.StartTime)
			if timeLeft < config.NotificationFadeOutDuration {
				opacity *= float64(timeLeft) / float64(config.NotificationFadeOutDuration)
			}

			// Skip if fully transparent
			if opacity <= 0 {
				continue
			}

			// Style based on type
			var bgColor, borderColor, fgColor, icon string
			switch notif.Type {
			case "error":
				bgColor = "#2a1515"
				borderColor = "#ff4444"
				fgColor = "#ff6666"
				icon = "✕"
			case "warning":
				bgColor = "#2a2515"
				borderColor = "#ffaa00"
				fgColor = "#ffcc00"
				icon = "⚠"
			case "success":
				bgColor = "#152a15"
				borderColor = "#44ff44"
				fgColor = "#66ff66"
				icon = "✓"
			default:
				bgColor = "#151a2a"
				borderColor = "#4488ff"
				fgColor = "#66aaff"
				icon = "ℹ"
			}

			// Calculate dynamic max width based on screen size (leave space for margins)
			maxNotifWidth := min(
				// Leave 8 chars margin (4 on each side)
				// Minimum width
				max(
					m.Width-8,
					20,
				),
				// Maximum width for readability
				60,
			)

			// Truncate message if it's too long (accounting for icon and padding)
			message := notif.Message
			maxMessageLen := maxNotifWidth - 8 // Account for icon, spaces, and padding
			if len(message) > maxMessageLen {
				message = message[:maxMessageLen-3] + "..."
			}

			// Build notification content with better spacing
			notifContent := fmt.Sprintf(" %s  %s ", icon, message)

			// Style the notification with border
			notifBox := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(borderColor)).
				Background(lipgloss.Color(bgColor)).
				Foreground(lipgloss.Color(fgColor)).
				Padding(0, 1).
				Bold(true).
				MaxWidth(maxNotifWidth).
				Render(notifContent)

			// Position in top-right corner with margin, ensure it doesn't go off-screen
			notifX := max(m.Width-lipgloss.Width(notifBox)-2, 0)
			currentY := notifY + (i * notifSpacing)

			// Create notification layer
			notifLayer := lipgloss.NewLayer(notifBox).
				X(notifX).Y(currentY).Z(config.ZIndexNotifications).
				ID(fmt.Sprintf("notif-%s", notif.ID))

			layers = append(layers, notifLayer)
		}
	}

	// Copy mode search input overlay
	focusedWindow := m.GetFocusedWindow()
	if focusedWindow != nil && focusedWindow.CopyMode != nil &&
		focusedWindow.CopyMode.Active &&
		focusedWindow.CopyMode.State == terminal.CopyModeSearch {

		searchQuery := focusedWindow.CopyMode.SearchQuery
		matchCount := len(focusedWindow.CopyMode.SearchMatches)
		currentMatch := focusedWindow.CopyMode.CurrentMatch

		// Build search bar text
		searchText := "/" + searchQuery + "█" // Block cursor
		if matchCount > 0 {
			searchText += fmt.Sprintf(" [%d/%d]", currentMatch+1, matchCount)
		} else if searchQuery != "" {
			searchText += " [0]"
		}

		// Style the search bar
		searchStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#000000")).
			Foreground(lipgloss.Color("#FFFF00")).
			Bold(true).
			Padding(0, 1)

		renderedSearch := searchStyle.Render(searchText)

		// Position at bottom of focused window
		searchX := focusedWindow.X + 2
		searchY := focusedWindow.Y + focusedWindow.Height - 2

		searchLayer := lipgloss.NewLayer(renderedSearch).
			X(searchX).
			Y(searchY).
			Z(config.ZIndexHelp + 1). // Above help
			ID("copy-mode-search")

		layers = append(layers, searchLayer)
	}

	// Copy mode help is now shown in the dock (bottom bar)

	return layers
}

func (m *OS) renderDock() *lipgloss.Layer {
	// System info styles
	sysInfoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808090")).
		MarginRight(2)

	modeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a0a0b0")).
		Bold(true).
		MarginRight(2)

	workspaceStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b0b0c0")).
		Bold(true).
		MarginRight(2)

	// Get mode text
	modeText := "[W]" // Window management mode
	focusedWindow := m.GetFocusedWindow()
	if m.Mode == TerminalMode {
		modeText = "[I]" // Insert/Terminal mode (like vim)
		// Add copy mode indicator
		if focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active {
			modeText = "[COPY]"
			// Show cursor position
			modeText += fmt.Sprintf(" %d:%d", focusedWindow.CopyMode.CursorY, focusedWindow.CopyMode.CursorX)
		}
	}
	// Add tiling indicator
	if m.AutoTiling && (focusedWindow == nil || focusedWindow.CopyMode == nil || !focusedWindow.CopyMode.Active) {
		modeText += " [TILE]" // Tiling mode icon (don't show in copy mode)
	}

	// Cache workspace styles for performance (optimization #8)
	if m.workspaceActiveStyle == nil {
		activeStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#4865f2")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true)
		m.workspaceActiveStyle = &activeStyle
	}
	if m.workspaceInactiveStyle == nil {
		inactiveStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808090"))
		m.workspaceInactiveStyle = &inactiveStyle
	}

	// Add workspace indicator with window counts
	workspaceText := ""
	for i := 1; i <= m.NumWorkspaces; i++ {
		count := m.GetWorkspaceWindowCount(i)
		if i == m.CurrentWorkspace {
			// Highlight current workspace
			if count > 0 {
				workspaceText += m.workspaceActiveStyle.Render(fmt.Sprintf(" %d:%d ", i, count))
			} else {
				workspaceText += m.workspaceActiveStyle.Render(fmt.Sprintf(" %d ", i))
			}
		} else if count > 0 {
			// Show workspaces with windows
			workspaceText += m.workspaceInactiveStyle.Render(fmt.Sprintf(" %d:%d ", i, count))
		}
	}

	// Count minimized AND minimizing windows in current workspace
	dockWindows := []int{}
	for i, window := range m.Windows {
		if window.Workspace == m.CurrentWorkspace && (window.Minimized || window.Minimizing) {
			dockWindows = append(dockWindows, i)
			if len(dockWindows) >= 9 {
				break // Only show up to 9 items
			}
		}
	}

	// Sort dock windows by minimize order (oldest first)
	sort.Slice(dockWindows, func(i, j int) bool {
		return m.Windows[dockWindows[i]].MinimizeOrder < m.Windows[dockWindows[j]].MinimizeOrder
	})

	// Build pill-style dock items
	var dockItemsStr string
	itemNumber := 1

	for _, windowIndex := range dockWindows {
		window := m.Windows[windowIndex]

		// Colors for active vs inactive
		bgColor := "#2a2a3e"
		fgColor := "#a0a0a8"

		// Check if window should be highlighted (newly minimized)
		isHighlighted := time.Now().Before(window.MinimizeHighlightUntil)

		// DEBUG: Log dock rendering
		if os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" && isHighlighted {
			if f, err := os.OpenFile("/tmp/tuios-minimize-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				fmt.Fprintf(f, "[RENDER] Dock item #%d, windowIndex=%d, ID=%s, CustomName=%s, isHighlighted=%v, HighlightUntil=%s\n",
					itemNumber, windowIndex, window.ID, window.CustomName, isHighlighted, window.MinimizeHighlightUntil.Format("15:04:05.000"))
				f.Close()
			}
		}

		if isHighlighted {
			// Bright highlight for newly minimized window
			bgColor = "#66ff66" // Bright green highlight
			fgColor = "#000000"
		} else if windowIndex == m.FocusedWindow && !window.Minimizing {
			bgColor = "#4865f2"
			fgColor = "#ffffff"
		}

		// Get window name (only custom names)
		windowName := window.CustomName

		// Format label based on whether we have a custom name
		var labelText string
		if windowName != "" {
			// Truncate if too long (max 12 chars for dock item)
			if len(windowName) > 12 {
				windowName = windowName[:9] + "..."
			}
			labelText = fmt.Sprintf(" %d:%s ", itemNumber, windowName)
		} else {
			// Just show the number if no custom name
			labelText = fmt.Sprintf(" %d ", itemNumber)
		}

		// Create pill-style item with circles and label
		leftCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(LeftHalfCircle)

		nameLabel := lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Bold(isHighlighted || windowIndex == m.FocusedWindow).
			Render(labelText)

		rightCircle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(bgColor)).
			Render(RightHalfCircle)

		// Add spacing between items
		if itemNumber > 1 {
			dockItemsStr += " "
		}
		dockItemsStr += leftCircle + nameLabel + rightCircle

		itemNumber++
	}

	// Build system info content
	// Left side: Mode, workspace, and window count
	leftInfo := lipgloss.JoinHorizontal(lipgloss.Top,
		modeStyle.Render(modeText),
		workspaceStyle.Render(workspaceText),
	)

	// Right side: CPU/RAM stats OR copy mode help
	var rightInfo string
	var rightWidth int

	// Check if in copy mode - show help instead of stats
	inCopyMode := focusedWindow != nil && focusedWindow.CopyMode != nil && focusedWindow.CopyMode.Active
	if inCopyMode {
		// Build help text based on copy mode state
		var helpText string
		switch focusedWindow.CopyMode.State {
		case terminal.CopyModeNormal:
			helpText = "hjkl:move w/b/e:word f/F/t/T:char /:search n/N:next/prev C-l:clear ;,:repeat v:visual y:yank i:term q:quit"
		case terminal.CopyModeSearch:
			helpText = "Type to search  n/N:next/prev  Enter:done  Esc:cancel"
		case terminal.CopyModeVisualChar:
			helpText = "hjkl:extend w/b/e:word f/F/t/T:char ;,:repeat {/}:para %:bracket y:yank Esc:cancel"
		case terminal.CopyModeVisualLine:
			helpText = "jk:extend  y:yank  Esc:cancel"
		}

		helpStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a0a0b0")).
			Background(lipgloss.Color("#1a1a2e")).
			Padding(0, 1)
		rightInfo = helpStyle.Render(helpText)
		rightWidth = lipgloss.Width(rightInfo) + 2
	} else {
		// Show CPU and RAM stats
		cpuGraph := m.GetCPUGraph()
		ramUsage := m.GetRAMUsage()
		rightInfo = sysInfoStyle.Render(cpuGraph + " " + ramUsage)
		rightWidth = 32 // CPU graph (~19 chars) + space + RAM (~11 chars) = ~31 chars
	}

	// Calculate actual widths to prevent overflow
	actualLeftWidth := lipgloss.Width(leftInfo)
	centerWidth := lipgloss.Width(dockItemsStr)

	// Ensure we have space for all components on one line
	minRequiredWidth := actualLeftWidth + centerWidth + rightWidth + 4 // +4 for minimal spacing

	// If content is too wide, truncate dock items to prevent wrapping
	if minRequiredWidth > m.Width {
		// Calculate how many dock items we can show
		maxDockWidth := m.Width - actualLeftWidth - rightWidth - 4
		if maxDockWidth < 0 {
			maxDockWidth = 0
		}

		// Truncate dockItemsStr if needed
		if lipgloss.Width(dockItemsStr) > maxDockWidth {
			// Show as many complete items as possible
			truncated := ""
			for i := range dockItemsStr {
				if lipgloss.Width(truncated+string(dockItemsStr[i])) > maxDockWidth-3 { // -3 for "..."
					truncated += "..."
					break
				}
				truncated += string(dockItemsStr[i])
			}
			dockItemsStr = truncated
			centerWidth = lipgloss.Width(dockItemsStr)
		}
	}

	// Use actual left width (no padding to fixed width)
	leftWidth := actualLeftWidth

	// Calculate spacing to center dock items
	availableSpace := m.Width - leftWidth - rightWidth - centerWidth
	leftSpacer := availableSpace / 2
	rightSpacer := availableSpace - leftSpacer

	// Ensure non-negative spacers
	if leftSpacer < 0 {
		leftSpacer = 0
		rightSpacer = 0
	}
	if rightSpacer < 0 {
		rightSpacer = 0
	}

	// Build the complete dock bar on a single line
	paddedRightInfo := lipgloss.NewStyle().Width(rightWidth).Align(lipgloss.Right).Render(rightInfo)

	dockBar := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftInfo,
		lipgloss.NewStyle().Width(leftSpacer).Render(""),
		lipgloss.NewStyle().Render(dockItemsStr),
		lipgloss.NewStyle().Width(rightSpacer).Render(""),
		paddedRightInfo,
	)

	// Cache separator string for performance (optimization #3)
	if m.cachedSeparatorWidth != m.Width {
		m.cachedSeparator = strings.Repeat("─", m.Width)
		m.cachedSeparatorWidth = m.Width
	}

	// Add separator line above
	separator := lipgloss.NewStyle().
		Width(m.Width).
		Foreground(lipgloss.Color("#303040")).
		Render(m.cachedSeparator)

	// Combine separator and dock bar
	fullDock := lipgloss.JoinVertical(lipgloss.Left,
		separator,
		dockBar,
	)

	// Return the dock layer positioned to show everything
	return lipgloss.NewLayer(fullDock).X(0).Y(m.Height - config.DockHeight).Z(config.ZIndexDock).ID("dock")
}

// isPositionInSelection checks if the given position is within the current text selection.
func (m *OS) isPositionInSelection(window *terminal.Window, x, y int) bool {
	// Return false if there's no selection (either actively selecting or completed selection)
	if !window.IsSelecting && window.SelectedText == "" {
		return false
	}

	// Normalize selection coordinates (ensure start <= end)
	startX, startY := window.SelectionStart.X, window.SelectionStart.Y
	endX, endY := window.SelectionEnd.X, window.SelectionEnd.Y

	// Swap if selection was made backwards
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	// Check if position is within selection bounds
	if y < startY || y > endY {
		return false
	}
	if y == startY && y == endY {
		// Single line selection
		return x >= startX && x <= endX
	} else if y == startY {
		// First line of multi-line selection
		return x >= startX
	} else if y == endY {
		// Last line of multi-line selection
		return x <= endX
	} else {
		// Middle lines of multi-line selection
		return true
	}
}

// View returns the rendered view as a string.
func (m *OS) View() tea.View {
	var view tea.View

	// Set content (use Sprint instead of Sprintln to avoid extra newline)
	view.SetContent(lipgloss.Sprint(m.GetCanvas(true).Render()))

	// Configure view properties (moved from startup options and Init commands)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	view.ReportFocus = true
	view.DisableBracketedPasteMode = false

	return view
}
