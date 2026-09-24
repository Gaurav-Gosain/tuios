package app

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const hostPickerWidth = 48

// renderHostPicker draws the machine picker on the shared list grammar.
func (m *OS) renderHostPicker() (string, overlay.Geometry, []overlayRowHit) {
	filtered := FilterHostPickerItems(m.HostPickerItems, m.HostPickerQuery)
	if len(filtered) > 0 {
		m.HostPickerSelected = clampInt(m.HostPickerSelected, 0, len(filtered)-1)
	}

	return m.renderListOverlay(listOverlay{
		Glyph:      "",
		Title:      hostPickerTitle(m.HostPickerPurpose),
		Width:      hostPickerWidth,
		MaxVisible: 10,
		Search:     true,
		Query:      m.HostPickerQuery,
		Count:      len(filtered),
		Selected:   m.HostPickerSelected,
		Scroll:     &m.HostPickerScroll,
		EmptyMsg:   "No machine matches",
		Hints: []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "open"},
			{Key: "esc", Label: "cancel"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			item := filtered[i]
			// A machine that cannot be reached is listed and dimmed rather than
			// hidden: hiding it leaves someone wondering whether they imagined
			// configuring it, which is the same argument the rail makes for
			// keeping a host's heading when its link is down.
			nameFg := pal.FgDim
			if selected {
				nameFg = pal.Fg
			}
			if !item.Up {
				nameFg = pal.FgMute
			}
			label := overlay.Truncate(item.Label, max(width-lipgloss.Width(item.Detail)-6, 1))
			return listRowLine(width, listRowMarker(selected), label, item.Detail,
				nameFg, pal.FgMute, selected && item.Up, rowBg, pal)
		},
	})
}

// hostPickerTitle says which question is being asked, since the list is the
// same for both.
func hostPickerTitle(p HostPickerPurpose) string {
	if p == HostPickerNewSession {
		return "New session on"
	}
	return "New window on"
}
