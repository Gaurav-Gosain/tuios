package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// agentStateIndicator returns the one-cell glyph that marks a pane's agent state
// in its window title, or the empty string for none/unset. The glyphs are
// deliberately distinct shapes rather than the same shape in different colors, so
// the state reads at a glance and survives a monochrome capture: a filled circle
// working, a triangle needs-input, a hollow circle idle, a filled square done,
// and a cross errored.
//
// ASCII-only terminals get a parallel set that keeps the same five states
// apart in one cell. Every surface that shows agent state goes through this
// function, so the rail, the title bars and the palette can never disagree.
func agentStateIndicator(state string) string {
	if overlay.UseASCII() {
		switch session.AgentState(state) {
		case session.AgentStateWorking:
			return "*"
		case session.AgentStateNeedsInput:
			return "!"
		case session.AgentStateIdle:
			return "o"
		case session.AgentStateDone:
			return "#"
		case session.AgentStateErrored:
			return "x"
		default:
			return ""
		}
	}
	switch session.AgentState(state) {
	case session.AgentStateWorking:
		return "●"
	case session.AgentStateNeedsInput:
		return "▲"
	case session.AgentStateIdle:
		return "○"
	case session.AgentStateDone:
		return "■"
	case session.AgentStateErrored:
		return "×"
	default:
		return ""
	}
}

// badgeStyle is the ink a window's title badge is written in. The colour is
// picked against the badge's own fill rather than fixed at black: the fill is
// the border colour, which follows the theme, and black on a theme's dark red
// measured 1.40:1 on the worst of them.
func badgeStyle(badgeBg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(theme.ContrastText(badgeBg)).Background(badgeBg)
}

// titleBadgeText renders the text segment of a window title badge, coloring a
// leading agent-state glyph with the state's palette color while keeping the
// badge background intact. Each segment carries the full style, so the glyph's
// color can never bleed into the name or reset the badge behind it. The shape
// stays the state carrier (agentStateIndicator); the color is reinforcement,
// exactly as in the sidebar and the palette rows.
func titleBadgeText(windowName, agentState string, badgeBg color.Color) string {
	nameStyle := badgeStyle(badgeBg)
	glyph := agentStateIndicator(agentState)
	if glyph == "" || !strings.HasPrefix(windowName, glyph) {
		return nameStyle.Render(" " + windowName + " ")
	}
	rest := strings.TrimPrefix(windowName, glyph)
	// The badge's fill is the border colour and the glyph's colour is the
	// state's, both drawn from the same theme: a done agent on a focused pane
	// was bright green on bright green, the same colour on every theme in the
	// registry. The shape carries the state, so the glyph is lifted only to the
	// mark floor and keeps as much of its hue as it can.
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

// borderRowGlyphs returns the fill character and the two corners a top or a
// bottom border row is drawn from.
func borderRowGlyphs(isTop bool, s *config.Settings) (fill, cornerLeft, cornerRight string) {
	if isTop {
		return s.GetWindowBorderTop(), s.GetWindowBorderTopLeft(), s.GetWindowBorderTopRight()
	}
	return s.GetWindowBorderBottom(), s.GetWindowBorderBottomLeft(), s.GetWindowBorderBottomRight()
}

// windowTitleBadge wraps a window's name in the pill caps the title bar shows
// it in.
func windowTitleBadge(windowName, agentState string, col color.Color, s *config.Settings) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	render := style.Foreground(col).Render
	return render(s.GetWindowPillLeft()) +
		titleBadgeText(windowName, agentState, col) +
		render(s.GetWindowPillRight())
}

// buttonBorderRow is a drawn title-bar row together with the column its control
// pill landed on. The two come back as one value because they are one decision:
// layoutBorderRow picks the end the pill goes on, draws it there, and reports
// that same column for the hit rectangles. Nothing measures the finished row
// back, which is what stops the drawn pill and the pressable cells disagreeing
// when the pill moves to the other end.
type buttonBorderRow struct {
	text string
	// pillStart is the pill's offset from the row's first cell, or -1 when the
	// row carries no pill.
	pillStart int
}

