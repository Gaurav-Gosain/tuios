package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// ASCII, when true, makes the controls avoid non-ASCII glyphs (arrows,
// ellipsis) for terminals without a capable font. It is a package-level toggle
// because it is a rendering-environment property, not a per-call concern.
var ASCII bool

// Style returns a fresh lipgloss style already backgrounded with bg. Every
// fragment on a panel row must carry that row's background, otherwise a bare
// foreground style emits an ANSI reset that punches a transparent hole through
// the solid fill when the panel is composited over other content.
func Style(bg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Background(bg)
}

// Fill pads s with bg-colored spaces so it spans width cells.
func Fill(s string, width int, bg color.Color) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + Style(bg).Render(strings.Repeat(" ", width-w))
}

// Chip renders a small inset pill (used for titles and tags).
func Chip(label string, bg, fg color.Color) string {
	return lipgloss.NewStyle().
		Background(bg).
		Foreground(fg).
		Bold(true).
		Padding(0, 1).
		Render(label)
}

// KeyBadge renders a single key combo as a subtle card-backed chip. The chip
// carries its own background so it reads on any row.
func KeyBadge(key string, pal Palette) string {
	return lipgloss.NewStyle().
		Background(pal.Card).
		Foreground(pal.AccentBright).
		Bold(true).
		Padding(0, 1).
		Render(key)
}

// KeyBadges joins several key badges with a bg-colored space.
func KeyBadges(keys []string, bg color.Color, pal Palette) string {
	if len(keys) == 0 {
		return ""
	}
	badges := make([]string, 0, len(keys))
	for _, k := range keys {
		badges = append(badges, KeyBadge(k, pal))
	}
	return strings.Join(badges, Style(bg).Render(" "))
}

// Cycler renders an enum value as a ‹ value › control on the given row
// background. The returned left/right arrow widths are two cells each, which
// hosts use to hit-test decrement/increment clicks.
func Cycler(value string, selected bool, bg color.Color, pal Palette) string {
	arrowColor := pal.FgMute
	valColor := pal.FgDim
	if selected {
		arrowColor = pal.AccentBright
		valColor = pal.Fg
	}
	left, right := "‹", "›"
	if ASCII {
		left, right = "<", ">"
	}
	arrow := Style(bg).Foreground(arrowColor)
	return arrow.Render(left+" ") +
		Style(bg).Foreground(valColor).Bold(selected).Render(value) +
		arrow.Render(" "+right)
}

// Toggle renders a boolean as an [ on ] / [ off ] control on the given row
// background.
func Toggle(on, selected bool, bg color.Color, pal Palette) string {
	label := "off"
	fg := pal.FgMute
	if on {
		label = "on"
		fg = pal.Success
	}
	bracketColor := pal.FgMute
	if selected {
		bracketColor = pal.AccentBright
	}
	bracket := Style(bg).Foreground(bracketColor)
	return bracket.Render("[ ") +
		Style(bg).Foreground(fg).Bold(true).Render(label) +
		bracket.Render(" ]")
}

// Rule returns a full-width muted horizontal rule on the given background.
func Rule(width int, bg color.Color, pal Palette) string {
	return Style(bg).Foreground(pal.FgMute).Render(strings.Repeat("─", width))
}

// Ellipsis returns the truncation marker for the current ASCII setting.
func Ellipsis() string {
	if ASCII {
		return "..."
	}
	return "…"
}

// Truncate shortens s to fit within maxWidth display cells, appending an
// ellipsis when it overflows.
func Truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	ell := Ellipsis()
	target := max(maxWidth-lipgloss.Width(ell), 0)
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > target {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + ell
}
