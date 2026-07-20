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

// Highlight styles used by the terminal render loop are invariant, so they are
// built once instead of per matching cell per frame.
var (
	copyModeCursorStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#00D7FF")).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)

	visualSelectionStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#5F5FAF")).
				Foreground(lipgloss.Color("#FFFFFF")).
				Bold(true)

	currentMatchStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#FF00FF")).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)

	searchMatchStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#FF8700")).
				Foreground(lipgloss.Color("#000000"))
)

// isBlankRender reports whether a rendered frame carries no visible text, so
// styling and cursor positioning alone do not count as content. It walks bytes
// and returns on the first visible one, so the ordinary non-blank frame costs a
// few comparisons and no allocation.
func isBlankRender(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b:
			// Skip an escape sequence up to its final byte.
			i++
			for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
				i++
			}
		case c == ' ', c == '\n', c == '\r', c == '\t':
			// Whitespace is not content.
		default:
			return false
		}
	}
	return true
}

// cacheRender stores a freshly rendered frame as the window's cached content and
// clears the repaint request, but refuses to do either for a frame with no
// visible text.
//
// A full-screen application clears the alternate screen when it enters it and
// paints a moment later. A render landing in that gap produces a genuinely
// blank frame, which is correct to display right then but must not become the
// window's cached truth: caching it also clears ContentDirty, and if focus
// moves away before the application paints, nothing re-reads the emulator. The
// pane then serves the blank cache from the branch above for as long as the
// application stays idle, which is exactly what a full-screen editor does once
// it has drawn. Leaving the frame uncached and the window dirty costs one cheap
// re-render per frame while a pane is genuinely blank, and guarantees the next
// frame reads the emulator again rather than freezing the gap.
func cacheRender(window *terminal.Window, content string) {
	if isBlankRender(content) {
		return
	}
	window.CachedContent = content
	window.ContentDirty = false
}

