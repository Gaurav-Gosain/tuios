package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

func agentStateIndicator(state string) string {
	return config.AgentStateMark(state, overlay.UseASCII())
}

func agentMarkWidth() int {
	return config.AgentMarkWidth(overlay.UseASCII())
}

func agentMarkLead() int {
	return agentMarkWidth() + 1
}

func agentMarkGap(mark string) string {
	return strings.Repeat(" ", max(agentMarkLead()-lipgloss.Width(mark), 1))
}

func agentMark(state string, doneSeen bool, running int, pal overlay.Palette) (string, color.Color) {
	shown := displayState(state, doneSeen, running)
	return agentStateIndicator(shown), agentGlyphColor(shown, pal)
}

func (m *OS) windowMarkState(w *terminal.Window) string {
	state, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq)
	return displayState(state, seen, w.AgentSubagents)
}

func badgeStyle(badgeBg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(theme.ContrastText(badgeBg)).Background(badgeBg)
}

func titleBadgeText(windowName, agentState string, badgeBg color.Color) string {
	nameStyle := badgeStyle(badgeBg)
	glyph := agentStateIndicator(agentState)
	if glyph == "" || !strings.HasPrefix(windowName, glyph) {
		return nameStyle.Render(" " + windowName + " ")
	}
	rest := strings.TrimPrefix(windowName, glyph)
	glyphStyle := lipgloss.NewStyle().
		Background(badgeBg).
		Foreground(theme.ReadableAt(agentGlyphColor(agentState, theme.UI()), badgeBg, theme.MarkFloor)).
		Bold(true)
	return nameStyle.Render(" ") + glyphStyle.Render(glyph) + nameStyle.Render(rest+" ")
}

func getBorder(s *config.Settings) lipgloss.Border {
	return s.GetBorderForStyle()
}

func getNormalBorder(s *config.Settings) lipgloss.Border {
	return getBorder(s)
}

func borderRowGlyphs(isTop bool, s *config.Settings) (fill, cornerLeft, cornerRight string) {
	if isTop {
		return s.GetWindowBorderTop(), s.GetWindowBorderTopLeft(), s.GetWindowBorderTopRight()
	}
	return s.GetWindowBorderBottom(), s.GetWindowBorderBottomLeft(), s.GetWindowBorderBottomRight()
}

func windowTitleBadge(windowName, agentState string, col color.Color, s *config.Settings) string {
	switch s.WindowTitleStyle {
	case "tab":
		return titleBadgeText(windowName, agentState, col)
	case "plain":
		return titlePlainText(windowName, agentState, col)
	}
	style := lipgloss.NewStyle()
	render := style.Foreground(col).Render
	return render(s.GetWindowPillLeft()) +
		titleBadgeText(windowName, agentState, col) +
		render(s.GetWindowPillRight())
}

func titlePlainText(windowName, agentState string, col color.Color) string {
	nameStyle := lipgloss.NewStyle().Foreground(col)
	glyph := agentStateIndicator(agentState)
	if glyph == "" || !strings.HasPrefix(windowName, glyph) {
		return nameStyle.Render(" " + windowName + " ")
	}
	rest := strings.TrimPrefix(windowName, glyph)
	glyphStyle := lipgloss.NewStyle().Foreground(agentGlyphColor(agentState, theme.UI())).Bold(true)
	return nameStyle.Render(" ") + glyphStyle.Render(glyph) + nameStyle.Render(rest+" ")
}

func titleFill(align string, padding, autoBefore int) (before, after int) {
	switch align {
	case "left":
		before = min(1, padding)
	case "center":
		before = padding / 2
	case "right":
		before = padding - min(1, padding)
	default:
		before = autoBefore
	}
	return before, padding - before
}

type buttonBorderRow struct {
	text      string
	pillStart int
}

