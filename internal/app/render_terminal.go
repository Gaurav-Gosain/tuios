package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

type markStyles struct {
	markInputs
	selection lipgloss.Style
	search    lipgloss.Style
	match     lipgloss.Style
	cursor    lipgloss.Style
}

type markInputs struct {
	gen, hostGen                uint64
	depth                       overlay.Depth
	selBg, selFg                string
	selBold                     bool
	searchBg, searchFg, matchBg string
	matchFg, cursorBg, cursorFg string
}

func markStyle(bg, fg string, bold bool) lipgloss.Style {
	st := lipgloss.NewStyle()
	if bg != "" {
		st = st.Background(configInk(bg))
	}
	if fg != "" {
		st = st.Foreground(configInk(fg))
	}
	return st.Bold(bold)
}

func (m *OS) marks() *markStyles {
	s := &m.Settings
	in := markInputs{
		gen: theme.Generation(), hostGen: m.host.gen, depth: theme.Depth(),
		selBg: s.SelectionBg, selFg: s.SelectionFg, selBold: s.SelectionBold,
		searchBg: s.SearchBg, searchFg: s.SearchFg, matchBg: s.MatchBg, matchFg: s.MatchFg,
		cursorBg: s.CopyCursorBg, cursorFg: s.CopyCursorFg,
	}
	if c := m.markStyleCache; c != nil && c.markInputs == in {
		return c
	}
	m.markStyleCache = &markStyles{
		markInputs: in,
		selection:  m.selectionStyle(s),
		search:     markStyle(s.SearchBg, s.SearchFg, false),
		match:      markStyle(s.MatchBg, s.MatchFg, true),
		cursor:     markStyle(s.CopyCursorBg, s.CopyCursorFg, true),
	}
	return m.markStyleCache
}

func (m *OS) selectionStyle(s *config.Settings) lipgloss.Style {
	st := markStyle(s.SelectionBg, s.SelectionFg, s.SelectionBold)
	if s.SelectionFg != "" {
		return st
	}
	bg := theme.ConfigColor(s.SelectionBg)
	if bg == nil {
		return st
	}
	ink := theme.TerminalFg()
	if theme.Current() == nil && m.host.fg != nil {
		ink = m.host.fg
	}
	if overlay.ContrastRatio(overlay.Shown(ink), overlay.Shown(bg)) >= overlay.ContrastFloor {
		return st
	}
	return st.Foreground(overlay.Readable(ink, bg))
}

func linkHoverStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(theme.LinkHover()).Underline(true)
}

func isBlankRender(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x1b:
			i++
			for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
				i++
			}
		case c == ' ', c == '\n', c == '\r', c == '\t':
		default:
			return false
		}
	}
	return true
}

func cacheRender(window *terminal.Window, content string, cols, rows, dim int) {
	if isBlankRender(content) {
		return
	}
	window.CachedContent = content
	window.CachedContentCols, window.CachedContentRows = cols, rows
	window.SetCachedContentDim(dim)
	if window.Terminal == nil || !window.Terminal.IsSyncActive() {
		window.SyncHoldContent = content
	}
	window.ContentDirty = false
}

