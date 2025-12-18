package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pool"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Deprecated: Use config.GetWindowPillLeft() instead
const (
	LeftHalfCircle  string = string(rune(0xe0b6))
	RightHalfCircle string = string(rune(0xe0b4))
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

func addToBorder(content string, color color.Color, window *terminal.Window, isRenaming bool, renameBuffer string, isTiling bool) string {
	width := max(lipgloss.Width(content)-2, 0)

	var border string
	if config.HideWindowButtons {
		border = ""
	} else {
		buttonStyle := baseButtonStyle.Background(color)
		cross := buttonStyle.Render(config.GetWindowButtonClose())
		dash := buttonStyle.Render(" — ")

		if isTiling {
			border = makeRounded(dash+cross, color)
		} else {
			square := buttonStyle.Render(" □ ")
			border = makeRounded(dash+square+cross, color)
		}
	}
	centered := RightString(border, width, color)

	style := pool.GetStyle()
	defer pool.PutStyle(style)
	bottomBorderStyle := style.Foreground(color)

	windowName := ""
	if window.CustomName != "" {
		windowName = window.CustomName
	}

	if isRenaming {
		windowName = renameBuffer + "_"
	}

	var bottomBorder string
	if windowName != "" {
		maxNameLen := width - 6
		if maxNameLen > 0 && len(windowName) > maxNameLen {
			if maxNameLen > 3 {
				windowName = windowName[:maxNameLen-3] + "..."
			} else {
				windowName = "..."
			}
		}

		nameStyle := baseButtonStyle.Background(color)

		leftCircle := bottomBorderStyle.Render(config.GetWindowPillLeft())
		nameText := nameStyle.Render(" " + windowName + " ")
		rightCircle := bottomBorderStyle.Render(config.GetWindowPillRight())
		nameBadge := leftCircle + nameText + rightCircle

		badgeWidth := lipgloss.Width(nameBadge)
		totalPadding := width - badgeWidth

		if totalPadding < 0 {
			bottomBorder = bottomBorderStyle.Render(config.GetWindowBorderBottomLeft() + strings.Repeat(config.GetWindowBorderBottom(), width) + config.GetWindowBorderBottomRight())
		} else {
			leftPadding := totalPadding / 2
			rightPadding := totalPadding - leftPadding

			bottomBorder = bottomBorderStyle.Render(config.GetWindowBorderBottomLeft()+strings.Repeat(config.GetWindowBorderBottom(), leftPadding)) +
				nameBadge +
				bottomBorderStyle.Render(strings.Repeat(config.GetWindowBorderBottom(), rightPadding)+config.GetWindowBorderBottomRight())
		}
	} else {
		bottomBorder = bottomBorderStyle.Render(config.GetWindowBorderBottomLeft() + strings.Repeat(config.GetWindowBorderBottom(), width) + config.GetWindowBorderBottomRight())
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		lines[len(lines)-1] = bottomBorder
	}
	return centered + "\n" + strings.Join(lines, "\n")
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
	defer func() {
		_ = recover()
	}()
	_, _, _, _ = c.RGBA()
	return true
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

func clipWindowContent(content string, x, y, viewportWidth, viewportHeight int) (string, int, int) {
	lines := strings.Split(content, "\n")
	windowHeight := len(lines)

	windowWidth := 0
	if len(lines) > 0 {
		windowWidth = ansi.StringWidth(lines[0])
	}

	if x+windowWidth <= 0 || x >= viewportWidth || y+windowHeight <= 0 || y >= viewportHeight {
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
			lineWidth := ansi.StringWidth(line)

			if clipLeft >= lineWidth {
				clippedLines[lineIdx] = ""
				continue
			}

			tempLine := line
			if lineWidth > maxWidth+clipLeft {
				tempLine = ansi.Truncate(line, maxWidth+clipLeft, "")
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

						if pos >= skipCount {
							result.WriteString(string(runes[seqStart:runeIdx]))
						}
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
				if lineWidth > maxWidth {
					clippedLines[lineIdx] += "\x1b[0m"
				}
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
