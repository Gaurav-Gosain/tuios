package overlay

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Hint is one key/label pair shown in a panel footer.
type Hint struct {
	Key   string
	Label string
}

// Panel is a borderless floating panel: a solid surface fill with an inset
// accent title chip, an optional tab row, a body, and a muted footer of key
// hints. Width is the inner content width; the rendered block is Width+4 cells
// wide (a two-cell pad on each side).
type Panel struct {
	Glyph     string // optional leading glyph for the title chip
	Title     string
	Width     int
	Tabs      []string
	ActiveTab int
	Body      string // pre-styled, multi-line; each line is surface-filled
	Hints     []Hint
}

// sidePad is the number of surface cells padding each side of the content.
const sidePad = 2

// glyphPrefix returns the glyph plus a trailing space, honoring ASCII mode.
func glyphPrefix(glyph string) string {
	if ASCII || glyph == "" {
		return ""
	}
	return glyph + " "
}

// Tabs renders the section tab row: the active tab is an accent pill, the rest
// muted. It also returns the panel-relative rect of each tab, given the tab
// row's top-left origin (originX, originY).
func tabsRow(tabs []string, active int, bg color.Color, pal Palette, originX, originY int) (string, []Rect) {
	rendered := make([]string, 0, len(tabs))
	rects := make([]Rect, 0, len(tabs))
	x := originX
	for i, name := range tabs {
		var pill string
		if i == active {
			pill = lipgloss.NewStyle().
				Background(pal.Accent).Foreground(pal.PillFg).
				Bold(true).Padding(0, 1).Render(name)
		} else {
			pill = Style(bg).Foreground(pal.FgDim).Padding(0, 1).Render(name)
		}
		w := lipgloss.Width(pill)
		rects = append(rects, Rect{X0: x, Y0: originY, X1: x + w, Y1: originY + 1})
		x += w
		rendered = append(rendered, pill)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...), rects
}

// footerRow renders the muted key-hint row.
func footerRow(hints []Hint, bg color.Color, pal Palette) string {
	keyStyle := Style(bg).Foreground(pal.AccentBright).Bold(true)
	labelStyle := Style(bg).Foreground(pal.FgMute)
	sep := Style(bg).Render("   ")
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, keyStyle.Render(h.Key)+labelStyle.Render(" "+h.Label))
	}
	return strings.Join(parts, sep)
}

// Render assembles the panel and returns the rendered string plus the geometry
// of its interactive regions in panel-relative coordinates.
func (p Panel) Render(pal Palette) (string, Geometry) {
	bg := pal.Surface
	totalW := p.Width + 2*sidePad
	pad := Style(bg).Render(strings.Repeat(" ", sidePad))
	blank := Style(bg).Render(strings.Repeat(" ", totalW))

	line := func(content string) string {
		return Fill(pad+content, totalW, bg)
	}

	var lines []string
	geo := Geometry{Width: totalW, InnerWidth: p.Width, BodyX: sidePad}

	lines = append(lines, blank) // 0: top pad

	// 1: title chip. The whole row is a drag handle.
	chip := Chip(glyphPrefix(p.Glyph)+p.Title, pal.Accent, pal.PillFg)
	lines = append(lines, line(chip))
	geo.TitleBar = Rect{X0: 0, Y0: len(lines) - 1, X1: totalW, Y1: len(lines)}
	lines = append(lines, blank) // 2: blank

	if len(p.Tabs) > 0 {
		tabsStr, rects := tabsRow(p.Tabs, p.ActiveTab, bg, pal, sidePad, len(lines))
		lines = append(lines, line(tabsStr))
		geo.Tabs = rects
		lines = append(lines, line(Rule(p.Width, bg, pal)))
		lines = append(lines, blank)
	}

	geo.BodyY = len(lines)
	for bodyLine := range strings.SplitSeq(p.Body, "\n") {
		lines = append(lines, line(bodyLine))
	}

	if len(p.Hints) > 0 {
		lines = append(lines, blank)
		lines = append(lines, line(Rule(p.Width, bg, pal)))
		lines = append(lines, line(footerRow(p.Hints, bg, pal)))
	}
	lines = append(lines, blank) // bottom pad

	geo.Height = len(lines)
	return strings.Join(lines, "\n"), geo
}
