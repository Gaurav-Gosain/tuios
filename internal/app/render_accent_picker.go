package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// accentPickerInnerWidth is the preferred inner width: a swatch, a color name,
// and its digit is all a row carries.
const accentPickerInnerWidth = 30

var accentPickerHints = []overlay.Hint{
	{Key: "↑↓", Label: "pick"},
	{Key: "1-8", Label: "direct"},
	{Key: "⏎", Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// renderAccentPicker draws the swatch picker for the window being accented. It
// is the shared list overlay with a colored block in front of each row, so it
// scrolls, hit-tests and fits a small screen exactly like every other list.
func (m *OS) renderAccentPicker() (string, overlay.Geometry, []overlayRowHit) {
	rows := accentSwatchCount + 1 // the swatches, then the row that clears
	m.AccentPickerSelected = clampInt(m.AccentPickerSelected, 0, rows-1)

	return m.renderListOverlay(listOverlay{
		Glyph:      glyphPalette,
		Title:      "Accent",
		Width:      accentPickerInnerWidth,
		MaxVisible: rows,
		Count:      rows,
		Selected:   m.AccentPickerSelected,
		Scroll:     &m.AccentPickerScroll,
		Hints:      accentPickerHints,
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			fg := pal.FgDim
			if selected {
				fg = pal.Fg
			}
			marker := overlay.Style(rowBg).Foreground(pal.Accent).Bold(true).Render(listRowMarker(selected))

			if i == accentSwatchCount {
				return marker + overlay.Style(rowBg).Foreground(fg).Italic(true).Render("None")
			}
			left := marker +
				lipgloss.NewStyle().Background(accentColor(i)).Render("  ") +
				overlay.Style(rowBg).Foreground(fg).Bold(selected).Render(" "+accentNames[i])
			trail := overlay.Style(rowBg).Foreground(pal.FgMute).Render(strconv.Itoa(i + 1))
			gap := max(width-lipgloss.Width(left)-lipgloss.Width(trail), 1)
			return left + overlay.Style(rowBg).Render(strings.Repeat(" ", gap)) + trail
		},
	})
}
