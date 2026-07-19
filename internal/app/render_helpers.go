package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

var (
	baseButtonStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000"))
)

func getBorder() lipgloss.Border {
	return config.GetBorderForStyle()
}

func getNormalBorder() lipgloss.Border {
	return getBorder()
}

// RightString returns a right-aligned string with decorative borders.
func RightString(str string, width int, color color.Color) string {
	spaces := width - lipgloss.Width(str)
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	fg := style.Foreground(color)

	if spaces < 0 {
		return ""
	}

	return fg.Render(config.GetWindowBorderTopLeft()+strings.Repeat(config.GetWindowBorderTop(), spaces)) +
		str +
		fg.Render(config.GetWindowBorderTopRight())
}

func makeRounded(content string, color color.Color) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	render := style.Foreground(color).Render
	content = render(config.GetWindowPillLeft()) + content + render(config.GetWindowPillRight())
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
func getWindowTitle(window *terminal.Window, position int, isRenaming bool, renameBuffer string, maxWidth int) string {
	windowName := ""
	if window.CustomName != "" {
		windowName = window.CustomName
	} else if window.Title() != "" && !isDefaultTitle(window.Title(), window.ID) {
		// Only show terminal-set title if it's not the default "Terminal <id>" format
		windowName = window.Title()
	}

	if isRenaming {
		// While renaming, the buffer is the title: running it through the format
		// would show the user something other than what they are typing.
		windowName = renameBuffer + "_"
	} else if windowName != "" || config.WindowTitleFormat != "" {
		// A format that mentions only {index} or {cwd} still has something to
		// say about a window whose title is empty.
		windowName = config.FormatWindowTitle(windowName, position, window.CWD())
	}

	if windowName == "" {
		return ""
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
			return ""
		}
	}
	return windowName
}

func addToBorder(content string, color color.Color, window *terminal.Window, position int, isRenaming bool, renameBuffer string, isTiling bool) string {
	width := max(lipgloss.Width(content)-2, 0)
	titlePos := config.WindowTitlePosition

	style := pool.GetStyle()
	defer pool.PutStyle(style)

	// Build window buttons first so we know their width
	var buttons string
	var buttonsWidth int
	if config.HideWindowButtons {
		buttons = ""
		buttonsWidth = 0
	} else {
		buttonStyle := baseButtonStyle.Background(color)
		cross := buttonStyle.Render(config.GetWindowButtonClose())
		dash := buttonStyle.Render("  - ")

		if isTiling {
			buttons = makeRounded(dash+cross, color)
		} else {
			square := buttonStyle.Render(" □ ")
			buttons = makeRounded(dash+square+cross, color)
		}
		buttonsWidth = lipgloss.Width(buttons)
	}

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
		windowName = getWindowTitle(window, position, isRenaming, renameBuffer, titleMaxWidth)
	}

	borderStyle := style.Foreground(color)

	// Build top border
	var topBorder string
	if titlePos == "top" && windowName != "" {
		// Title on top with buttons on the right
		topBorder = renderTitleWithButtons(windowName, buttons, width, color, true)
	} else {
		// Normal top border with buttons on right
		topBorder = RightString(buttons, width, color)
	}

	// Build bottom border with optional scrollback position indicator
	var bottomBorder string
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
		bottomBorder = renderTitleBadge(windowName, width, color, false)
	} else if scrollIndicator != "" {
		// Bottom border with scrollback position indicator on the right
		indicatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Bold(true)
		indicator := indicatorStyle.Render(scrollIndicator)
		indicatorWidth := lipgloss.Width(indicator)
		lineWidth := max(width-indicatorWidth, 0)
		bottomBorder = borderStyle.Render(config.GetWindowBorderBottomLeft()+strings.Repeat(config.GetWindowBorderBottom(), lineWidth)) + indicator + borderStyle.Render(config.GetWindowBorderBottomRight())
	} else {
		bottomBorder = borderStyle.Render(config.GetWindowBorderBottomLeft() + strings.Repeat(config.GetWindowBorderBottom(), width) + config.GetWindowBorderBottomRight())
	}

	lines := strings.Split(content, "\n")

	if len(lines) > 0 {
		lines[len(lines)-1] = bottomBorder
	}
	return topBorder + "\n" + strings.Join(lines, "\n")
}

