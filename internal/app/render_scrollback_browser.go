package app

import (
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"github.com/charmbracelet/x/ansi"
)

// renderScrollbackBrowser renders the scrollback browser as a full-screen overlay.
func (m *OS) renderScrollbackBrowser() string {
	browser, ok := m.ScrollbackBrowser.(*scrollback.Browser)
	if browser == nil || !ok {
		return ""
	}

	w := m.GetRenderWidth()
	h := m.GetRenderHeight()
	if w < 30 || h < 10 {
		return ""
	}

	// Colors
	accent := lipgloss.Color("#4fc3f7")
	dimFg := lipgloss.Color("#505068")
	selBg := lipgloss.Color("#1e3a5f")
	selFg := lipgloss.Color("#ffffff")
	normalFg := lipgloss.Color("#b0b0c0")
	multiClr := lipgloss.Color("#66bb6a")
	okClr := lipgloss.Color("#66bb6a")
	failClr := lipgloss.Color("#ef5350")

	// Inner content area (border=2, padding=2 top/bot, 4 left/right)
	innerW := w - 6 // border(2) + padding-left(2) + padding-right(2)
	innerH := h - 4 // border(2) + padding-top(1) + padding-bottom(1)
	if innerW < 20 || innerH < 6 {
		return ""
	}

	// Pane layout
	headerLines := 2 // title + separator
	footerLines := 2 // separator + hints
	paneH := max(innerH-headerLines-footerLines, 1)
	leftW := max(innerW*30/100, 10)
	rightW := innerW - leftW - 3 // " │ "
	if rightW < 8 {
		rightW = 8
		leftW = innerW - rightW - 3
	}

	// === HEADER ===
	title := styled(accent, true, "Scrollback Browser")

	// Help-overlay style tabs
	modes := []string{"Commands", "JSON", "Paths"}
	var tabs []string
	for i, name := range modes {
		if scrollback.BrowserMode(i) == browser.Mode {
			tabs = append(tabs, lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#000000")).
				Background(accent).
				Padding(0, 1).
				Render(name))
		} else {
			tabs = append(tabs, lipgloss.NewStyle().
				Foreground(dimFg).
				Padding(0, 1).
				Render(name))
		}
	}

	// Method indicator
	methodStr := ""
	if browser.ParseMethod != "" {
		methodStr = styled(dimFg, false, " ("+browser.ParseMethod+")")
		if browser.MarkerCount > 0 {
			methodStr = styled(dimFg, false, fmt.Sprintf(" (%s, %d markers)", browser.ParseMethod, browser.MarkerCount))
		}
	}

	// Output mode badge
	modeBadge := ""
	if browser.OutputMode && browser.Vim != nil {
		switch browser.Vim.Mode {
		case scrollback.VimVisualChar:
			modeBadge = " " + lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#ffeb3b")).
				Render(" VISUAL ") + " "
		case scrollback.VimVisualLine:
			modeBadge = " " + lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#ffeb3b")).
				Render(" VISUAL LINE ") + " "
		default:
			modeBadge = " " + lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#000000")).
				Background(accent).
				Render(" OUTPUT ") + " "
		}
	}

	headerStr := title + methodStr + modeBadge + "  " + strings.Join(tabs, "")
	vim := browser.Vim
	if browser.OutputMode && vim != nil && vim.Mode == scrollback.VimSearch {
		headerStr += "  " + styled(lipgloss.Color("#ffeb3b"), true, "/"+vim.SearchQuery+"█")
	} else if browser.OutputMode && vim != nil && vim.SearchQuery != "" {
		matchInfo := ""
		if n := len(vim.SearchMatches); n > 0 {
			matchInfo = fmt.Sprintf(" [%d/%d]", vim.SearchCurrent+1, n)
		}
		headerStr += "  " + styled(lipgloss.Color("#ffeb3b"), false, "/"+vim.SearchQuery+matchInfo)
	} else if browser.SearchActive {
		headerStr += "  " + styled(lipgloss.Color("#ffeb3b"), true, "/"+browser.SearchQuery+"█")
	} else if browser.SearchQuery != "" {
		headerStr += "  " + styled(lipgloss.Color("#ffeb3b"), false, "["+browser.SearchQuery+"]")
	}

	lines := make([]string, 0, innerH)
	lines = append(lines, fitLine(headerStr, innerW))
	lines = append(lines, styled(dimFg, false, strings.Repeat("─", innerW)))

	// Store layout geometry for mouse input mapping.
	browser.LayoutLeftW = leftW
	browser.LayoutRightW = rightW
	browser.LayoutPaneH = paneH

	// === PANES ===
	leftLines := buildLeftPane(browser, leftW, paneH, selBg, selFg, normalFg, dimFg, multiClr)
	rightLines := buildRightPane(browser, rightW, paneH, dimFg, accent, okClr, failClr)

	sep := styled(dimFg, false, "│")
	for i := range paneH {
		l := safeIdx(leftLines, i, leftW)
		r := safeIdx(rightLines, i, rightW)
		lines = append(lines, l+" "+sep+" "+r)
	}

	// === FOOTER ===
	lines = append(lines, styled(dimFg, false, strings.Repeat("─", innerW)))

	var hints []browserHint
	if browser.OutputMode {
		hints = []browserHint{
			{"hjkl", "move"},
			{"w/b/e", "word"},
			{"f/t", "find"},
			{"/", "search"},
			{"v/V", "visual"},
			{"y", "yank"},
			{"esc", "back"},
		}
	} else {
		switch browser.Mode {
		case scrollback.ModeCommands:
			hints = []browserHint{
				{"j/k", "nav"},
				{"l", "output"},
				{"J/K", "scroll"},
				{"y", "copy"},
				{"c", "cmd"},
				{"Enter", "paste"},
				{"/", "search"},
				{"Tab", "mode"},
				{"q", "close"},
			}
		case scrollback.ModeJSON:
			hints = []browserHint{
				{"j/k", "nav"},
				{"l", "output"},
				{"y", "copy"},
				{"/", "search"},
				{"Tab", "mode"},
				{"q", "close"},
			}
		case scrollback.ModePaths:
			hints = []browserHint{
				{"j/k", "nav"},
				{"l", "output"},
				{"y", "copy"},
				{"Enter", "paste"},
				{"/", "search"},
				{"Tab", "mode"},
				{"q", "close"},
			}
		}
	}
	lines = append(lines, fitLine(browserFooterHints(hints, innerW), innerW))

	// Pad to exact height
	for len(lines) < innerH {
		lines = append(lines, strings.Repeat(" ", innerW))
	}
	content := strings.Join(lines[:innerH], "\n")

	box := lipgloss.NewStyle().
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Background(lipgloss.Color("#1a1a2a")).
		Render(content)

	result := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)

	// Help overlay (rendered on top)
	if browser.ShowHelp {
		result = overlayBrowserHelp(w, h, browser.OutputMode, accent, dimFg)
	}

	return result
}