// layoutBorderRow draws one border row of the given inner width carrying the
// control pill, and the title badge when there is one and it fits.
//
// The badge sits at the end the pill does not, so the two stay as far apart as
// the bar allows and a press aimed at one is never near the other. When they do
// not both fit the badge is dropped rather than the pill: a name the bar cannot
// show is still readable from the dock, while a close button nobody can press
// is simply gone.
func layoutBorderRow(badge, pill string, width int, col color.Color, isTop bool, s *config.Settings) buttonBorderRow {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	render := style.Foreground(col).Render

	fill, cornerLeft, cornerRight := borderRowGlyphs(isTop, s)

	pillWidth := lipgloss.Width(pill)
	padding := width - lipgloss.Width(badge) - pillWidth
	if padding < 0 {
		badge, padding = "", width-pillWidth
	}
	if padding < 0 {
		// Not even the controls fit. The row comes back empty, which is how a
		// bar that could not be drawn has always been reported.
		return buttonBorderRow{pillStart: -1}
	}

	middle := render(strings.Repeat(fill, padding))
	// A bar with no controls leaves the badge where it has always been, since
	// there is nothing for it to make room for.
	if pill != "" && s.WindowButtonPosition == config.WindowButtonPositionLeft {
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

func makeRounded(content string, color color.Color, s *config.Settings) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	render := style.Foreground(color).Render
	content = render(s.GetWindowPillLeft()) + content + render(s.GetWindowPillRight())
	return content
}

// isDefaultTitle checks if the title is the auto-generated default (e.g., "Terminal 8bf1c038").
func isDefaultTitle(title, windowID string) bool {
	if len(windowID) < 8 {
		return false
	}
	return title == "Terminal "+windowID[:8]
}

// getWindowTitle returns the display name for a window, truncated to fit within maxWidth.
// Returns empty string if title should be hidden or doesn't fit.
// position is the window's 1-based place in its workspace, used by the {index}
// placeholder of appearance.window_title_format.
func getWindowTitle(window *terminal.Window, position int, maxWidth int, s *config.Settings) string {
	// Titles reach the badge as chrome, so launder the same decorative junk the
	// sidebar and palette drop; our own state glyph is added below, untouched.
	windowName := ""
	if window.CustomName != "" {
		windowName = printableTitle(window.CustomName)
	} else if t := window.Title(); t != "" && !isDefaultTitle(t, window.ID) {
		// Only show terminal-set title if it's not the default "Terminal <id>" format
		windowName = printableTitle(t)
	}

	if windowName != "" || s.WindowTitleFormat != "" {
		// A format that mentions only {index} or {cwd} still has something to
		// say about a window whose title is empty.
		windowName = s.FormatWindowTitle(windowName, position, window.CWD())
	}

	// The agent-state indicator shows even for a window with no name, so a pane
	// running an agent is always marked.
	indicator := agentStateIndicator(window.AgentState)

	if windowName == "" {
		return indicator
	}

	// Reserve room for the indicator and its trailing space before truncating.
	if indicator != "" {
		maxWidth = max(maxWidth-2, 0)
	}

	maxNameLen := max(maxWidth-6, 0)
	nameWidth := ansi.StringWidth(windowName)
	if nameWidth > maxNameLen {
		if maxNameLen > 3 {
			// Truncate by runes to handle unicode properly
			runes := []rune(windowName)
			truncated := string(runes)
			for ansi.StringWidth(truncated) > maxNameLen-3 && len(runes) > 0 {
				runes = runes[:len(runes)-1]
				truncated = string(runes)
			}
			windowName = truncated + "..."
		} else {
			return indicator
		}
	}
	if indicator != "" {
		return indicator + " " + windowName
	}
	return windowName
}

// addToBorder draws the title bar and the bottom bar around an already
// rendered border box. width is the box's inner width, the box without its two
// border cells, and the caller passes it because the caller is what decided it.
//
// It used to be recovered here with lipgloss.Width(content)-2, which walks
// every row of the rendered box through a grapheme-cluster iterator to find the
// widest one. That is a full scan of styled text per pane per frame to learn a
// number the caller already held: at 158x40 the scan measured 144933 ns
// against 1.357 ns for reading it, and a profile of the flood benchmark put
// 11% of the whole client's samples in it. See TestBorderBoxInnerWidthIsKnown
// for the proof that the two answers agree.
func (m *OS) addToBorder(content string, width int, color color.Color, window *terminal.Window, position int, isTiling bool) string {
	top, bottom := m.windowBorderRows(width, color, window, position, isTiling)

	lines := strings.Split(content, "\n")
	// The box arrived with a bottom edge lipgloss drew and this one replaces,
	// because only this function knows what the bar has to carry.
	if len(lines) > 0 {
		lines[len(lines)-1] = bottom
	}
	return top + "\n" + strings.Join(lines, "\n")
}

// windowBorderRows builds the two chrome rows a bordered pane is framed with:
// the title bar that goes above the body and the bar that closes it below.
// width is the box's inner width, the box without its two border cells.
//
// It is separate from addToBorder because the fused box path (fastWindowBox)
// needs the same two rows without a rendered box to splice them into.
func (m *OS) windowBorderRows(width int, color color.Color, window *terminal.Window, position int, isTiling bool) (topBorder, bottomBorder string) {
	width = max(width, 0)
	titlePos := m.Settings.WindowTitlePosition

	style := pool.GetStyle()
	defer pool.PutStyle(style)

	// Build window buttons first so we know their width, and record where each
	// one landed as the pill is assembled rather than measuring it back
	// afterwards.
	buttons, hits := m.buildWindowButtons(color, window, isTiling)
	buttonsWidth := lipgloss.Width(buttons)

	// Calculate available width for title based on position
	var titleMaxWidth int
	if titlePos == "top" {
		// Title on top shares space with buttons
		titleMaxWidth = width - buttonsWidth - 2 // -2 for some padding
	} else {
		titleMaxWidth = width
	}

	windowName := ""
	if titlePos != "hidden" {
		windowName = getWindowTitle(window, position, titleMaxWidth, &m.Settings)
	}

	borderStyle := style.Foreground(color)

	// Build the top border. The titled and the bare bar go through one layout,
	// so the end the pill sits on is decided once and the columns it reports
	// are the ones it drew on.
	badge := ""
	if titlePos == "top" && windowName != "" {
		badge = windowTitleBadge(windowName, window.AgentState, color, &m.Settings)
	}
	row := layoutBorderRow(badge, buttons, width, color, true, &m.Settings)
	topBorder = row.text
	m.recordWindowButtons(window.ID, placeWindowButtons(hits, window, row.pillStart))

	// Build bottom border with optional scrollback position indicator
	scrollIndicator := ""
	// Show scroll position when in copy mode with scroll offset
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
		bottomBorder = renderTitleBadge(windowName, window.AgentState, width, color, false, &m.Settings)
	} else if scrollIndicator != "" {
		// Bottom border with scrollback position indicator on the right
		indicatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Bold(true)
		indicator := indicatorStyle.Render(scrollIndicator)
		indicatorWidth := lipgloss.Width(indicator)
		lineWidth := max(width-indicatorWidth, 0)
		bottomBorder = borderStyle.Render(m.Settings.GetWindowBorderBottomLeft()+strings.Repeat(m.Settings.GetWindowBorderBottom(), lineWidth)) + indicator + borderStyle.Render(m.Settings.GetWindowBorderBottomRight())
	} else {
		bottomBorder = borderStyle.Render(m.Settings.GetWindowBorderBottomLeft() + strings.Repeat(m.Settings.GetWindowBorderBottom(), width) + m.Settings.GetWindowBorderBottomRight())
	}

	return topBorder, bottomBorder
}

