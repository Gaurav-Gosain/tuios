package app

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const layoutPickerWidth = 58

// renderLayoutPicker renders the layout save/load overlay on the shared grammar.
func (m *OS) renderLayoutPicker() (string, overlay.Geometry, []overlayRowHit) {
	if m.LayoutPickerMode == "save" {
		pal := m.overlayUI()
		bg := pal.Surface
		width := m.panelWidth(layoutPickerWidth)
		before, at, after := m.layoutSaveName.Parts()
		input := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("Name  ") +
			overlay.FieldLine(before, at, after, max(width-6, 1), bg, pal.Fg)
		panel := overlay.Panel{
			Title: "Save layout",
			Width: width,
			Body:  input,
			Hints: []overlay.Hint{{Key: overlay.EnterGlyph, Label: "save"}, {Key: "esc", Label: "cancel"}},
		}
		content, geo := panel.Render(pal)
		return content, geo, nil
	}

	filtered := m.layoutPickerRows()
	empty := "No saved layouts"
	if m.LayoutPickerLoading {
		empty = "Reading the layouts..."
	}
	m.layoutList.Fit(len(filtered), listOverlayRows)

	return m.renderListOverlay(listOverlay{
		Title:      "Load layout",
		Width:      layoutPickerWidth,
		MaxVisible: 10,
		Search:     true,
		SearchLine: func(width int) string { return layoutPicker().searchLine(m, width) },
		Count:      len(filtered),
		Selected:   m.layoutList.Cursor,
		Scroll:     &m.layoutList.Scroll,
		EmptyMsg:   empty,
		Hints: []overlay.Hint{
			{Key: overlay.EnterGlyph, Label: "apply"},
			{Key: "ctrl+d", Label: "delete"},
			{Key: "esc", Label: "close"},
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			item := filtered[i]
			detail := fmt.Sprintf("%d windows", len(item.Windows))
			if item.AutoTiling {
				detail += " · tiling"
			}
			// On a narrow panel the detail is dropped before the name is.
			if width-lipgloss.Width(detail)-6 < 12 {
				detail = fmt.Sprintf("%d", len(item.Windows))
			}
			labelColor := pal.FgDim
			if selected {
				labelColor = pal.Fg
			}
			name := overlay.Truncate(item.Name, max(width-lipgloss.Width(detail)-6, 1))
			return listRowLine(width, listRowMarker(selected), name, detail, labelColor, pal.FgMute, selected, rowBg, pal)
		},
	})
}
