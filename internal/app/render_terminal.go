package app

import (
	"fmt"
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
func cacheRender(window *terminal.Window, content string, cols, rows int) {
	if isBlankRender(content) {
		return
	}
	window.CachedContent = content
	window.CachedContentCols, window.CachedContentRows = cols, rows
	// Only a frame read outside a synchronized update is a complete one, so
	// only that may become what the hold falls back on. Caching a frame taken
	// mid-update would make the hold present the very thing it exists to hide.
	if window.Terminal == nil || !window.Terminal.IsSyncActive() {
		window.SyncHoldContent = content
	}
	window.ContentDirty = false
}

// cellGrid is the part of the emulator gridFillsEveryRow reads. Taking an
// interface keeps the emulator package out of this file's imports.
type cellGrid interface {
	CellAt(x, y int) *uv.Cell
}

// gridFillsEveryRow reports whether every row of the grid accounts for exactly
// w columns, so a buffer render of it carries w columns on every one of its h
// lines and the border box may take it as already shaped.
//
// It exists because that is a property of somebody else's renderer. A buffer
// render walks the grid and emits each cell's glyph, so it is w columns wide
// only while the grid's own widths add up to w, and an earlier version of that
// renderer right-trimmed every line instead. Reading the widths costs a pass
// over the cells with no allocation and no text handling at all, and it is the
// same bookkeeping the cell loop below steps by, so the two paths agree on
// what they are claiming.
//
// Nothing here looks at a cell's text. A wide rune, a combining mark and a
// joined emoji are each just a width to this, which is why none of them can
// fool it: the cell grid is where their column counts were decided.
func gridFillsEveryRow(grid cellGrid, w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	for y := range h {
		total := 0
		for x := 0; x < w && total <= w; x++ {
			cell := grid.CellAt(x, y)
			if cell == nil {
				return false
			}
			total += cell.Width
		}
		if total != w {
			return false
		}
	}
	return true
}

func (m *OS) renderTerminal(window *terminal.Window, isFocused bool, inTerminalMode bool) string {
	entryDirty := window.ContentDirty
	// Every branch below that does not lay the whole grid out leaves this
	// zeroed, so the border box re-flows exactly as it always did.
	window.RenderedCols, window.RenderedRows = 0, 0

	if window.IsBeingManipulated && m.Resizing {
		out := m.renderResizeIndicator(window)
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "resize-indicator", out)
		}
		return out
	}

	if (window.IsBeingManipulated || !window.ContentDirty) && window.CachedContent != "" {
		window.RenderedCols, window.RenderedRows = window.CachedContentCols, window.CachedContentRows
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "cache-clean", window.CachedContent)
		}
		return window.CachedContent
	}

	// The guest has an open synchronized update (DEC 2026): it has begun a frame
	// and does not want it seen half-drawn. The compositor holds the window's
	// cached layer for this, but a retile, scroll, rename or palette change
	// drops that layer and the content behind it, and the frame composed next
	// read the emulator mid-update and presented half of it. Holding here
	// instead of at the layer covers every path that composes a window, and
	// costs nothing when there is a layer to hold. ContentDirty is left set, so
	// the frame that arrives when the guest closes the update re-reads.
	if window.SyncHoldContent != "" && window.Terminal != nil && window.Terminal.IsSyncActive() {
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "sync-hold", window.SyncHoldContent)
		}
		return window.SyncHoldContent
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
		window.CachedContentCols, window.CachedContentRows = 0, 0
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "no-terminal", window.CachedContent)
		}
		return window.CachedContent
	}

	screen := window.Terminal
	if screen == nil {
		window.CachedContent = "No screen"
		window.CachedContentCols, window.CachedContentRows = 0, 0
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
			window.RenderedCols, window.RenderedRows = window.CachedContentCols, window.CachedContentRows
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
	if !isFocused && window.CopyMode == nil && window.ScrollbackOffset == 0 {
		rendered := screen.Render()
		cols, rows := 0, 0
		if gridFillsEveryRow(screen, screen.Width(), screen.Height()) {
			cols, rows = screen.Width(), screen.Height()
		}
		cacheRender(window, rendered, cols, rows)
		window.RenderedCols, window.RenderedRows = cols, rows
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "fast-unfocused", rendered)
		}
		return rendered
	}

	// Fast path for scrollback mode: content is static at a given scroll
	// position, so reuse the cache if the offset hasn't changed.
	if window.ScrollbackOffset > 0 && window.CachedContent != "" && !window.ContentDirty {
		window.RenderedCols, window.RenderedRows = window.CachedContentCols, window.CachedContentRows
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

	inCopyMode := window.InCopyMode()
	// The block cursor is copy mode showing itself. A pane that is merely
	// scrolled back under the wheel draws none: a cursor parked mid-pane over
	// output the user is only reading is the clearest tell that they have been
	// put in a mode. Selection highlights and search matches still render,
	// because a drag-selection runs in an implicit session.
	showCopyCursor := window.CopyModeVisible()
	copyModeCursorX, copyModeCursorY := -1, -1
	if showCopyCursor {
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

	// Set false by any row the cell loop could not fill to exactly maxX columns.
	gridExact := true

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
	var prevIsCursor bool

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

	// safeColorEquals is defined at package scope (color_nil.go) so it can guard
	// against wrapped-nil colors and be exercised directly by tests.
	styleMatches := func(cell *uv.Cell, isCursorPos bool) bool {
		if prevCell == nil && cell == nil {
			return prevIsCursor == isCursorPos
		}
		if prevCell == nil || cell == nil {
			return false
		}
		return prevIsCursor == isCursorPos &&
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

		x := 0
		for x < maxX {
			var cell *uv.Cell

			if showCopyCursor && x == copyModeCursorX && y == copyModeCursorY {
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
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}
			}

			// Only render fake cursor when real terminal cursor is not being
			// used. Suppressing the real one during a resize must not hand the
			// job to this path instead: the gesture draws no cursor either way.
			isCursorPos := !useRealCursor && !m.Resizing && isFocused && inTerminalMode && !inCopyMode && !screen.IsCursorHidden() && x == cursorX && y == cursorY

			needsStyling := shouldApplyStyle(cell) || isCursorPos

			if x > 0 && !styleMatches(cell, isCursorPos) {
				flushBatch()
			}

			if needsStyling && batchBuilder.Len() == 0 {
				// Pure cached style: reuse the cached ANSI escape so flushBatch
				// skips styleToANSI.
				if useOptimizedRendering {
					currentStyle, currentPrefix, currentSuffix = buildOptimizedCellStyleCachedANSI(cell)
				} else {
					currentStyle, currentPrefix, currentSuffix = buildCellStyleCachedANSI(cell, isCursorPos)
				}
				currentStyleCached = true
				batchHasStyle = true
			}
			batchBuilder.WriteString(char)

			prevCell = cell
			prevIsCursor = isCursorPos

			cellWidth := 1
			if cell != nil && cell.Width > 1 {
				cellWidth = cell.Width
			}
			x += cellWidth
		}

		// The loop writes one glyph per cell and steps by the cell's own width,
		// so the row it just emitted is maxX columns wide unless a wide cell
		// straddled the last column and pushed it one further. Checking the
		// counter the loop already keeps costs one comparison per row and makes
		// the claim below something this function has measured rather than
		// something it assumes about the grid.
		if x != maxX {
			gridExact = false
		}

		flushBatch()
	}

	content := builder.String()

	cols, rows := 0, 0
	if gridExact && maxX == contentW && maxY == contentH {
		cols, rows = maxX, maxY
	}
	window.RenderedCols, window.RenderedRows = cols, rows
	cacheRender(window, content, cols, rows)
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