func (m *OS) renderTerminal(window *terminal.Window, isFocused bool, inTerminalMode bool) string {
	entryDirty := window.ContentDirty

	if window.IsBeingManipulated && m.Resizing {
		out := m.renderResizeIndicator(window)
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "resize-indicator", out)
		}
		return out
	}

	if (window.IsBeingManipulated || !window.ContentDirty) && window.CachedContent != "" {
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "cache-clean", window.CachedContent)
		}
		return window.CachedContent
	}

	// An unfocused window used to return its cache here unconditionally, even
	// with ContentDirty set. That silently discarded a repaint request: the
	// paths that mark content dirty without dropping the cache (WriteToPTY and
	// the drag and resize release handler) left the window able to serve stale
	// bytes indefinitely, because nothing else re-reads the emulator while a
	// window is unfocused. Once the flag is honoured the branch is subsumed by
	// the one above, which already serves the cache whenever the content is
	// clean, focused or not, so there is nothing left for it to do.

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	if window.Terminal == nil {
		window.CachedContent = "Terminal not initialized"
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "no-terminal", window.CachedContent)
		}
		return window.CachedContent
	}

	screen := window.Terminal
	if screen == nil {
		window.CachedContent = "No screen"
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "no-screen", window.CachedContent)
		}
		return window.CachedContent
	}

	// Whether the host terminal is drawing a real cursor decides only whether
	// the cell loop paints a fake one. getRealCursor takes the focused window's
	// read side of ioMu itself, and when this window is the focused one that is
	// the very same lock acquired just below. w.ioMu is a sync.RWMutex, which is
	// not reentrant for readers: once the PTY writer queues a Lock, every later
	// RLock parks behind it, so a second RLock taken while the first is still
	// held deadlocks against a writer that is waiting on that first one. Query
	// it here, before the lock is taken, so the two acquisitions never nest.
	useRealCursor := m.getRealCursor() != nil

	// The emulator cell buffer is written by the PTY reader and daemon paths
	// under w.ioMu and reallocated by Resize under the same lock, so every VT
	// read below (Render, CursorPosition, CellAt, scrollback) must hold the
	// read side. terminalMu still guards the m.Windows slice and dirty flags.
	//
	// Try rather than wait. A pane emitting thousands of lines a second holds
	// the exclusive side almost continuously, and blocking here stalls the
	// whole composited frame on that one pane, so the user's keystroke echo in
	// a completely different pane waits behind output nobody can read. Serving
	// the previous frame for the busy pane and leaving it dirty sheds that
	// intermediate frame instead: the pane repaints on the next frame that
	// acquires, so it still converges on its true final state once the burst
	// ends, and no input is affected.
	if !window.TryRLockIO() {
		if window.CachedContent != "" {
			if renderTraceEnabled {
				traceRender(window, isFocused, inTerminalMode, entryDirty, "shed-locked", window.CachedContent)
			}
			return window.CachedContent
		}
		// No cache to fall back on yet, so this pane has never rendered. Wait,
		// because showing nothing at all is worse than one blocked frame, and
		// it can only happen in the first frames of a pane's life.
		window.RLockIO()
	}
	defer window.RUnlockIO()

	// Fast path for unfocused windows: use the emulator's built-in Render()
	// which is faster than cell-by-cell iteration. The focused window uses
	// the slow path for cursor overlay and selection highlighting.
	if !isFocused && window.CopyMode == nil && !window.IsSelecting && window.SelectedText == "" && window.ScrollbackOffset == 0 {
		rendered := screen.Render()
		cacheRender(window, rendered)
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "fast-unfocused", rendered)
		}
		return rendered
	}

	// Fast path for scrollback mode: content is static at a given scroll
	// position, so reuse the cache if the offset hasn't changed.
	if window.ScrollbackOffset > 0 && window.CachedContent != "" && !window.ContentDirty {
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "cache-scrollback", window.CachedContent)
		}
		return window.CachedContent
	}

	cursor := screen.CursorPosition()
	cursorX := cursor.X
	cursorY := cursor.Y

	builder := pool.GetStringBuilder()
	defer pool.PutStringBuilder(builder)

	contentW := window.ContentWidth()
	contentH := window.ContentHeight()

	estimatedSize := contentW * contentH
	builder.Grow(estimatedSize)

	maxY := min(contentH, screen.Height())
	maxX := min(contentW, screen.Width())

	useOptimizedRendering := !isFocused && !inTerminalMode

	scrollbackLen := window.ScrollbackLen()
	inScrollbackMode := window.ScrollbackOffset > 0

	inCopyMode := window.CopyMode != nil && window.CopyMode.Active
	var copyModeCursorX, copyModeCursorY int
	if inCopyMode {
		copyModeCursorX = window.CopyMode.CursorX
		copyModeCursorY = window.CopyMode.CursorY
	}

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
	// When the batch style came straight from the style cache (not a
	// selection-modified or highlight style), the derived ANSI escape is cached
	// alongside it, so flushBatch can emit the cached prefix/suffix directly
	// instead of rebuilding them via styleToANSI. currentStyleCached gates that.
	var currentStyleCached bool
	var currentPrefix, currentSuffix string
	var prevCell *uv.Cell
	var prevIsCursor, prevIsSelected, prevIsSelectionCursor bool

	flushBatch := func() {
		if batchBuilder.Len() > 0 {
			if batchHasStyle {
				if currentStyleCached {
					if currentPrefix == "" {
						builder.WriteString(batchBuilder.String())
					} else {
						builder.WriteString(currentPrefix)
						builder.WriteString(batchBuilder.String())
						builder.WriteString(currentSuffix)
					}
				} else {
					builder.WriteString(renderStyledText(currentStyle, batchBuilder.String()))
				}
			} else {
				builder.WriteString(batchBuilder.String())
			}
			batchBuilder.Reset()
			batchHasStyle = false
			currentStyleCached = false
		}
	}

	safeColorEquals := func(a, b color.Color) bool {
		// Adjacent cells almost always share the same color interface value, so
		// compare identity before falling back to the four RGBA computations.
		if a == b {
			return true
		}
		if a == nil || b == nil {
			return false
		}
		ar, ag, ab, aa := a.RGBA()
		br, bg, bb, ba := b.RGBA()
		return ar == br && ag == bg && ab == bb && aa == ba
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

				flushBatch()

				builder.WriteString(renderStyledText(copyModeCursorStyle, char))

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
				flushBatch()

				builder.WriteString(renderStyledText(visualSelectionStyle, char))
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
					flushBatch()

					builder.WriteString(renderStyledText(currentMatchStyle, char))
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
					flushBatch()

					builder.WriteString(renderStyledText(searchMatchStyle, char))
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
				flushBatch()
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
						// The style was modified after the cache lookup, so the
						// cached escape no longer matches it; flush via styleToANSI.
						currentStyleCached = false
					} else {
						// Pure cached style: reuse the cached ANSI escape so
						// flushBatch skips styleToANSI.
						if useOptimizedRendering {
							currentStyle, currentPrefix, currentSuffix = buildOptimizedCellStyleCachedANSI(cell)
						} else {
							currentStyle, currentPrefix, currentSuffix = buildCellStyleCachedANSI(cell, isCursorPos)
						}
						currentStyleCached = true
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

		flushBatch()
	}

	content := builder.String()

	cacheRender(window, content)
	if renderTraceEnabled {
		traceRender(window, isFocused, inTerminalMode, entryDirty, "slow", content)
	}
	return content
}

func (m *OS) renderResizeIndicator(window *terminal.Window) string {
	termWidth := window.ContentWidth()
	termHeight := window.ContentHeight()

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
