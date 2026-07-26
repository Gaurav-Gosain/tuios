package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Theme picker layout constants. These are the preferred sizes; a narrower or
// shorter screen gets a panel fitted to it (see overlay_fit.go).
const (
	themePickerInnerWidth  = 52
	themePickerVisibleRows = 10
)

// themePickerHints is the theme picker footer, shared by the renderer and the
// sizing helper so both measure the same panel.
var themePickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: "↑↓", Label: "preview"},
	{Key: "⏎", Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// themePickerLayout returns the fitted inner width and visible row count.
func (m *OS) themePickerLayout() (width, rows int) {
	width = m.panelWidth(themePickerInnerWidth)
	// Body lines that are not theme rows: the search input, its rule, and the
	// count line.
	rows = m.panelBodyRows(themePickerVisibleRows, 3, width, nil, themePickerHints)
	return width, rows
}

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
	width, visible := m.themePickerLayout()
	m.ThemePickerScroll = scrollWindow(m.ThemePickerScroll, m.ThemePickerSelected, len(items), visible)

	var lines []string

	// Search input.
	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.ThemePickerQuery) + cursor
	lines = append(lines, search, overlay.Rule(width, bg, pal))

	start := m.ThemePickerScroll
	end := min(start+visible, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, m.themeRow(items[i], i == m.ThemePickerSelected, pal, width))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  No matching themes"))
		shown++
	}
	for shown < visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	if len(items) > visible {
		info := lipgloss.Sprintf("%d of %d themes", m.ThemePickerSelected+1, len(items))
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	panel := overlay.Panel{
		Glyph: "", // palette
		Title: "Theme",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: themePickerHints,
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
func (m *OS) themeRow(id string, selected bool, pal overlay.Palette, width int) string {
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
	// On a panel too narrow for a name and a swatch strip, the name wins: the
	// swatch is a preview of something the row already names.
	if swatchW+8 > width {
		swatch, swatchW = "", 0
	}

	name := overlay.Truncate(id, max(width-2-swatchW-2, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(name)

	gap := max(width-lipgloss.Width(left)-swatchW, 1)
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