func layoutBorderRow(badge, pill string, width int, col color.Color, isTop bool, s *config.Settings) buttonBorderRow {
	style := lipgloss.NewStyle()
	render := style.Foreground(col).Render

	fill, cornerLeft, cornerRight := borderRowGlyphs(isTop, s)

	pillWidth := lipgloss.Width(pill)
	padding := width - lipgloss.Width(badge) - pillWidth
	if padding < 0 {
		badge, padding = "", width-pillWidth
	}
	if padding < 0 {
		return buttonBorderRow{pillStart: -1}
	}

	pillLeft := pill != "" && s.WindowButtonPosition == config.WindowButtonPositionLeft
	if s.WindowTitleAlign != "" && s.WindowTitleAlign != "auto" {
		return alignedBorderRow(badge, pill, padding, pillLeft, render, fill, cornerLeft, cornerRight, s.WindowTitleAlign)
	}
	middle := render(strings.Repeat(fill, padding))
	if pillLeft {
		return buttonBorderRow{
			text:      render(cornerLeft) + pill + middle + badge + render(cornerRight),
			pillStart: 1,
		}
	}
	return buttonBorderRow{
		text:      render(cornerLeft) + badge + middle + pill + render(cornerRight),
		pillStart: 1 + lipgloss.Width(badge) + padding,
	}
}

func alignedBorderRow(badge, pill string, padding int, pillLeft bool, render func(...string) string,
	fill, cornerLeft, cornerRight, align string) buttonBorderRow {
	before, after := titleFill(align, padding, 0)
	run := func(n int) string {
		if n <= 0 {
			return ""
		}
		return render(strings.Repeat(fill, n))
	}
	if pillLeft {
		return buttonBorderRow{
			text:      render(cornerLeft) + pill + run(before) + badge + run(after) + render(cornerRight),
			pillStart: 1,
		}
	}
	return buttonBorderRow{
		text:      render(cornerLeft) + run(before) + badge + run(after) + pill + render(cornerRight),
		pillStart: 1 + before + lipgloss.Width(badge) + after,
	}
}

func isDefaultTitle(title, windowID string) bool {
	if len(windowID) < 8 {
		return false
	}
	return title == "Terminal "+windowID[:8]
}

func paneName(window *terminal.Window, s *config.Settings) string {
	if _, title, ok := tmlWindowParts(window); ok && s.WindowTitlePosition == "top" {
		return printableTitle(title)
	}
	if window.CustomName != "" {
		return printableTitle(window.CustomName)
	}
	if t := window.Title(); t != "" && !isDefaultTitle(t, window.ID) {
		return printableTitle(t)
	}
	return ""
}

func windowTitleText(window *terminal.Window, markState string, position int, maxWidth int, s *config.Settings) string {
	windowName := paneName(window, s)

	if windowName != "" || s.WindowTitleFormat != "" {
		windowName = s.FormatWindowTitle(windowName, position, window.CWD())
	}

	indicator := agentStateIndicator(markState)

	host := window.Host
	if host != "" && window.HostLink != "" {
		host = "[" + window.HostLink + "] " + host
	}
	if windowName == "" {
		if host != "" {
			return joinTitleParts(indicator, host)
		}
		return indicator
	}

	if host != "" {
		windowName = host + ":" + windowName
	}

	if indicator != "" {
		maxWidth = max(maxWidth-lipgloss.Width(indicator)-1, 0)
	}

	maxNameLen := max(maxWidth-6, 0)
	nameWidth := ansi.StringWidth(windowName)
	if nameWidth > maxNameLen {
		if maxNameLen > ansi.StringWidth(overlay.Ellipsis()) {
			windowName = overlay.Truncate(windowName, maxNameLen)
		} else {
			return indicator
		}
	}
	return joinTitleParts(indicator, windowName)
}

func joinTitleParts(indicator, rest string) string {
	if indicator == "" {
		return rest
	}
	if rest == "" {
		return indicator
	}
	return indicator + " " + rest
}

func (m *OS) addToBorder(content string, width int, color color.Color, window *terminal.Window, position int, isTiling bool) string {
	top, bottom := m.windowBorderRows(width, color, window, position, isTiling)

	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		lines[len(lines)-1] = bottom
	}
	return top + "\n" + strings.Join(lines, "\n")
}

