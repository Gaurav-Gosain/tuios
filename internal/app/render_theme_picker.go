package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Theme picker layout constants.
const (
	themePickerInnerWidth  = 52
	themePickerVisibleRows = 10
)

// renderThemePicker draws the searchable theme picker with a live color-swatch
// preview per theme, returning the panel, geometry, and per-row hit rects.
func (m *OS) renderThemePicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.themePickerItems()
	pal := theme.UI()
	bg := pal.Surface

	// Clamp selection/scroll to the filtered list.
	if len(items) > 0 {
		m.ThemePickerSelected = clampInt(m.ThemePickerSelected, 0, len(items)-1)
	} else {
		m.ThemePickerSelected = 0
	}
	maxScroll := max(len(items)-themePickerVisibleRows, 0)
	m.ThemePickerScroll = clampInt(m.ThemePickerScroll, 0, maxScroll)

	var lines []string

	// Search input.
	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.ThemePickerQuery) + cursor
	lines = append(lines, search, overlay.Rule(themePickerInnerWidth, bg, pal))

	start := m.ThemePickerScroll
	end := min(start+themePickerVisibleRows, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, m.themeRow(items[i], i == m.ThemePickerSelected, pal))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  No matching themes"))
		shown++
	}
	for shown < themePickerVisibleRows {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	if len(items) > themePickerVisibleRows {
		info := lipgloss.Sprintf("%d of %d themes", m.ThemePickerSelected+1, len(items))
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	panel := overlay.Panel{
		Glyph: "", // palette
		Title: "Theme",
		Width: themePickerInnerWidth,
		Body:  strings.Join(lines, "\n"),
		Hints: []overlay.Hint{
			{Key: "type", Label: "filter"},
			{Key: "↑↓", Label: "preview"},
			{Key: "⏎", Label: "apply"},
			{Key: "esc", Label: "cancel"},
		},
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + 2 // +2 for search line and rule
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}

// themeRow renders one theme entry: a name on the left and a color-swatch
// preview on the right, with a highlight bar when selected.
func (m *OS) themeRow(id string, selected bool, pal overlay.Palette) string {
	bg := pal.Surface
	nameColor := pal.FgDim
	marker := "  "
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
		marker = "› "
	}

	swatch := themeSwatchStrip(id, bg)
	swatchW := lipgloss.Width(swatch)

	name := id
	nameMax := themePickerInnerWidth - 2 - swatchW - 2
	if nameMax > 1 {
		name = overlay.Truncate(name, nameMax)
	}
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(name)

	gap := max(themePickerInnerWidth-lipgloss.Width(left)-swatchW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + swatch
}

// themeSwatchStrip renders a theme's preview colors as adjacent two-cell blocks.
func themeSwatchStrip(id string, bg color.Color) string {
	colors := theme.ThemeSwatch(id)
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Background(c).Render("  "))
	}
	// A trailing surface cell separates the strip from the panel edge cleanly.
	return b.String() + overlay.Style(bg).Render(" ")
}
