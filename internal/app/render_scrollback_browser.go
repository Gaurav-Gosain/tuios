package app

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/scrollback"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/charmbracelet/x/ansi"
)

func (m *OS) renderScrollbackBrowser() string {
	browser := m.ScrollbackBrowser
	if browser == nil {
		return ""
	}

	w := m.GetRenderWidth()
	h := m.GetRenderHeight()
	if w < 30 || h < 10 {
		return ""
	}

	pal := m.overlayUI()
	accent := pal.AccentBright
	dimFg := pal.FgMute
	cursorRow := overlay.RowState{Cursor: true, Focused: true}
	selBg := pal.Ground(cursorRow, pal.Surface)
	selFg := pal.Fg
	normalFg := pal.FgDim
	multiClr := pal.Success
	okClr := pal.Success
	failClr := pal.Warn
	searchBgColor := configInk(m.Settings.SearchBg)
	matchFg := configInk(m.Settings.MatchFg)
	matchBgColor := configInk(m.Settings.MatchBg)

	innerW := w - 6
	innerH := h - 4
	if innerW < 20 || innerH < 6 {
		return ""
	}

	headerLines := 2
	footerLines := 2
	paneH := max(innerH-headerLines-footerLines, 1)
	leftW := browserListWidth(browserItems(browser), innerW)
	rightW := innerW - leftW - 3
	if rightW < 8 {
		rightW = 8
		leftW = innerW - rightW - 3
	}

	title := styled(accent, true, "Scrollback browser")

	modes := []string{"Commands", "JSON", "Paths"}
	var tabs []string
	for i, name := range modes {
		if scrollback.BrowserMode(i) == browser.Mode {
			tabs = append(tabs, lipgloss.NewStyle().
				Bold(true).
				Foreground(matchFg).
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

	methodStr := ""
	if browser.ParseMethod != "" {
		methodStr = styled(dimFg, false, " ("+browser.ParseMethod+")")
		if browser.MarkerCount > 0 {
			methodStr = styled(dimFg, false, fmt.Sprintf(" (%s, %d markers)", browser.ParseMethod, browser.MarkerCount))
		}
	}

	modeBadge := ""
	if browser.OutputMode && browser.Vim != nil {
		switch browser.Vim.Mode {
		case scrollback.VimVisualChar:
			modeBadge = " " + lipgloss.NewStyle().
				Bold(true).
				Foreground(matchFg).
				Background(matchBgColor).
				Render(" VISUAL ") + " "
		case scrollback.VimVisualLine:
			modeBadge = " " + lipgloss.NewStyle().
				Bold(true).
				Foreground(matchFg).
				Background(matchBgColor).
				Render(" VISUAL LINE ") + " "
		default:
			modeBadge = " " + lipgloss.NewStyle().
				Bold(true).
				Foreground(matchFg).
				Background(accent).
				Render(" OUTPUT ") + " "
		}
	}

	headerStr := title + methodStr + modeBadge + "  " + strings.Join(tabs, "")
	vim := browser.Vim
	if browser.OutputMode && vim != nil && vim.Mode == scrollback.VimSearch {
		headerStr += "  " + searchFieldText(searchBgColor, "/", &vim.Search)
	} else if browser.OutputMode && vim != nil && vim.Search.String() != "" {
		matchInfo := ""
		if n := len(vim.SearchMatches); n > 0 {
			matchInfo = fmt.Sprintf(" [%d/%d]", vim.SearchCurrent+1, n)
		}
		headerStr += "  " + styled(searchBgColor, false, "/"+vim.Search.String()+matchInfo)
	} else if browser.SearchActive {
		headerStr += "  " + searchFieldText(searchBgColor, "/", &browser.Search)
	} else if q := browser.Search.String(); q != "" {
		headerStr += "  " + styled(searchBgColor, false, "["+q+"]")
	}

	lines := make([]string, 0, innerH)
	lines = append(lines, fitLine(headerStr, innerW))
	lines = append(lines, styled(dimFg, false, strings.Repeat("─", innerW)))

	browser.LayoutLeftW = leftW
	browser.LayoutRightW = rightW
	browser.LayoutPaneH = paneH

	leftLines := buildLeftPane(browser, leftW, paneH, selBg, selFg, normalFg, dimFg, multiClr)
	if sel := browser.SelectedIdx - browser.ScrollOffset; sel >= 0 && sel < len(leftLines) {
		leftLines[sel] = pal.Mark(leftLines[sel], cursorRow)
	}
	rightLines := buildRightPane(browser, rightW, paneH, dimFg, accent, okClr, failClr, browserMarks{
		cursorBg: pal.Selected,
		visualBg: configInk(m.Settings.SelectionBg),
		searchBg: searchBgColor,
		searchFg: configInk(m.Settings.SearchFg),
		normalFg: pal.FgDim,
	})

	sep := styled(dimFg, false, "│")
	for i := range paneH {
		l := safeIdx(leftLines, i, leftW)
		r := safeIdx(rightLines, i, rightW)
		lines = append(lines, l+" "+sep+" "+r)
	}

	lines = append(lines, styled(dimFg, false, strings.Repeat("─", innerW)))

	nav := m.listMoveKeys(true)
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
				{nav, "nav"},
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
				{nav, "nav"},
				{"l", "output"},
				{"y", "copy"},
				{"/", "search"},
				{"Tab", "mode"},
				{"q", "close"},
			}
		case scrollback.ModePaths:
			hints = []browserHint{
				{nav, "nav"},
				{"l", "output"},
				{"y", "copy"},
				{"Enter", "paste"},
				{"/", "search"},
				{"Tab", "mode"},
				{"q", "close"},
			}
		}
	}
	hints = slices.DeleteFunc(hints, func(h browserHint) bool { return h.key == "" })
	lines = append(lines, fitLine(browserFooterHints(hints, innerW, &m.Settings, pal), innerW))

	for len(lines) < innerH {
		lines = append(lines, strings.Repeat(" ", innerW))
	}
	content := strings.Join(lines[:innerH], "\n")

	box := lipgloss.NewStyle().
		Padding(1, 2).
		Border(overlay.EdgeBorder()).
		BorderForeground(browserFrameInk(pal)).
		Render(content)

	result := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)

	if browser.ShowHelp {
		result = overlayBrowserHelp(w, h, browser.OutputMode, accent, dimFg, pal)
	}

	return result
}