func (m *OS) windowBorderRows(width int, color color.Color, window *terminal.Window, position int, isTiling bool) (topBorder, bottomBorder string) {
	width = max(width, 0)
	titlePos := m.Settings.WindowTitlePosition

	style := lipgloss.NewStyle()

	buttons, hits := m.buildWindowButtons(color, window, isTiling)
	buttonsWidth := lipgloss.Width(buttons)

	var titleMaxWidth int
	if titlePos == "top" {
		titleMaxWidth = width - buttonsWidth - 2
	} else {
		titleMaxWidth = width
	}

	markState := m.windowMarkState(window)
	windowName := ""
	ownerTag, ownerW := "", 0
	if titlePos == "top" {
		ownerTag, ownerW = m.tmlOwnerTag(window, &m.Settings, titleMaxWidth)
		titleMaxWidth = max(titleMaxWidth-ownerW, 0)
	}
	if titlePos != "hidden" {
		marked := m.MarkedPaneID != "" && window.ID == m.MarkedPaneID
		if marked {
			titleMaxWidth -= len(markedPaneBadge) + 1
		}
		windowName = windowTitleText(window, markState, position, titleMaxWidth, &m.Settings)
		if marked {
			windowName = strings.TrimSpace(windowName + " " + markedPaneBadge)
		}
	}

	borderStyle := style.Foreground(color)

	badge := ""
	if titlePos == "top" && windowName != "" {
		badge = windowTitleBadge(windowName, markState, m.titleInk(window, color), &m.Settings)
	}
	badge += ownerTag
	row := layoutBorderRow(badge, buttons, width, color, true, &m.Settings)
	topBorder = row.text
	m.recordWindowButtons(window.ID, placeWindowButtons(hits, window, row.pillStart))

	scrollIndicator := ""
	if window.CopyMode != nil && window.CopyMode.Active && window.CopyMode.ScrollOffset > 0 {
		scrollbackLen := 0
		if window.Terminal != nil {
			scrollbackLen = window.Terminal.ScrollbackLen()
		}
		if scrollbackLen > 0 {
			scrollIndicator = fmt.Sprintf(" %d/%d ", window.CopyMode.ScrollOffset, scrollbackLen)
		}
	}

	if titlePos == "bottom" && windowName != "" {
		bottomBorder = renderTitleBadge(windowName, markState, width, color, m.titleInk(window, color), false, &m.Settings)
	} else if scrollIndicator != "" {
		indicatorStyle := lipgloss.NewStyle().Foreground(theme.ScrollIndicator()).Bold(true)
		indicator := indicatorStyle.Render(scrollIndicator)
		indicatorWidth := lipgloss.Width(indicator)
		lineWidth := max(width-indicatorWidth, 0)
		bottomBorder = borderStyle.Render(m.Settings.GetWindowBorderBottomLeft()+strings.Repeat(m.Settings.GetWindowBorderBottom(), lineWidth)) + indicator + borderStyle.Render(m.Settings.GetWindowBorderBottomRight())
	} else {
		bottomBorder = borderStyle.Render(m.Settings.GetWindowBorderBottomLeft() + strings.Repeat(m.Settings.GetWindowBorderBottom(), width) + m.Settings.GetWindowBorderBottomRight())
	}

	return topBorder, bottomBorder
}

func (m *OS) titleInk(window *terminal.Window, border color.Color) color.Color {
	if m.Settings.WindowTitleStyle != "plain" || m.GetFocusedWindow() == window {
		return border
	}
	return theme.UI().FgDim
}

func renderTitleBadge(windowName, agentState string, width int, color, ink color.Color, isTop bool, s *config.Settings) string {
	style := lipgloss.NewStyle()
	borderStyle := style.Foreground(color)

	borderChar, cornerLeft, cornerRight := borderRowGlyphs(isTop, s)

	if windowName == "" {
		return borderStyle.Render(cornerLeft + strings.Repeat(borderChar, width) + cornerRight)
	}

	nameBadge := windowTitleBadge(windowName, agentState, ink, s)
	badgeWidth := lipgloss.Width(nameBadge)
	totalPadding := width - badgeWidth

	if totalPadding < 0 {
		return borderStyle.Render(cornerLeft + strings.Repeat(borderChar, width) + cornerRight)
	}

	leftPadding, rightPadding := titleFill(s.WindowTitleAlign, totalPadding, totalPadding/2)

	return borderStyle.Render(cornerLeft+strings.Repeat(borderChar, leftPadding)) +
		nameBadge +
		borderStyle.Render(strings.Repeat(borderChar, rightPadding)+cornerRight)
}