func buildLeftPane(
	browser *scrollback.Browser, width, height int,
	selBg, selFg, normalFg, dimFg, multiClr color.Color,
) []string {
	var items []string
	switch browser.Mode {
	case scrollback.ModeCommands:
		for _, idx := range browser.FilteredIdx {
			items = append(items, browser.Blocks[idx].Command)
		}
	case scrollback.ModeJSON:
		for _, idx := range browser.FilteredJSON {
			jb := browser.JSONBlocks[idx]
			items = append(items, jb.Name+": "+firstLine(jb.Raw))
		}
	case scrollback.ModePaths:
		for _, idx := range browser.FilteredPaths {
			items = append(items, browser.PathBlocks[idx].Raw)
		}
	}

	out := make([]string, height)
	if len(items) == 0 {
		out[0] = fitLine(styled(dimFg, false, "(no items)"), width)
		for i := 1; i < height; i++ {
			out[i] = strings.Repeat(" ", width)
		}
		return out
	}

	// Keep selection visible
	vis := browser.ScrollOffset
	if browser.SelectedIdx >= vis+height {
		vis = browser.SelectedIdx - height + 1
	}
	if browser.SelectedIdx < vis {
		vis = browser.SelectedIdx
	}
	vis = max(vis, 0)
	browser.ScrollOffset = vis

	textW := width - 2 // prefix "  " or "> "
	for i := range height {
		idx := vis + i
		if idx >= len(items) {
			out[i] = strings.Repeat(" ", width)
			continue
		}

		// Clean text for display
		text := strings.ReplaceAll(items[idx], "\n", " ")
		text = ansi.Truncate(text, textW, "…")

		if idx == browser.SelectedIdx {
			out[i] = styled(selFg, true, "> ") +
				lipgloss.NewStyle().Foreground(selFg).Bold(true).Background(selBg).
					Width(textW).Render(text)
		} else if browser.MultiSelect[idx] {
			out[i] = styled(multiClr, false, "* ") +
				fitLine(styled(normalFg, false, text), textW)
		} else {
			out[i] = "  " + fitLine(styled(normalFg, false, text), textW)
		}
	}
	return out
}