func browserItems(browser *scrollback.Browser) []string {
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
	return items
}

const (
	browserListMinWidth = 18
	browserListMaxShare = 40
)

func browserListWidth(items []string, innerW int) int {
	widest := 0
	for _, it := range items {
		if w := lipgloss.Width(strings.ReplaceAll(it, "\n", " ")); w > widest {
			widest = w
		}
	}
	want := widest + 3
	ceiling := max(innerW*browserListMaxShare/100, browserListMinWidth)
	return min(max(want, browserListMinWidth), ceiling)
}

func buildLeftPane(
	browser *scrollback.Browser, width, height int,
	selBg, selFg, normalFg, dimFg, multiClr color.Color,
) []string {
	items := browserItems(browser)

	out := make([]string, height)
	if len(items) == 0 {
		out[0] = fitLine(styled(dimFg, false, "(no items)"), width)
		for i := 1; i < height; i++ {
			out[i] = strings.Repeat(" ", width)
		}
		return out
	}

	vis := browser.ScrollOffset
	if browser.SelectedIdx >= vis+height {
		vis = browser.SelectedIdx - height + 1
	}
	if browser.SelectedIdx < vis {
		vis = browser.SelectedIdx
	}
	vis = max(vis, 0)
	browser.ScrollOffset = vis

	textW := width - 2
	for i := range height {
		idx := vis + i
		if idx >= len(items) {
			out[i] = strings.Repeat(" ", width)
			continue
		}

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

type browserMarks struct {
	cursorBg color.Color
	visualBg color.Color
	searchBg color.Color
	searchFg color.Color
	normalFg color.Color
}

func buildRightPane(
	browser *scrollback.Browser, width, height int,
	dimFg, accent, okClr, failClr color.Color, marks browserMarks,
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

	out[0] = fitLine(styled(accent, true, ansi.Truncate(headerText, width, "…")), width)

	preview := styledPreview
	if preview == "" {
		preview = plainPreview
	}

	previewLines := strings.Split(preview, "\n")

	availH := height - 1
	browser.LastPaneHeight = height

	var scroll int
	vim := browser.Vim

	if browser.OutputMode && vim != nil {
		effectiveH := availH
		if len(vim.Lines) > availH {
			effectiveH = availH - 1
		}
		vim.ViewHeight = effectiveH
		vim.EnsureVisible()
		scroll = vim.ScrollY

		maxScroll := max(len(vim.Lines)-effectiveH, 0)
		if scroll > maxScroll {
			scroll = maxScroll
		}
		scroll = max(scroll, 0)
		vim.ScrollY = scroll
	} else {
		scroll = browser.PreviewScroll
		maxScroll := max(len(previewLines)-availH, 0)
		if scroll > maxScroll {
			scroll = maxScroll
		}
		scroll = max(scroll, 0)
	}

	cursorBg := marks.cursorBg
	visualBg := marks.visualBg
	searchBg := marks.searchBg
	normalTextFg := marks.normalFg
	visualFg := overlay.Readable(normalTextFg, visualBg)
	cursorFg := overlay.Readable(normalTextFg, cursorBg)

	for i := range availH {
		pIdx := scroll + i
		if pIdx >= len(previewLines) {
			out[i+1] = empty
			continue
		}

		if browser.OutputMode && vim != nil {
			out[i+1] = renderVimLine(vim, pIdx, width,
				cursorBg, cursorFg, visualBg, visualFg, searchBg, marks.searchFg, normalTextFg)
		} else {
			truncated := ansi.Truncate(previewLines[pIdx], width, "")
			out[i+1] = padANSI(truncated, width)
		}
	}

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

func renderVimLine(vim *scrollback.VimState, lineIdx, width int,
	cursorBg, cursorFg, visualBg, visualFg, searchBg, searchFg, normalFg color.Color,
) string {
	lineSelected := false
	text := ""
	if lineIdx < len(vim.Lines) {
		text = vim.Lines[lineIdx]
	}
	runes := []rune(text)
	if len(runes) > width {
		runes = runes[:width]
	}

	hl := make([]int, len(runes))

	for _, m := range vim.SearchMatchesOnLine(lineIdx) {
		for x := m.StartX; x < m.EndX && x < len(runes); x++ {
			if x >= 0 {
				hl[x] = 1
			}
		}
	}

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
			lineSelected = true
		}
	}

	if lineIdx == vim.CursorY && vim.CursorX >= 0 && vim.CursorX < len(runes) {
		hl[vim.CursorX] = 3
	}

	if len(runes) == 0 {
		if lineIdx == vim.CursorY {
			return lipgloss.NewStyle().
				Foreground(normalFg).
				Bold(true).
				Background(cursorBg).
				Render(" ") + strings.Repeat(" ", max(width-1, 0))
		}
		return strings.Repeat(" ", width)
	}

	hasHL := false
	for _, h := range hl {
		if h != 0 {
			hasHL = true
			break
		}
	}

	cursorPastEnd := lineIdx == vim.CursorY && vim.CursorX >= len(runes)

	if !hasHL && !cursorPastEnd {
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

	var ansiStates []string
	if vim.StyledLines != nil && lineIdx < len(vim.StyledLines) {
		ansiStates = parseANSIRuneStates(vim.StyledLines[lineIdx], len(runes))
	}

	var result strings.Builder
	i := 0
	for i < len(runes) {
		j := i + 1
		for j < len(runes) && hl[j] == hl[i] {
			j++
		}
		span := string(runes[i:j])
		switch hl[i] {
		case 3:
			result.WriteString("\x1b[0m")
			result.WriteString(lipgloss.NewStyle().
				Foreground(cursorFg).
				Bold(true).
				Background(cursorBg).
				Render(span))
		case 2:
			result.WriteString("\x1b[0m")
			result.WriteString(lipgloss.NewStyle().
				Foreground(visualFg).
				Background(visualBg).
				Render(span))
		case 1:
			result.WriteString("\x1b[0m")
			result.WriteString(lipgloss.NewStyle().
				Foreground(searchFg).
				Background(searchBg).
				Render(span))
		default:
			if ansiStates != nil {
				result.WriteString("\x1b[0m")
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
			Foreground(normalFg).
			Bold(true).
			Background(cursorBg).
			Render(" "))
	}

	rendered := result.String()
	w := ansi.StringWidth(rendered)
	if w < width {
		pad := strings.Repeat(" ", width-w)
		if lineSelected {
			pad = lipgloss.NewStyle().Background(visualBg).Render(pad)
		}
		rendered += pad
	}
	return rendered
}

func parseANSIRuneStates(styled string, numRunes int) []string {
	states := make([]string, numRunes)
	var current strings.Builder
	runeIdx := 0
	i := 0

	for i < len(styled) && runeIdx < numRunes {
		if styled[i] == '\x1b' && i+1 < len(styled) && styled[i+1] == '[' {
			j := i + 2
			for j < len(styled) && !isCSIFinal(styled[j]) {
				j++
			}
			if j < len(styled) {
				j++
				seq := styled[i:j]
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
			i++
			for i < len(styled) && styled[i] != '\x1b' && styled[i] >= 0x20 {
				i++
			}
			continue
		}

		if runeIdx < numRunes {
			states[runeIdx] = current.String()
		}
		_, size := utf8.DecodeRuneInString(styled[i:])
		i += size
		runeIdx++
	}

	return states
}

func isCSIFinal(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}

func overlayBrowserHelp(w, h int, outputMode bool, accent, dimFg color.Color, pal overlay.Palette) string {
	var helpLines []string
	titleText := "Keys"

	if outputMode {
		titleText = "Output mode keys"
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

	maxLineW := 0
	for _, l := range helpLines {
		if lw := len([]rune(l)); lw > maxLineW {
			maxLineW = lw
		}
	}
	modalW := min(maxLineW+6, w-10)
	modalH := min(len(helpLines)+4, h-6)

	title := styled(accent, true, titleText)
	separator := styled(dimFg, false, strings.Repeat("─", modalW-4))
	dismissHint := styled(dimFg, false, "Press ? or esc to close")

	var modalLines []string
	modalLines = append(modalLines, fitLine(title, modalW-4))
	modalLines = append(modalLines, separator)

	contentH := modalH - 4
	for i, l := range helpLines {
		if i >= contentH {
			break
		}
		if l == "" {
			modalLines = append(modalLines, strings.Repeat(" ", modalW-4))
		} else {
			modalLines = append(modalLines, fitLine(styled(pal.FgDim, false, l), modalW-4))
		}
	}

	for len(modalLines) < modalH-2 {
		modalLines = append(modalLines, strings.Repeat(" ", modalW-4))
	}
	modalLines = append(modalLines, fitLine(dismissHint, modalW-4))

	modalContent := strings.Join(modalLines, "\n")
	modal := lipgloss.NewStyle().
		Padding(1, 2).
		Border(overlay.EdgeBorder()).
		BorderForeground(browserFrameInk(pal)).
		Render(modalContent)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, modal)
}

type browserHint struct {
	key  string
	desc string
}

func browserFrameInk(pal overlay.Palette) color.Color {
	if !isNilColor(pal.Frame) {
		return pal.Frame
	}
	return pal.AccentBright
}

func browserFooterHints(hints []browserHint, maxWidth int, s *config.Settings, pal overlay.Palette) string {
	pillBg := pal.Card
	pillFg := pal.Fg
	descFg := pal.FgMute

	pillLeft := s.GetWindowPillLeft()
	pillRight := s.GetWindowPillRight()

	renderHint := func(h browserHint) string {
		left := lipgloss.NewStyle().Foreground(pillBg).Render(pillLeft)
		key := lipgloss.NewStyle().Background(pillBg).Foreground(pillFg).Render(" " + h.key + " ")
		right := lipgloss.NewStyle().Foreground(pillBg).Render(pillRight)
		desc := lipgloss.NewStyle().Foreground(descFg).Render(" " + h.desc)
		return left + key + right + desc
	}

	moreIndicator := renderHint(browserHint{"?", "help"})
	moreW := ansi.StringWidth(moreIndicator)

	var parts []string
	totalW := 0
	truncated := false
	for i, h := range hints {
		part := renderHint(h)
		partW := ansi.StringWidth(part)
		sep := 0
		if i > 0 {
			sep = 2
		}

		needed := totalW + sep + partW
		if needed+2+moreW > maxWidth {
			truncated = true
			break
		}

		parts = append(parts, part)
		totalW = needed
	}

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

func searchFieldText(c color.Color, prefix string, l *textedit.Line) string {
	before, at, after := l.Parts()
	if at == "" {
		at = " "
	}
	return styled(c, true, prefix+before) +
		lipgloss.NewStyle().Foreground(c).Reverse(true).Render(at) +
		styled(c, true, after)
}
