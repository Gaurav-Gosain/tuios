package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *OS) dialogRows(preferred, chrome int) int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return preferred
	}
	return max(min(preferred, rh-chrome), minPanelRows)
}

func squeezeLines(lines []string, rows int) []string {
	height := func(ls []string) int {
		n := 0
		for _, ln := range ls {
			n += lipgloss.Height(ln)
		}
		return n
	}
	if rows < 1 || height(lines) <= rows {
		return lines
	}

	out := make([]string, 0, len(lines))
	drop := height(lines) - rows
	for i, ln := range lines {
		if drop > 0 && i > 0 && i < len(lines)-1 && strings.TrimSpace(ln) == "" {
			drop--
			continue
		}
		out = append(out, ln)
	}
	for len(out) > 1 && height(out) > rows {
		out = append(out[:len(out)-2], out[len(out)-1])
	}
	return out
}

func clipStyled(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

func clipStyledLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = clipStyled(ln, width)
	}
	return strings.Join(lines, "\n")
}