func buildRightPane(
	browser *scrollback.Browser, width, height int,
	dimFg, accent, okClr, failClr color.Color,
) []string {
	out := make([]string, height)
	empty := strings.Repeat(" ", width)

	headerText := ""
	styledPreview := ""
	plainPreview := ""

	switch browser.Mode {
	case scrollback.ModeCommands:
		if cmd := browser.SelectedCommand(); cmd != nil {
			headerText = "$ " + cmd.Command
			if cmd.ExitCode >= 0 {
				c := okClr
				if cmd.ExitCode != 0 {
					c = failClr
				}
				headerText += lipgloss.NewStyle().Foreground(c).
					Render(fmt.Sprintf(" [%d]", cmd.ExitCode))
			}
			styledPreview = cmd.StyledOutput
			plainPreview = cmd.Output
		}
	case scrollback.ModeJSON:
		if jb := browser.SelectedJSON(); jb != nil {
			headerText = "JSON: " + jb.Name
			plainPreview = jb.Pretty
		}
	case scrollback.ModePaths:
		if pb := browser.SelectedPath(); pb != nil {
			if pb.IsURL {
				headerText = "URL"
			} else {
				headerText = "Path"
			}
			if pb.Line > 0 {
				headerText += fmt.Sprintf(" (line %d", pb.Line)
				if pb.Col > 0 {
					headerText += fmt.Sprintf(", col %d", pb.Col)
				}
				headerText += ")"
			}
			plainPreview = pb.Raw
		}
	}

	if headerText == "" && styledPreview == "" && plainPreview == "" {
		out[0] = fitLine(styled(dimFg, false, "(select a command)"), width)
		for i := 1; i < height; i++ {
			out[i] = empty
		}
		return out
	}

	// Header
	out[0] = fitLine(styled(accent, true, ansi.Truncate(headerText, width, "…")), width)

	// Preview  - prefer styled text for display
	preview := styledPreview
	if preview == "" {
		preview = plainPreview
	}

	previewLines := strings.Split(preview, "\n")

	availH := height - 1
	browser.LastPaneHeight = height

	// Determine scroll position
	var scroll int
	vim := browser.Vim

	if browser.OutputMode && vim != nil {
		// Reserve last line for scroll indicator when content overflows
		effectiveH := availH
		if len(vim.Lines) > availH {
			effectiveH = availH - 1
		}
		vim.ViewHeight = effectiveH
		vim.EnsureVisible()
		scroll = vim.ScrollY

		// Clamp scroll using vim's line count (authoritative for cursor navigation)
		maxScroll := max(len(vim.Lines)-effectiveH, 0)
		if scroll > maxScroll {
			scroll = maxScroll
		}
		scroll = max(scroll, 0)
		vim.ScrollY = scroll // write back so EnsureVisible stays consistent
	} else {
		scroll = browser.PreviewScroll
		maxScroll := max(len(previewLines)-availH, 0)
		if scroll > maxScroll {
			scroll = maxScroll
		}
		scroll = max(scroll, 0)
	}

	cursorBg := lipgloss.Color("#2e5090")
	visualBg := lipgloss.Color("#1e3a5f")
	searchBg := lipgloss.Color("#3a3520")
	normalTextFg := lipgloss.Color("#b0b0c0")

	for i := range availH {
		pIdx := scroll + i
		if pIdx >= len(previewLines) {
			out[i+1] = empty
			continue
		}

		if browser.OutputMode && vim != nil {
			out[i+1] = renderVimLine(vim, pIdx, width,
				cursorBg, visualBg, searchBg, normalTextFg)
		} else {
			truncated := ansi.Truncate(previewLines[pIdx], width, "")
			out[i+1] = padANSI(truncated, width)
		}
	}

	// Scroll indicator on last line (reserved space when content overflows)
	lineCount := len(previewLines)
	if browser.OutputMode && vim != nil {
		lineCount = len(vim.Lines)
	}
	if lineCount > availH {
		var indText string
		if browser.OutputMode && vim != nil {
			indText = fmt.Sprintf("ln %d/%d", vim.CursorY+1, lineCount)
		} else {
			indText = fmt.Sprintf("(%d/%d)", scroll+1, lineCount)
		}
		ind := styled(dimFg, false, indText)
		indW := ansi.StringWidth(ind)
		if indW < width {
			out[height-1] = strings.Repeat(" ", width-indW) + ind
		}
	}

	return out
}