func styleToANSI(s lipgloss.Style) (prefix string, suffix string) {
	var te ansi.Style

	fg := s.GetForeground()
	bg := s.GetBackground()

	if _, ok := fg.(lipgloss.NoColor); !ok {
		te = te.ForegroundColor(ansi.Color(fg))
	}
	if _, ok := bg.(lipgloss.NoColor); !ok {
		te = te.BackgroundColor(ansi.Color(bg))
	}

	if s.GetBold() {
		te = te.Bold()
	}
	if s.GetItalic() {
		te = te.Italic(true)
	}
	if ul := s.GetUnderlineStyle(); ul != lipgloss.UnderlineNone {
		te = te.UnderlineStyle(ul)
	}
	uc := s.GetUnderlineColor()
	if _, ok := uc.(lipgloss.NoColor); !ok {
		te = te.UnderlineColor(ansi.Color(uc))
	}
	if s.GetStrikethrough() {
		te = te.Strikethrough(true)
	}
	if s.GetBlink() {
		te = te.Blink(true)
	}
	if s.GetFaint() {
		te = te.Faint()
	}
	if s.GetReverse() {
		te = te.Reverse(true)
	}

	ansiStr := te.String()
	if ansiStr != "" {
		return ansiStr, "\x1b[0m"
	}
	return "", ""
}

func renderStyledText(style lipgloss.Style, text string) string {
	prefix, suffix := styleToANSI(style)
	if prefix == "" {
		return text
	}
	return prefix + text + suffix
}

func shouldApplyStyle(cell *uv.Cell) bool {
	if cell == nil {
		return false
	}
	return cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0 ||
		cell.Style.Underline != uv.UnderlineNone || cell.Style.UnderlineColor != nil
}

func buildCellStyleCachedANSI(cell *uv.Cell, isCursor bool) (lipgloss.Style, string, string) {
	return GetGlobalStyleCache().GetWithANSI(cell, isCursor)
}

func isColorSafe(c color.Color) bool {
	if c == nil {
		return false
	}
	switch c.(type) {
	case lipgloss.ANSIColor, lipgloss.NoColor, lipgloss.RGBColor,
		color.RGBA, color.NRGBA, color.Gray, color.Gray16,
		color.RGBA64, color.CMYK, color.Alpha, color.Alpha16,
		color.YCbCr:
		return true
	default:
		safe := true
		func() {
			defer func() {
				if recover() != nil {
					safe = false
				}
			}()
			_, _, _, _ = c.RGBA()
		}()
		return safe
	}
}

