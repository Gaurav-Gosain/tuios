package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The theme picker and the glyph-set picker are the same panel with a
// different preview: a search line, a scrolling list of ids that previews the
// selected one live, and a few lines under the list. The movement and the
// frame are shared here. What each picker applies, persists and previews stays
// with the picker.

// livePickerPanel describes one picker for renderLivePicker.
type livePickerPanel struct {
	Title string
	Items []string
	// List is the picker's filter, cursor and window. The renderer clamps the
	// cursor and the window to the filtered list.
	List    *listnav.State
	Width   int
	Visible int
	Hints   []overlay.Hint
	// Empty is the line shown when the query matches nothing.
	Empty string
	// Row draws one item.
	Row func(id string, selected bool, pal overlay.Palette, width int) string
	// Footer returns the lines under the list. It runs after the selection is
	// clamped, so it can read the selected item.
	Footer func(pal overlay.Palette) []string
}

// renderLivePicker draws a live picker panel, returning the panel, geometry,
// and per-row hit rects.
func (m *OS) renderLivePicker(p livePickerPanel) (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Surface
	items := p.Items

	p.List.Fit(len(items), p.Visible)

	var lines []string
	lines = append(lines, searchLine(p.List, p.Width, pal), overlay.Rule(p.Width, bg, pal))

	start := p.List.Scroll
	end := min(start+p.Visible, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, p.Row(items[i], i == p.List.Cursor, pal, p.Width))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Empty{Message: p.Empty, Hint: overlay.Hint{Key: "esc", Label: "close"}}.Lines(p.Width, p.Visible, bg, pal)...)
		shown = p.Visible
	}
	for shown < p.Visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	lines = append(lines, p.Footer(pal)...)

	panel := overlay.Panel{
		Title: p.Title,
		Width: p.Width,
		Body:  strings.Join(lines, "\n"),
		Hints: p.Hints,
	}
	content, geo := panel.Render(pal)

	var rows []overlayRowHit
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + 2 // +2 for the search line and its rule
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}