// renderVimLine renders a single line with character-level cursor, visual, and search highlighting.
func renderVimLine(vim *scrollback.VimState, lineIdx, width int,
	cursorBg, visualBg, searchBg, normalFg color.Color,
) string {
	// Get plain text from VimState lines
	text := ""
	if lineIdx < len(vim.Lines) {
		text = vim.Lines[lineIdx]
	}
	runes := []rune(text)
	if len(runes) > width {
		runes = runes[:width]
	}

	// Per-rune highlight classification
	// 0=none, 1=search, 2=visual, 3=cursor
	hl := make([]int, len(runes))

	// Search matches (lowest priority)
	for _, m := range vim.SearchMatchesOnLine(lineIdx) {
		for x := m.StartX; x < m.EndX && x < len(runes); x++ {
			if x >= 0 {
				hl[x] = 1
			}
		}
	}

	// Visual selection (medium priority)
	switch vim.Mode {
	case scrollback.VimVisualChar:
		sx, sy, ex, ey := vim.VisualBounds()
		if lineIdx >= sy && lineIdx <= ey {
			startX, endX := 0, len(runes)-1
			if lineIdx == sy {
				startX = sx
			}
			if lineIdx == ey {
				endX = ex
			}
			for x := startX; x <= endX && x < len(runes); x++ {
				if x >= 0 {
					hl[x] = 2
				}
			}
		}
	case scrollback.VimVisualLine:
		lo, hi := vim.VisualStartY, vim.CursorY
		if lo > hi {
			lo, hi = hi, lo
		}
		if lineIdx >= lo && lineIdx <= hi {
			for x := range runes {
				hl[x] = 2
			}
		}
	}

	// Cursor cell (highest priority)
	if lineIdx == vim.CursorY && vim.CursorX >= 0 && vim.CursorX < len(runes) {
		hl[vim.CursorX] = 3
	}

	// Handle empty lines
	if len(runes) == 0 {
		if lineIdx == vim.CursorY {
			// Show cursor block on empty line
			return lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Background(cursorBg).
				Render(" ") + strings.Repeat(" ", max(width-1, 0))
		}
		return strings.Repeat(" ", width)
	}

	// Check if any highlights exist on this line
	hasHL := false
	for _, h := range hl {
		if h != 0 {
			hasHL = true
			break
		}
	}

	// If cursor is past end of line, add a cursor block at end
	cursorPastEnd := lineIdx == vim.CursorY && vim.CursorX >= len(runes)

	if !hasHL && !cursorPastEnd {
		// No highlights  - prefer styled text for ANSI colors
		if vim.StyledLines != nil && lineIdx < len(vim.StyledLines) {
			truncated := ansi.Truncate(vim.StyledLines[lineIdx], width, "")
			return padANSI(truncated, width)
		}
		if lineIdx < len(vim.Lines) {
			truncated := ansi.Truncate(vim.Lines[lineIdx], width, "")
			return padANSI(truncated, width)
		}
		return strings.Repeat(" ", width)
	}

	// Parse per-rune ANSI state from styled line for color preservation
	var ansiStates []string
	if vim.StyledLines != nil && lineIdx < len(vim.StyledLines) {
		ansiStates = parseANSIRuneStates(vim.StyledLines[lineIdx], len(runes))
	}

	// Build styled output, preserving original ANSI colors for non-highlighted chars
	var result strings.Builder
	i := 0
	for i < len(runes) {
		j := i + 1
		for j < len(runes) && hl[j] == hl[i] {
			j++
		}
		span := string(runes[i:j])
		switch hl[i] {
		case 3: // cursor
			result.WriteString("\x1b[0m")
			result.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Background(cursorBg).
				Render(span))
		case 2: // visual
			result.WriteString("\x1b[0m")
			result.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#d0d0e0")).
				Background(visualBg).
				Render(span))
		case 1: // search
			result.WriteString("\x1b[0m")
			result.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#d0c080")).
				Background(searchBg).
				Render(span))
		default:
			// Preserve original ANSI colors for non-highlighted text
			if ansiStates != nil {
				result.WriteString("\x1b[0m")
				// Emit per-rune ANSI state with resets to prevent color leaking
				for k := i; k < j; k++ {
					if k < len(ansiStates) && ansiStates[k] != "" {
						result.WriteString(ansiStates[k])
						result.WriteRune(runes[k])
						result.WriteString("\x1b[0m")
					} else {
						result.WriteRune(runes[k])
					}
				}
			} else {
				result.WriteString(lipgloss.NewStyle().
					Foreground(normalFg).
					Render(span))
			}
		}
		i = j
	}
	result.WriteString("\x1b[0m")

	if cursorPastEnd {
		result.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Background(cursorBg).
			Render(" "))
	}

	rendered := result.String()
	w := ansi.StringWidth(rendered)
	if w < width {
		rendered += strings.Repeat(" ", width-w)
	}
	return rendered
}

