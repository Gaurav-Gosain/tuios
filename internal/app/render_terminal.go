package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
)

func (m *OS) renderTerminal(window *terminal.Window, isFocused bool, inTerminalMode bool) string {
	if window.IsBeingManipulated && m.Resizing {
		return m.renderResizeIndicator(window)
	}

	if (window.IsBeingManipulated || !window.ContentDirty) && window.CachedContent != "" {
		return window.CachedContent
	}

	if !isFocused && window.CachedContent != "" && len(window.CachedContent) > 0 {
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

	cursor := screen.CursorPosition()
	cursorX := cursor.X
	cursorY := cursor.Y

	builder := pool.GetStringBuilder()
	defer pool.PutStringBuilder(builder)

	estimatedSize := (window.Width - 2) * (window.Height - 2)
	builder.Grow(estimatedSize)

	maxY := min(window.Height-2, screen.Height())
	maxX := min(window.Width-2, screen.Width())

	useOptimizedRendering := !isFocused && !inTerminalMode

	scrollbackLen := window.ScrollbackLen()
	inScrollbackMode := window.ScrollbackOffset > 0

	inCopyMode := window.CopyMode != nil && window.CopyMode.Active
	var copyModeCursorX, copyModeCursorY int
	if inCopyMode {
		copyModeCursorX = window.CopyMode.CursorX
		copyModeCursorY = window.CopyMode.CursorY
	}

	// Skip fake cursor rendering when real terminal cursor is active
	useRealCursor := m.getRealCursor() != nil

	// Use pooled highlight grids to reduce allocations
	var searchHighlights, currentMatchHighlight, visualSelection *pool.HighlightGrid

	if inCopyMode && len(window.CopyMode.SearchMatches) > 0 {
		searchHighlights = pool.GetHighlightGrid()
		currentMatchHighlight = pool.GetHighlightGrid()
		searchHighlights.Init(maxY, maxX)
		currentMatchHighlight.Init(maxY, maxX)
		defer pool.PutHighlightGrid(searchHighlights)
		defer pool.PutHighlightGrid(currentMatchHighlight)

		for i, match := range window.CopyMode.SearchMatches {
			var viewportY int
			if match.Line < scrollbackLen {
				if window.ScrollbackOffset > 0 {
					if match.Line >= scrollbackLen-window.ScrollbackOffset {
						viewportY = match.Line - (scrollbackLen - window.ScrollbackOffset)
					} else {
						continue
					}
				} else {
					continue
				}
			} else {
				screenLine := match.Line - scrollbackLen
				if window.ScrollbackOffset > 0 {
					viewportY = window.ScrollbackOffset + screenLine
				} else {
					viewportY = screenLine
				}
			}

			if viewportY >= 0 && viewportY < maxY {
				isCurrentMatch := (i == window.CopyMode.CurrentMatch)

				for x := match.StartX; x < match.EndX && x < maxX; x++ {
					if isCurrentMatch {
						currentMatchHighlight.Set(viewportY, x)
					} else {
						searchHighlights.Set(viewportY, x)
					}
				}
			}
		}
	}

	inVisualMode := inCopyMode &&
		(window.CopyMode.State == terminal.CopyModeVisualChar ||
			window.CopyMode.State == terminal.CopyModeVisualLine)

	if inVisualMode {
		visualSelection = pool.GetHighlightGrid()
		visualSelection.Init(maxY, maxX)
		defer pool.PutHighlightGrid(visualSelection)

		start := window.CopyMode.VisualStart
		end := window.CopyMode.VisualEnd

		if start.Y > end.Y || (start.Y == end.Y && start.X > end.X) {
			start, end = end, start
		}

		for absY := start.Y; absY <= end.Y; absY++ {
			var viewportY int
			if absY < scrollbackLen {
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
				screenY := absY - scrollbackLen
				if window.ScrollbackOffset > 0 {
					viewportY = window.ScrollbackOffset + screenY
				} else {
					viewportY = screenY
				}
			}

			if viewportY >= 0 && viewportY < maxY {
				startX, endX := 0, maxX-1
				if absY == start.Y {
					startX = start.X
				}
				if absY == end.Y {
					endX = end.X
				}

				for x := startX; x <= endX && x < maxX; x++ {
					visualSelection.Set(viewportY, x)
				}
			}
		}
	}

	var batchBuilder strings.Builder
	var currentStyle lipgloss.Style
	var batchHasStyle bool
	var prevCell *uv.Cell
	var prevIsCursor, prevIsSelected, prevIsSelectionCursor bool

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

	safeColorEquals := func(a, b color.Color) (result bool) {
		defer func() {
			if recover() != nil {
				result = false
			}
		}()
		if a == nil && b == nil {
			return true
		}
		if a == nil || b == nil {
			return false
		}
		return a == b
	}

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
			safeColorEquals(prevCell.Style.Fg, cell.Style.Fg) &&
			safeColorEquals(prevCell.Style.Bg, cell.Style.Bg) &&
			prevCell.Style.Attrs == cell.Style.Attrs
	}

	for y := range maxY {
		if y > 0 {
			builder.WriteRune('\n')
		}

		lineBuilder := pool.GetStringBuilder()
		defer pool.PutStringBuilder(lineBuilder)

		batchBuilder.Reset()
		batchHasStyle = false
		prevCell = nil

		lineEndX := maxX - 1
		if inVisualMode && visualSelection != nil && visualSelection.HasRow(y) {
			if inScrollbackMode {
				if y < window.ScrollbackOffset {
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

			if inCopyMode && x == copyModeCursorX && y == copyModeCursorY {
				char := " "
				var cursorCell *uv.Cell
				charWidth := 1

				if inScrollbackMode {
					if y < window.ScrollbackOffset {
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
					cursorCell = screen.CellAt(x, y)
					if cursorCell != nil && cursorCell.Content != "" {
						char = cursorCell.Content
					}
					if cursorCell != nil && cursorCell.Width > 0 {
						charWidth = cursorCell.Width
					}
				}

				cursorStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#00D7FF")).
					Foreground(lipgloss.Color("#000000")).
					Bold(true)

				if batchBuilder.Len() > 0 {
					if batchHasStyle {
						lineBuilder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
					} else {
						lineBuilder.WriteString(batchBuilder.String())
					}
					batchBuilder.Reset()
					batchHasStyle = false
				}

				lineBuilder.WriteString(renderStyledText(cursorStyle, char))

				prevCell = nil
				prevIsCursor = false
				prevIsSelected = false
				prevIsSelectionCursor = false

				x += charWidth
				continue
			}

			if inScrollbackMode {
				if y < window.ScrollbackOffset {
					scrollbackIndex := scrollbackLen - window.ScrollbackOffset + y
					if scrollbackIndex >= 0 && scrollbackIndex < scrollbackLen {
						scrollbackLine := window.ScrollbackLine(scrollbackIndex)
						if scrollbackLine != nil && x < len(scrollbackLine) {
							cell = &scrollbackLine[x]
						}
					}
				} else {
					screenY := y - window.ScrollbackOffset
					if screenY >= 0 && screenY < screen.Height() {
						cell = screen.CellAt(x, screenY)
					}
				}
			} else {
				cell = screen.CellAt(x, y)
			}

			char := " "
			if cell != nil && cell.Content != "" {
				char = string(cell.Content)
			}

			if inVisualMode && visualSelection != nil && visualSelection.Get(y, x) && x <= lineEndX {
				selStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#5F5FAF")).
					Foreground(lipgloss.Color("#FFFFFF")).
					Bold(true)

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
				cellWidth := 1
				if cell != nil && cell.Width > 1 {
					cellWidth = cell.Width
				}
				x += cellWidth
				continue
			}

			if inCopyMode && !inVisualMode {
				if currentMatchHighlight != nil && currentMatchHighlight.Get(y, x) {
					matchStyle := lipgloss.NewStyle().
						Background(lipgloss.Color("#FF00FF")).
						Foreground(lipgloss.Color("#000000")).
						Bold(true)

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
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}

				if searchHighlights != nil && searchHighlights.Get(y, x) {
					matchStyle := lipgloss.NewStyle().
						Background(lipgloss.Color("#FF8700")).
						Foreground(lipgloss.Color("#000000"))

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
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}
			}

			isSelected := (window.IsSelecting || window.SelectedText != "") && m.isPositionInSelection(window, x, y)
			// Only render fake cursor when real terminal cursor is not being used
			isCursorPos := !useRealCursor && isFocused && inTerminalMode && !inCopyMode && !screen.IsCursorHidden() && x == cursorX && y == cursorY

			isSelectionCursor := m.SelectionMode && !inTerminalMode && isFocused &&
				x == window.SelectionCursor.X && y == window.SelectionCursor.Y

			needsStyling := shouldApplyStyle(cell) || isCursorPos || isSelected || isSelectionCursor

			if x > 0 && !styleMatches(cell, isCursorPos, isSelected, isSelectionCursor) {
				flushBatch(lineBuilder)
			}

			if needsStyling {
				if batchBuilder.Len() == 0 {
					if isSelected || isSelectionCursor {
						if useOptimizedRendering {
							currentStyle = buildOptimizedCellStyleCached(cell)
						} else {
							currentStyle = buildCellStyleCached(cell, isCursorPos)
						}

						if isSelected {
							currentStyle = currentStyle.Background(lipgloss.Color("62")).Foreground(lipgloss.Color("15"))
						}

						if isSelectionCursor {
							currentStyle = currentStyle.Background(lipgloss.Color("208")).Foreground(lipgloss.Color("0"))
						}
					} else {
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
				batchBuilder.WriteString(char)
			}

			prevCell = cell
			prevIsCursor = isCursorPos
			prevIsSelected = isSelected
			prevIsSelectionCursor = isSelectionCursor

			cellWidth := 1
			if cell != nil && cell.Width > 1 {
				cellWidth = cell.Width
			}
			x += cellWidth
		}

		flushBatch(lineBuilder)
		builder.WriteString(lineBuilder.String())
	}

	content := builder.String()
	window.CachedContent = content
	window.ContentDirty = false
	return content
}

func (m *OS) renderResizeIndicator(window *terminal.Window) string {
	termWidth := max(window.Width-2, 1)
	termHeight := max(window.Height-2, 1)

	resizeMsg := fmt.Sprintf("Resizing... %dx%d", termWidth, termHeight)

	var builder strings.Builder

	centerY := termHeight / 2
	centerX := max((termWidth-len(resizeMsg))/2, 0)

	for y := range termHeight {
		for x := range termWidth {
			if y == centerY && x >= centerX && x < centerX+len(resizeMsg) {
				msgIdx := x - centerX
				if msgIdx < len(resizeMsg) {
					builder.WriteRune(rune(resizeMsg[msgIdx]))
				} else {
					builder.WriteRune(' ')
				}
			} else {
				builder.WriteRune(' ')
			}
		}

		if y < termHeight-1 {
			builder.WriteRune('\n')
		}
	}

	return builder.String()
}
