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
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// themePickerLayout returns the fitted inner width and visible row count.
func (m *OS) themePickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(themePickerInnerWidth)
	// Body lines that are not theme rows: the search input, its rule, and the
	// count line.
	rows, hints = m.panelBody(themePickerVisibleRows, 3, width, nil, themePickerHints)
	return width, rows, hints
}

// renderThemePicker draws the searchable theme picker with a live color-swatch
// preview per theme, returning the panel, geometry, and per-row hit rects.
func (m *OS) renderThemePicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.themePickerItems()
	width, visible, hints := m.themePickerLayout()
	return renderLivePicker(livePickerPanel{
		Title:    "Theme",
		Query:    m.ThemePickerQuery,
		Items:    items,
		Selected: &m.ThemePickerSelected,
		Scroll:   &m.ThemePickerScroll,
		Width:    width,
		Visible:  visible,
		Hints:    hints,
		Empty:    "No matching themes",
		Row:      m.themeRow,
		Footer: func(pal overlay.Palette) []string {
			bg := pal.Surface
			if len(items) > visible {
				info := lipgloss.Sprintf("%d of %d themes", m.ThemePickerSelected+1, len(items))
				return []string{overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  " + info)}
			}
			return []string{overlay.Style(bg).Render(" ")}
		},
	})
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