// parseANSIRuneStates parses a styled ANSI string and returns per-rune ANSI
// SGR state strings. ansiStates[i] contains the accumulated SGR sequences
// active at rune position i (to be emitted before that rune to restore color).
func parseANSIRuneStates(styled string, numRunes int) []string {
	states := make([]string, numRunes)
	var current strings.Builder
	runeIdx := 0
	i := 0

	for i < len(styled) && runeIdx < numRunes {
		if styled[i] == '\x1b' && i+1 < len(styled) && styled[i+1] == '[' {
			// CSI sequence: ESC [ params final
			j := i + 2
			for j < len(styled) && !isCSIFinal(styled[j]) {
				j++
			}
			if j < len(styled) {
				j++ // include final byte
				seq := styled[i:j]
				// Only track SGR sequences (ending in 'm')
				if styled[j-1] == 'm' {
					if seq == "\x1b[0m" || seq == "\x1b[m" {
						current.Reset()
					} else {
						current.WriteString(seq)
					}
				}
				i = j
			} else {
				i = j
			}
			continue
		}

		if styled[i] == '\x1b' {
			// Non-CSI escape (OSC, etc)  - skip to end
			i++
			for i < len(styled) && styled[i] != '\x1b' && styled[i] >= 0x20 {
				i++
			}
			continue
		}

		// Printable rune
		if runeIdx < numRunes {
			states[runeIdx] = current.String()
		}
		_, size := utf8.DecodeRuneInString(styled[i:])
		i += size
		runeIdx++
	}

	return states
}

// isCSIFinal returns true if byte is a CSI final byte (0x40-0x7E).
func isCSIFinal(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}