type cellGrid interface {
	CellAt(x, y int) *uv.Cell
}

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
	window.RenderedCols, window.RenderedRows = 0, 0

	if window.IsBeingManipulated && m.Resizing {
		out := m.renderResizeIndicator(window)
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "resize-indicator", out)
		}
		return out
	}

	dim := paneDim(isFocused || m.MultifocusUndimmed(window.ID), &m.Settings)
	if m.MultiCopyParked(window.ID) {
		dim = max(dim, m.Settings.MultiCopyDim)
	}

	cacheUsable := window.CachedContent != "" && window.CachedContentDim() == dim

	flashing := m.copyFlashFor(window.ID) != nil && m.CopyFlashActive()

	if !flashing && (window.IsBeingManipulated || !window.ContentDirty) && cacheUsable {
		window.RenderedCols, window.RenderedRows = window.CachedContentCols, window.CachedContentRows
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "cache-clean", window.CachedContent)
		}
		return window.CachedContent
	}

	if m.DrawsTML(window) {
		return m.renderTML(window, dim)
	}
	if m.tmlHostTooOld(window) {
		return m.renderTMLHostTooOld(window, dim)
	}

	if window.SyncHoldContent != "" && window.Terminal != nil && window.Terminal.IsSyncActive() {
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "sync-hold", window.SyncHoldContent)
		}
		return window.SyncHoldContent
	}

	m.terminalMu.Lock()
	defer m.terminalMu.Unlock()

	if window.Terminal == nil {
		window.CachedContent = "Terminal not initialized"
		window.CachedContentCols, window.CachedContentRows = 0, 0
		window.SetCachedContentDim(dim)
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "no-terminal", window.CachedContent)
		}
		return window.CachedContent
	}

	screen := window.Terminal
	if screen == nil {
		window.CachedContent = "No screen"
		window.CachedContentCols, window.CachedContentRows = 0, 0
		window.SetCachedContentDim(dim)
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "no-screen", window.CachedContent)
		}
		return window.CachedContent
	}

	nativeCursor, _ := m.paneCursor()
	useRealCursor := nativeCursor != nil

	overlayOpen := m.AnyOverlayOpen()

	if !window.TryRLockIO() {
		if window.CachedContent != "" {
			window.RenderedCols, window.RenderedRows = window.CachedContentCols, window.CachedContentRows
			if renderTraceEnabled {
				traceRender(window, isFocused, inTerminalMode, entryDirty, "shed-locked", window.CachedContent)
			}
			return window.CachedContent
		}
		window.RLockIO()
	}
	defer window.RUnlockIO()
	if window.SyncHoldContent != "" && screen.IsSyncActive() {
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "sync-hold-locked", window.SyncHoldContent)
		}
		return window.SyncHoldContent
	}

	linkRun, hasLinkRun := m.linkHoverFor(window)

	if !isFocused && !flashing && dim == 0 && !hasLinkRun && window.CopyMode == nil && window.ScrollbackOffset == 0 {
		rendered := screen.Render()
		cols, rows := 0, 0
		if gridFillsEveryRow(screen, screen.Width(), screen.Height()) {
			cols, rows = screen.Width(), screen.Height()
		}
		cacheRender(window, rendered, cols, rows, dim)
		window.RenderedCols, window.RenderedRows = cols, rows
		if renderTraceEnabled {
			traceRender(window, isFocused, inTerminalMode, entryDirty, "fast-unfocused", rendered)
		}
		return rendered
	}

	cursor := screen.CursorPosition()
	cursorX := cursor.X
	cursorY := cursor.Y

	var builder paneTextBuf

	contentW := window.ContentWidth()
	contentH := window.ContentHeight()

	estimatedSize := contentW * contentH
	builder.Grow(estimatedSize)

	maxY := min(contentH, screen.Height())
	maxX := min(contentW, screen.Width())

	scrollbackLen := window.ScrollbackLen()
	window.HoldScrollAnchorAt(scrollbackLen)
	inScrollbackMode := window.ScrollbackOffset > 0

	marks := m.marks()

	findBare := m.Settings.Links == config.LinksAll
	var linkRules *hints.LinkRules
	if findBare && !m.Settings.LinkRules.Empty() {
		linkRules = m.Settings.LinkRules
	}
	var bareRows [][]paneBareSpan
	var linkScratch linkScan
	lineTop := 0
	lineScanned := false
	scannedEnd := -1
	var rowStart []int
	if findBare {
		rowStart = make([]int, maxY)
	}
	schemeState := 0
	lineHasScheme := false
	cutAbove := findBare && paneRowWraps(window, -1)
	cutBelow := findBare && paneRowWraps(window, maxY-1)
	var openLink uv.Link
	hoverByID := hasLinkRun && linkRun.Marked && linkParamID(linkRun.Params) != ""

	inCopyMode := window.InCopyMode()
	showCopyCursor := window.CopyModeVisible()
	copyModeCursorX, copyModeCursorY := -1, -1
	if showCopyCursor {
		copyModeCursorX = window.CopyMode.CursorX
		copyModeCursorY = window.CopyMode.CursorY
	}

	drawFakeCursor := !useRealCursor && !m.Resizing && !overlayOpen && isFocused && inTerminalMode && !inCopyMode && !screen.IsCursorHidden()

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

	inVisualMode := inCopyMode && window.CopyMode.State.InVisual()

	if inVisualMode {
		visualSelection = pool.GetHighlightGrid()
		visualSelection.Init(maxY, maxX)
		defer pool.PutHighlightGrid(visualSelection)
	}
	if inVisualMode && window.CopyMode.State == terminal.CopyModeVisualBlock {
		fillBlockSelection(visualSelection, window, screen, window.CopyMode.VisualStart, window.CopyMode.VisualEnd,
			scrollbackLen, maxY, maxX)
	} else if inVisualMode {
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

	flashGrid, flashBand := m.copyFlashGrid(window, screen, scrollbackLen, maxY, maxX)
	if flashGrid != nil {
		defer pool.PutHighlightGrid(flashGrid)
	}

	var (
		dimScratch   uv.Cell
		dimMemo      blendMemo
		dimFg, dimBg color.Color
		dimPal       *[16]color.Color
		dimT         float64
		dimText      bool
	)
	if dim > 0 {
		dimFg, dimBg, dimPal = m.paneDimGround()
		dimT = float64(dim) / 100
		dimText = m.Settings.DimForeground
	}

	ground := m.paneGround()
	var groundScratch uv.Cell

	gridExact := true

	var batchBuilder strings.Builder
	var currentStyle lipgloss.Style
	var batchHasStyle bool
	var currentStyleCached bool
	var currentPrefix, currentSuffix string
	var prevStyle uv.Style
	var prevValid bool
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

	styleMatches := func(cell *uv.Cell, isCursorPos bool) bool {
		if !prevValid && cell == nil {
			return prevIsCursor == isCursorPos
		}
		if !prevValid || cell == nil {
			return false
		}
		return prevIsCursor == isCursorPos &&
			prevStyle.Attrs == cell.Style.Attrs &&
			prevStyle.Underline == cell.Style.Underline &&
			(prevStyle.Fg == cell.Style.Fg || safeColorEquals(prevStyle.Fg, cell.Style.Fg)) &&
			(prevStyle.Bg == cell.Style.Bg || safeColorEquals(prevStyle.Bg, cell.Style.Bg)) &&
			(prevStyle.UnderlineColor == cell.Style.UnderlineColor ||
				safeColorEquals(prevStyle.UnderlineColor, cell.Style.UnderlineColor))
	}
	notePrev := func(cell *uv.Cell) {
		if prevValid = cell != nil; prevValid {
			prevStyle = cell.Style
		}
	}

	for y := 0; y < maxY; y++ {
		if y > 0 {
			builder.WriteRune('\n')
		}
		if findBare {
			rowStart[y] = builder.Len()
		}
		schemeState = 0

		batchBuilder.Reset()
		batchHasStyle = false
		prevValid = false

		var sbLine uv.Line
		sbRow := inScrollbackMode && y < window.ScrollbackOffset
		if sbRow {
			scrollbackIndex := scrollbackLen - window.ScrollbackOffset + y
			if scrollbackIndex >= 0 && scrollbackIndex < scrollbackLen {
				sbLine = vt.ClipHistoryRow(window.ScrollbackLine(scrollbackIndex), maxX)
			}
		}

		lineEndX := maxX - 1
		if inVisualMode && window.CopyMode.State != terminal.CopyModeVisualBlock &&
			visualSelection != nil && visualSelection.HasRow(y) {
			if inScrollbackMode {
				if sbRow {
					for i := len(sbLine) - 1; i >= 0; i-- {
						if sbLine[i].Width > 0 && sbLine[i].Content != "" && sbLine[i].Content != " " {
							lineEndX = i
							break
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

		var bareRow []paneBareSpan
		if bareRows != nil {
			bareRow = bareRows[y]
		}

		x := 0
		for x < maxX {
			var cell *uv.Cell
			if inScrollbackMode {
				if sbRow {
					if x < len(sbLine) {
						cell = &sbLine[x]
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

			if findBare && !lineScanned && cell != nil {
				var b byte
				if len(cell.Content) == 1 {
					b = cell.Content[0]
				}
				switch {
				case b == ':':
					schemeState = 1
				case b == '/' && schemeState > 0:
					schemeState++
					if schemeState == 3 {
						lineHasScheme = true
					}
				default:
					schemeState = 0
				}
			}

			if openLink.URL != "" || bareRow != nil || (cell != nil && cell.Link.URL != "" && frameLink(cell.Link.URL)) {
				var cellLink uv.Link
				if cell != nil && cell.Link.URL != "" && frameLink(cell.Link.URL) {
					cellLink = cell.Link
				}
				if cellLink.URL == "" && bareRow != nil {
					cellLink = bareLinkFor(bareRow, x)
				}
				if cellLink != openLink {
					flushBatch()
					if openLink.URL != "" {
						builder.WriteString(resetHyperlink)
					}
					if cellLink.URL != "" {
						builder.WriteString(vt.SetHostHyperlink(cellLink.URL, cellLink.Params))
					}
					openLink = cellLink
				}
			}

			if showCopyCursor && x == copyModeCursorX && y == copyModeCursorY {
				char := " "
				charWidth := 1
				if cell != nil && cell.Content != "" {
					char = cell.Content
				}
				if cell != nil && cell.Width > 0 {
					charWidth = cell.Width
				}

				flushBatch()

				builder.WriteString(renderStyledText(marks.cursor, char))

				prevValid = false
				prevIsCursor = false

				x += charWidth
				continue
			}

			char := " "
			if cell != nil && cell.Content != "" {
				char = string(cell.Content)
			}

			if flashGrid != nil && flashGrid.Get(y, x) {
				hasGlyph := char != "" && char != " "
				cellBg := m.terminalBg()
				if ground.on() {
					cellBg = ground.bg
				}
				if cell != nil && cell.Style.Bg != nil {
					cellBg = cell.Style.Bg
				}
				if st, lit := flashBand.styleFor(x, y, hasGlyph, cellBg); lit {
					flushBatch()
					builder.WriteString(renderStyledText(st, char))
					notePrev(cell)
					prevIsCursor = false
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}
			}

			if inVisualMode && visualSelection != nil && visualSelection.Get(y, x) && x <= lineEndX {
				flushBatch()

				builder.WriteString(renderStyledText(marks.selection, char))
				notePrev(cell)
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

					builder.WriteString(renderStyledText(marks.match, char))
					notePrev(cell)
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

					builder.WriteString(renderStyledText(marks.search, char))
					notePrev(cell)
					prevIsCursor = false
					cellWidth := 1
					if cell != nil && cell.Width > 1 {
						cellWidth = cell.Width
					}
					x += cellWidth
					continue
				}
			}

			if hasLinkRun && (linkRun.Contains(x, y) ||
				(hoverByID && cell != nil && cell.Link.URL == linkRun.URL && cell.Link.Params == linkRun.Params)) {
				flushBatch()

				builder.WriteString(renderStyledText(linkHoverStyle(), char))
				notePrev(cell)
				prevIsCursor = false
				cellWidth := 1
				if cell != nil && cell.Width > 1 {
					cellWidth = cell.Width
				}
				x += cellWidth
				continue
			}

			isCursorPos := drawFakeCursor && x == cursorX && y == cursorY

			if x > 0 && !styleMatches(cell, isCursorPos) {
				flushBatch()
			}

			if batchBuilder.Len() == 0 && (shouldApplyStyle(cell) || isCursorPos || (dimT > 0 && cell != nil)) {
				styleCell := cell
				if dimT > 0 {
					styleCell = dimCell(&dimScratch, cell, dimFg, dimBg, dimPal, dimT, dimText, &dimMemo)
				}
				if isCursorPos {
					styleCell = paneGroundCell(&groundScratch, styleCell, ground)
				}
				currentStyle, currentPrefix, currentSuffix = buildCellStyleCachedANSI(styleCell, isCursorPos)
				currentStyleCached = true
				batchHasStyle = true
			}
			batchBuilder.WriteString(char)

			notePrev(cell)
			prevIsCursor = isCursorPos

			cellWidth := 1
			if cell != nil && cell.Width > 1 {
				cellWidth = cell.Width
			}
			x += cellWidth
		}

		if x != maxX {
			gridExact = false
		}

		flushBatch()
		if openLink.URL != "" {
			builder.WriteString(resetHyperlink)
			openLink = uv.Link{}
		}

		switch {
		case !findBare:
		case lineScanned:
			if y == scannedEnd {
				lineTop = y + 1
				lineScanned, lineHasScheme = false, false
			}
		case lineHasScheme || linkRules != nil || (lineTop == 0 && cutAbove) || (y == maxY-1 && cutBelow):
			if lineTop < 0 {
				lineTop = y
				for lineTop > 0 && paneRowWraps(window, lineTop-1) {
					lineTop--
				}
			}
			if y+1 < maxY && paneRowWraps(window, y) {
				break
			}
			if bareRows == nil {
				bareRows = make([][]paneBareSpan, maxY)
			}
			spansBefore := bareSpanCount(bareRows, lineTop, y)
			bareRows = appendBareSpans(bareRows, window, lineTop, y, maxX, maxY, linkRules, &linkScratch)
			if bareSpanCount(bareRows, lineTop, y) == spansBefore {
				lineTop = y + 1
				lineHasScheme = false
				break
			}
			lineScanned, scannedEnd = true, y
			lineStart := rowStart[lineTop]
			builder.Truncate(lineStart)
			y = lineTop - 1
			if lineTop > 0 {
				builder.Truncate(lineStart - 1)
			}
			continue
		default:
			lineTop = -1
		}
	}

	content := builder.String()

	cols, rows := 0, 0
	if gridExact && maxX == contentW && maxY == contentH {
		cols, rows = maxX, maxY
	}
	window.RenderedCols, window.RenderedRows = cols, rows
	if !flashing {
		cacheRender(window, content, cols, rows, dim)
	}
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
