package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The bordered dialogs (logs, cache stats, tape manager, tape review) predate
// the panel grammar and size themselves from their own content. The helpers
// here fit them to the screen the same way overlay_fit.go fits the panels.

// dialogChrome is what a bordered dialog spends on its own frame: a border cell
// and two padding cells on each side.
const dialogChrome = 6

// dialogWidth returns the total width to draw a centered bordered dialog at,
// given the width it would prefer. Never wider than the screen.
func (m *OS) dialogWidth(preferred int) int {
	rw := m.GetRenderWidth()
	if rw <= 0 {
		return preferred // size not known yet; the caller's preference stands
	}
	return max(min(preferred, rw), dialogChrome+2)
}

// dialogRows returns how many scrolling content rows a centered dialog can show
// given the rows it spends on everything else.
func (m *OS) dialogRows(preferred, chrome int) int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return preferred
	}
	return max(min(preferred, rh-chrome), minPanelRows)
}

// dialogTextWidth is the widest a line of a content-sized dialog may be before
// the box around it would reach past the edge of the screen.
func (m *OS) dialogTextWidth() int {
	rw := m.GetRenderWidth()
	if rw <= 0 {
		return 1 << 20 // size not known yet; do not clip anything
	}
	return max(rw-dialogChrome, 8)
}

// dialogContentRows is the tallest a content-sized dialog's body may be before
// the box around it would reach past the top or bottom of the screen.
func (m *OS) dialogContentRows() int {
	rh := m.GetRenderHeight()
	if rh <= 0 {
		return 1 << 20 // size not known yet; do not drop anything
	}
	return max(rh-4, 1) // border and padding, top and bottom
}

// squeezeLines shortens a dialog body to rows lines. The blank spacer lines go
// first, since a dialog that reads a little tighter is better than one whose
// last rows are off the bottom of the screen; only if that is not enough does
// it drop content, keeping the last line (which is where the key hints live).
func squeezeLines(lines []string, rows int) []string {
	// An entry can itself be several lines tall (a bordered input field, say),
	// so the budget is measured in rendered lines, not in entries.
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
	// Still too tall: keep the top and the last line, which is where a dialog
	// puts its key hints.
	for len(out) > 1 && height(out) > rows {
		out = append(out[:len(out)-2], out[len(out)-1])
	}
	return out
}

// clipStyled cuts an already-styled line to at most width cells, keeping its
// escape sequences intact. Used where the text being fitted has colors baked in
// and cannot be re-measured as plain runes.
func clipStyled(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

// clipStyledLines applies clipStyled to every line of a multi-line block.
func clipStyledLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = clipStyled(ln, width)
	}
	return strings.Join(lines, "\n")
}