// overlayBrowserHelp renders a help modal on top of the browser content.
func overlayBrowserHelp(w, h int, outputMode bool, accent, dimFg color.Color) string {
	var helpLines []string
	titleText := "Keybindings"

	if outputMode {
		titleText = "Output Mode Keybindings"
		helpLines = []string{
			"h j k l      Move cursor",
			"\u2190 \u2193 \u2191 \u2192      Move cursor",
			"w b e        Word forward/backward/end",
			"W B E        WORD forward/backward/end",
			"f F t T      Find char forward/backward/till",
			"; ,          Repeat/reverse char search",
			"0 ^ $        Line start/first-nonblank/end",
			"",
			"gg           Go to top",
			"G            Go to bottom",
			"H M L        Screen top/middle/bottom",
			"{ }          Paragraph up/down",
			"Ctrl+d/u     Half page down/up",
			"Ctrl+f/b     Full page down/up",
			"%            Match bracket",
			"",
			"v            Visual char mode",
			"V            Visual line mode",
			"y            Yank (copy) selection",
			"/            Search in output",
			"n N          Next/prev search match",
			"",
			"esc q        Exit output mode",
			"?            Toggle this help",
		}
	} else {
		helpLines = []string{
			"j k  \u2191\u2193     Navigate commands",
			"l  \u2192        Enter output mode",
			"J K          Scroll preview",
			"g G          Go to top/bottom",
			"Ctrl+d/u     Page down/up",
			"",
			"y            Copy output",
			"c            Copy command",
			"Enter        Paste command to terminal",
			"Space        Multi-select",
			"/            Search",
			"",
			"Tab  1 2 3   Switch mode",
			"q  esc       Close browser",
			"?            Toggle this help",
		}
	}

	// Calculate modal dimensions
	maxLineW := 0
	for _, l := range helpLines {
		if lw := len([]rune(l)); lw > maxLineW {
			maxLineW = lw
		}
	}
	modalW := min(maxLineW+6, w-10) // padding + margin
	modalH := min(len(helpLines)+4, h-6)

	// Build modal content
	title := styled(accent, true, titleText)
	separator := styled(dimFg, false, strings.Repeat("─", modalW-4))
	dismissHint := styled(dimFg, false, "Press ? or esc to close")

	var modalLines []string
	modalLines = append(modalLines, fitLine(title, modalW-4))
	modalLines = append(modalLines, separator)

	contentH := modalH - 4 // title + sep + blank + dismiss
	for i, l := range helpLines {
		if i >= contentH {
			break
		}
		if l == "" {
			modalLines = append(modalLines, strings.Repeat(" ", modalW-4))
		} else {
			modalLines = append(modalLines, fitLine(styled(lipgloss.Color("#b0b0c0"), false, l), modalW-4))
		}
	}

	// Pad remaining
	for len(modalLines) < modalH-2 {
		modalLines = append(modalLines, strings.Repeat(" ", modalW-4))
	}
	modalLines = append(modalLines, fitLine(dismissHint, modalW-4))

	modalContent := strings.Join(modalLines, "\n")
	modal := lipgloss.NewStyle().
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Background(lipgloss.Color("#1a1a2a")).
		Render(modalContent)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, modal)
}

// --- helpers ---

type browserHint struct {
	key  string
	desc string
}

func browserFooterHints(hints []browserHint, maxWidth int) string {
	pillBg := lipgloss.Color("#3a3a5e")
	pillFg := lipgloss.Color("#ffffff")
	descFg := lipgloss.Color("#808098")

	pillLeft := config.GetWindowPillLeft()
	pillRight := config.GetWindowPillRight()

	renderHint := func(h browserHint) string {
		left := lipgloss.NewStyle().Foreground(pillBg).Render(pillLeft)
		key := lipgloss.NewStyle().Background(pillBg).Foreground(pillFg).Render(" " + h.key + " ")
		right := lipgloss.NewStyle().Foreground(pillBg).Render(pillRight)
		desc := lipgloss.NewStyle().Foreground(descFg).Render(" " + h.desc)
		return left + key + right + desc
	}

	moreIndicator := renderHint(browserHint{"?", "help"})
	moreW := ansi.StringWidth(moreIndicator)

	// Try to fit as many hints as possible; append "? help" at the end
	var parts []string
	totalW := 0
	truncated := false
	for i, h := range hints {
		part := renderHint(h)
		partW := ansi.StringWidth(part)
		sep := 0
		if i > 0 {
			sep = 2 // "  " separator
		}

		needed := totalW + sep + partW
		// Reserve space for the "? help" indicator
		if needed+2+moreW > maxWidth {
			truncated = true
			break
		}

		parts = append(parts, part)
		totalW = needed
	}

	// Always show the ? help pill (indicates help is available)
	if truncated || totalW+2+moreW <= maxWidth {
		parts = append(parts, moreIndicator)
	}

	return strings.Join(parts, "  ")
}

func styled(c color.Color, bold bool, text string) string {
	s := lipgloss.NewStyle().Foreground(c)
	if bold {
		s = s.Bold(true)
	}
	return s.Render(text)
}

func fitLine(s string, width int) string {
	w := ansi.StringWidth(s)
	if w > width {
		return ansi.Truncate(s, width, "")
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func padANSI(s string, width int) string {
	w := ansi.StringWidth(s)
	if w < width {
		return s + "\x1b[0m" + strings.Repeat(" ", width-w)
	}
	return s + "\x1b[0m"
}

func safeIdx(lines []string, i, width int) string {
	if i < len(lines) {
		return lines[i]
	}
	return strings.Repeat(" ", width)
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}