// renderTitleBadge renders a border with a centered title badge.
func renderTitleBadge(windowName, agentState string, width int, color color.Color, isTop bool, s *config.Settings) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	borderStyle := style.Foreground(color)

	borderChar, cornerLeft, cornerRight := borderRowGlyphs(isTop, s)

	if windowName == "" {
		return borderStyle.Render(cornerLeft + strings.Repeat(borderChar, width) + cornerRight)
	}

	nameBadge := windowTitleBadge(windowName, agentState, color, s)
	badgeWidth := lipgloss.Width(nameBadge)
	totalPadding := width - badgeWidth

	if totalPadding < 0 {
		return borderStyle.Render(cornerLeft + strings.Repeat(borderChar, width) + cornerRight)
	}

	leftPadding := totalPadding / 2
	rightPadding := totalPadding - leftPadding

	return borderStyle.Render(cornerLeft+strings.Repeat(borderChar, leftPadding)) +
		nameBadge +
		borderStyle.Render(strings.Repeat(borderChar, rightPadding)+cornerRight)
}

func styleToANSI(s lipgloss.Style) (prefix string, suffix string) {
	var te ansi.Style

	fg := s.GetForeground()
	bg := s.GetBackground()

	if _, ok := fg.(lipgloss.NoColor); !ok && fg != nil {
		te = te.ForegroundColor(ansi.Color(fg))
	}
	if _, ok := bg.(lipgloss.NoColor); !ok && bg != nil {
		te = te.BackgroundColor(ansi.Color(bg))
	}

	if s.GetBold() {
		te = te.Bold()
	}
	if s.GetItalic() {
		te = te.Italic(true)
	}
	if s.GetUnderline() {
		te = te.Underline(true)
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
	return cell.Style.Fg != nil || cell.Style.Bg != nil || cell.Style.Attrs != 0
}

// buildOptimizedCellStyleCachedANSI returns the cached style together with its
// cached ANSI escape prefix/suffix, avoiding a styleToANSI rebuild on flush.
func buildOptimizedCellStyleCachedANSI(cell *uv.Cell) (lipgloss.Style, string, string) {
	return GetGlobalStyleCache().GetWithANSI(cell, false, true)
}

// buildCellStyleCachedANSI returns the cached style together with its cached
// ANSI escape prefix/suffix, avoiding a styleToANSI rebuild on flush.
func buildCellStyleCachedANSI(cell *uv.Cell, isCursor bool) (lipgloss.Style, string, string) {
	return GetGlobalStyleCache().GetWithANSI(cell, isCursor, false)
}

func buildOptimizedCellStyle(cell *uv.Cell) lipgloss.Style {
	cellStyle := lipgloss.NewStyle()

	if cell == nil {
		return cellStyle
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

	return cellStyle
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
		// Unknown type  - attempt RGBA() and recover on panic
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
		if attrs&1 != 0 {
			cellStyle = cellStyle.Bold(true)
		}
		if attrs&2 != 0 {
			cellStyle = cellStyle.Faint(true)
		}
		if attrs&4 != 0 {
			cellStyle = cellStyle.Italic(true)
		}
		if attrs&32 != 0 {
			cellStyle = cellStyle.Reverse(true)
		}
		if attrs&128 != 0 {
			cellStyle = cellStyle.Strikethrough(true)
		}
	}

	return cellStyle
}

// truncateToWidth cuts line to at most width cells as measured by
// ansi.StringWidth.
//
// ansi.Truncate and ansi.StringWidth disagree about malformed UTF-8: for a line
// carrying invalid bytes, Truncate can return a string that StringWidth then
// measures one or more cells over the limit. That reaches the compositor as a
// row wider than the space the layer was given, which bleeds into the pane next
// door. Guest programs can put arbitrary bytes in an OSC title and those titles
// are rendered into the window chrome, so this is reachable input, not a
// theoretical one. Re-measure and keep cutting until the result really fits.
func truncateToWidth(line string, width int) string {
	if width <= 0 {
		return ""
	}
	out := ansi.Truncate(line, width, "")
	// The overshoot is a cell or two in practice; the loop is bounded by width
	// regardless so a pathological line cannot spin here.
	for target := width; ansi.StringWidth(out) > width && target > 0; {
		target--
		out = ansi.Truncate(line, target, "")
	}
	return out
}

func clipWindowContent(content string, x, y, viewportWidth, viewportHeight int) (string, int, int) {
	lines := strings.Split(content, "\n")
	windowHeight := len(lines)

	// Reject on the axes that cost nothing to test first. Measuring the frame
	// width walks every line, and a window rejected for being off the right
	// edge or off the top or bottom does not need the width at all, so paying
	// for it before these three tests was pure waste.
	if x >= viewportWidth || y+windowHeight <= 0 || y >= viewportHeight {
		return "", max(x, 0), max(y, 0)
	}

	// The window is as wide as its widest line, not as wide as its first one.
	// Measuring only lines[0] under-reports the width whenever the top row is
	// blank, and the unfocused fast render path trims trailing spaces, so a
	// full-screen application with an empty first row (nvim, among others)
	// produced a frame starting with an empty line and measured as zero wide.
	// The offscreen guard below then read x+0 <= 0 as true for the leftmost
	// tile and discarded the whole frame, compositing the pane as bare
	// background while the rest of the layout carried on. The same
	// under-measurement also let the horizontal clip below be skipped for
	// content that really did overrun the viewport.
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
			if w > maxWidth+clipLeft {
				tempLine = truncateToWidth(line, maxWidth+clipLeft)
			}

			if clipLeft > 0 {
				result := strings.Builder{}
				pos := 0
				skipCount := clipLeft

				runes := []rune(tempLine)
				runeIdx := 0
				for runeIdx < len(runes) {
					if runes[runeIdx] == '\x1b' {
						seqStart := runeIdx
						runeIdx++

						if runeIdx < len(runes) && runes[runeIdx] == '[' {
							runeIdx++
							for runeIdx < len(runes) && (runes[runeIdx] < 0x40 || runes[runeIdx] > 0x7E) {
								runeIdx++
							}
							if runeIdx < len(runes) {
								runeIdx++
							}
						} else if runeIdx < len(runes) && runes[runeIdx] == ']' {
							runeIdx++
							for runeIdx < len(runes) {
								if runes[runeIdx] == '\x07' || (runes[runeIdx] == '\x1b' && runeIdx+1 < len(runes) && runes[runeIdx+1] == '\\') {
									runeIdx++
									if runeIdx < len(runes) && runes[runeIdx-1] == '\x1b' {
										runeIdx++
									}
									break
								}
								runeIdx++
							}
						}

						// Always include escape sequences  - they set terminal state (colors, styles)
						result.WriteString(string(runes[seqStart:runeIdx]))
						continue
					}

					if pos >= skipCount {
						result.WriteRune(runes[runeIdx])
					}
					pos++
					runeIdx++
				}

				clippedLines[lineIdx] = result.String() + "\x1b[0m"
			} else {
				clippedLines[lineIdx] = tempLine
				if w > maxWidth {
					clippedLines[lineIdx] += "\x1b[0m"
				}
			}
		}

		// Enforce the width contract on the finished rows rather than trusting
		// the arithmetic that built them. The left-skip above walks runes and
		// counts one position per rune, but converting a line that carries
		// invalid bytes to runes turns each bad byte into a replacement
		// character with a width of its own, so the assembled row can come out
		// several cells wider than the space it is being placed in and bleed
		// into the pane next door. Guest programs can put arbitrary bytes in an
		// OSC title and those titles are rendered into the window chrome.
		for i, line := range clippedLines {
			if ansi.StringWidth(line) > maxWidth {
				clippedLines[i] = truncateToWidth(line, maxWidth) + "\x1b[0m"
			}
		}

		return strings.Join(clippedLines, "\n"), finalX, finalY
	}

	return strings.Join(visibleLines, "\n"), finalX, finalY
}

// workspacePosition returns the window's 1-based place among the windows of its
// workspace, the same number the leader-digit shortcuts address it by. Returns
// 0 for a window that is not in the list, which the title format renders as-is.
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