// renderTitleWithButtons renders a top/bottom border with a title badge and buttons.
func renderTitleWithButtons(windowName string, buttons string, width int, color color.Color, isTop bool) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	borderStyle := style.Foreground(color)
	nameStyle := baseButtonStyle.Background(color)

	var borderChar, cornerLeft, cornerRight string
	if isTop {
		borderChar = config.GetWindowBorderTop()
		cornerLeft = config.GetWindowBorderTopLeft()
		cornerRight = config.GetWindowBorderTopRight()
	} else {
		borderChar = config.GetWindowBorderBottom()
		cornerLeft = config.GetWindowBorderBottomLeft()
		cornerRight = config.GetWindowBorderBottomRight()
	}

	// Build name badge
	leftCircle := borderStyle.Render(config.GetWindowPillLeft())
	nameText := nameStyle.Render(" " + windowName + " ")
	rightCircle := borderStyle.Render(config.GetWindowPillRight())
	nameBadge := leftCircle + nameText + rightCircle

	nameBadgeWidth := lipgloss.Width(nameBadge)
	buttonsWidth := lipgloss.Width(buttons)

	// Calculate padding between title and buttons
	middlePadding := width - nameBadgeWidth - buttonsWidth
	if middlePadding < 0 {
		// Not enough space, just show buttons
		return RightString(buttons, width, color)
	}

	return borderStyle.Render(cornerLeft) +
		nameBadge +
		borderStyle.Render(strings.Repeat(borderChar, middlePadding)) +
		buttons +
		borderStyle.Render(cornerRight)
}

// renderTitleBadge renders a border with a centered title badge.
func renderTitleBadge(windowName string, width int, color color.Color, isTop bool) string {
	style := pool.GetStyle()
	defer pool.PutStyle(style)
	borderStyle := style.Foreground(color)
	nameStyle := baseButtonStyle.Background(color)

	var borderChar, cornerLeft, cornerRight string
	if isTop {
		borderChar = config.GetWindowBorderTop()
		cornerLeft = config.GetWindowBorderTopLeft()
		cornerRight = config.GetWindowBorderTopRight()
	} else {
		borderChar = config.GetWindowBorderBottom()
		cornerLeft = config.GetWindowBorderBottomLeft()
		cornerRight = config.GetWindowBorderBottomRight()
	}

	if windowName == "" {
		return borderStyle.Render(cornerLeft + strings.Repeat(borderChar, width) + cornerRight)
	}

	leftCircle := borderStyle.Render(config.GetWindowPillLeft())
	nameText := nameStyle.Render(" " + windowName + " ")
	rightCircle := borderStyle.Render(config.GetWindowPillRight())
	nameBadge := leftCircle + nameText + rightCircle

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

func buildOptimizedCellStyleCached(cell *uv.Cell) lipgloss.Style {
	return GetGlobalStyleCache().Get(cell, false, true)
}

func buildCellStyleCached(cell *uv.Cell, isCursor bool) lipgloss.Style {
	return GetGlobalStyleCache().Get(cell, isCursor, false)
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

func (m *OS) isPositionInSelection(window *terminal.Window, x, y int) bool {
	if !window.IsSelecting && window.SelectedText == "" {
		return false
	}

	startX, startY := window.SelectionStart.X, window.SelectionStart.Y
	endX, endY := window.SelectionEnd.X, window.SelectionEnd.Y

	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	if y < startY || y > endY {
		return false
	}
	if y == startY && y == endY {
		return x >= startX && x <= endX
	} else if y == startY {
		return x >= startX
	} else if y == endY {
		return x <= endX
	} else {
		return true
	}
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