func buildCellStyle(cell *uv.Cell, isCursor bool) lipgloss.Style {
	cellStyle := lipgloss.NewStyle()

	if cell == nil {
		return cellStyle
	}

	if isCursor {
		fg, bg := theme.CursorFallback()
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

	if cell.Style.Attrs != 0 {
		attrs := cell.Style.Attrs
		if attrs&uv.AttrBold != 0 {
			cellStyle = cellStyle.Bold(true)
		}
		if attrs&uv.AttrFaint != 0 {
			cellStyle = cellStyle.Faint(true)
		}
		if attrs&uv.AttrItalic != 0 {
			cellStyle = cellStyle.Italic(true)
		}
		if attrs&uv.AttrBlink != 0 {
			cellStyle = cellStyle.Blink(true)
		}
		if attrs&uv.AttrReverse != 0 {
			cellStyle = cellStyle.Reverse(true)
		}
		if attrs&uv.AttrStrikethrough != 0 {
			cellStyle = cellStyle.Strikethrough(true)
		}
	}

	if cell.Style.Underline != uv.UnderlineNone {
		cellStyle = cellStyle.UnderlineStyle(cell.Style.Underline)
	}
	if isColorSafe(cell.Style.UnderlineColor) {
		cellStyle = cellStyle.UnderlineColor(cell.Style.UnderlineColor)
	}

	return cellStyle
}

func truncateToWidth(line string, width int) string {
	out, _ := truncateMeasured(line, width)
	return out
}

func truncateMeasured(line string, width int) (string, int) {
	if width <= 0 {
		return "", 0
	}
	out := ansi.Truncate(line, width, "")
	got := ansi.StringWidth(out)
	for target := width; got > width && target > 0; {
		target--
		out = ansi.Truncate(line, target, "")
		got = ansi.StringWidth(out)
	}
	return out, got
}

func skipCells(line string, n int) string {
	var b strings.Builder
	b.Grow(len(line) + len(sgrReset))
	pos := 0
	pstate := parser.GroundState
	i := 0
	for i < len(line) && pos < n {
		c := line[i]
		state, action := parser.Table.Transition(pstate, c)
		if action != parser.PrintAction && state != parser.Utf8State {
			b.WriteByte(c)
			pstate = state
			i++
			continue
		}
		pstate = parser.GroundState
		var size, width int
		if c < 0x80 && (i+1 == len(line) || line[i+1] < 0x80) {
			size, width = 1, 1
		} else {
			cluster, w := ansi.FirstGraphemeCluster(line[i:], ansi.GraphemeWidth)
			size, width = max(len(cluster), 1), w
		}
		i += size
		if pos+width > n {
			for range pos + width - n {
				b.WriteByte(' ')
			}
		}
		pos += width
	}
	b.WriteString(line[i:])
	b.WriteString(sgrReset)
	return b.String()
}

const sgrReset = "\x1b[0m"

func clipWindowContent(content string, x, y, viewportWidth, viewportHeight int) (string, int, int) {
	lines := strings.Split(content, "\n")
	windowHeight := len(lines)

	if x >= viewportWidth || y+windowHeight <= 0 || y >= viewportHeight {
		return "", max(x, 0), max(y, 0)
	}

	windowWidth := framesWidth(lines)

	if x+windowWidth <= 0 {
		return "", max(x, 0), max(y, 0)
	}

	clipTop := 0
	clipLeft := 0
	finalX := x
	finalY := y

	if y < 0 {
		clipTop = -y
		finalY = 0
	}

	if x < 0 {
		clipLeft = -x
		finalX = 0
	}

	if clipTop >= len(lines) {
		return "", finalX, finalY
	}
	visibleLines := lines[clipTop:]

	maxVisibleLines := viewportHeight - finalY
	if maxVisibleLines < len(visibleLines) {
		visibleLines = visibleLines[:maxVisibleLines]
	}

	if clipLeft > 0 || finalX+windowWidth > viewportWidth {
		maxWidth := viewportWidth - finalX
		clippedLines := make([]string, len(visibleLines))

		for lineIdx, line := range visibleLines {
			w := lineWidth(line)

			if clipLeft >= w {
				clippedLines[lineIdx] = ""
				continue
			}

			tempLine := line
			short := 0
			if w > maxWidth+clipLeft {
				var kept int
				tempLine, kept = truncateMeasured(line, maxWidth+clipLeft)
				short = maxWidth + clipLeft - kept
			}

			if clipLeft > 0 {
				clippedLines[lineIdx] = skipCells(tempLine, clipLeft)
			} else {
				clippedLines[lineIdx] = tempLine
				if w > maxWidth {
					clippedLines[lineIdx] += sgrReset
				}
			}
			if short > 0 {
				clippedLines[lineIdx] += strings.Repeat(" ", short)
			}
		}

		for i, line := range clippedLines {
			if ansi.StringWidth(line) > maxWidth {
				clippedLines[i] = truncateToWidth(line, maxWidth) + "\x1b[0m"
			}
		}

		return strings.Join(clippedLines, "\n"), finalX, finalY
	}

	return strings.Join(visibleLines, "\n"), finalX, finalY
}

func (m *OS) workspacePosition(window *terminal.Window) int {
	position := 0
	for _, w := range m.Windows {
		if w.Workspace != window.Workspace {
			continue
		}
		if m.AutoTiling && w.Minimized {
			continue
		}
		position++
		if w == window {
			return position
		}
	}
	return 0
}
