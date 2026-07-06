package app

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const layoutPickerWidth = 58

// renderLayoutPicker renders the layout save/load overlay on the shared grammar.
func (m *OS) renderLayoutPicker() (string, overlay.Geometry, []overlayRowHit) {
	if m.LayoutPickerMode == "save" {
		pal := theme.UI()
		bg := pal.Surface
		input := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("Name  ") +
			overlay.Style(bg).Foreground(pal.Fg).Render(m.LayoutSaveBuffer) +
			overlay.Style(bg).Foreground(pal.Accent).Render("█")
		panel := overlay.Panel{
			Glyph: "",
			Title: "Save Layout",
			Width: layoutPickerWidth,
			Body:  input,
			Hints: []overlay.Hint{{Key: "⏎", Label: "save"}, {Key: "esc", Label: "cancel"}},
		}
		content, geo := panel.Render(pal)
		return content, geo, nil
	}

	filtered := FilterLayoutTemplates(m.LayoutPickerItems, m.LayoutPickerQuery)
	if len(filtered) > 0 {
		m.LayoutPickerSelected = clampInt(m.LayoutPickerSelected, 0, len(filtered)-1)
	}

	return m.renderListOverlay(listOverlay{
		Glyph:      "",
		Title:      "Load Layout",
		Width:      layoutPickerWidth,
		MaxVisible: 10,
		Search:     true,
		Query:      m.LayoutPickerQuery,
		Count:      len(filtered),
		Selected:   m.LayoutPickerSelected,
		Scroll:     m.LayoutPickerScroll,
		EmptyMsg:   "No saved layouts",
		Hints: []overlay.Hint{
			{Key: "⏎", Label: "apply"},
			{Key: "d", Label: "delete"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette) string {
			item := filtered[i]
			detail := fmt.Sprintf("%d windows", len(item.Windows))
			if item.AutoTiling {
				detail += " · tiling"
			}
			labelColor := pal.FgDim
			if selected {
				labelColor = pal.Fg
			}
			name := overlay.Truncate(item.Name, layoutPickerWidth-lipgloss.Width(detail)-6)
			return listRowLine(layoutPickerWidth, listRowMarker(selected), name, detail, labelColor, pal.FgMute, selected, rowBg, pal)
		},
	})
}
