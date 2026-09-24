package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The theme picker and the glyph-set picker are the same panel with a
// different preview: a search line, a scrolling list of ids that previews the
// selected one live, and a few lines under the list. The movement and the
// frame are shared here. What each picker applies, persists and previews stays
// with the picker.

// livePickerMove moves *selected by delta within n items under the list rule,
// keeping it inside the scroll window of visible rows that starts at *scroll.
// It reports false when there are no items, in which case nothing is moved.
func livePickerMove(selected, scroll *int, delta, n, visible int, wrap bool) bool {
	if n == 0 {
		return false
	}
	*selected = listnav.Step(*selected, delta, n, wrap)
	*scroll = listnav.Scroll(*scroll, *selected, n, visible)
	return true
}

// livePickerPanel describes one picker for renderLivePicker.
type livePickerPanel struct {
	Glyph string
	Title string
	Query string
	Items []string
	// Selected and Scroll are the picker's own state. The renderer clamps them
	// to the filtered list, the same as it did when each picker drew itself.
	Selected *int
	Scroll   *int
	Width    int
	Visible  int
	Hints    []overlay.Hint
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
func renderLivePicker(p livePickerPanel) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	items := p.Items

	// Clamp selection/scroll to the filtered list.
	if len(items) > 0 {
		*p.Selected = clampInt(*p.Selected, 0, len(items)-1)
	} else {
		*p.Selected = 0
	}
	*p.Scroll = scrollWindow(*p.Scroll, *p.Selected, len(items), p.Visible)

	var lines []string

	// Search input.
	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(p.Query) + cursor
	lines = append(lines, search, overlay.Rule(p.Width, bg, pal))

	start := *p.Scroll
	end := min(start+p.Visible, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, p.Row(items[i], i == *p.Selected, pal, p.Width))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+p.Empty))
		shown++
	}
	for shown < p.Visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	lines = append(lines, p.Footer(pal)...)

	panel := overlay.Panel{
		Glyph: p.Glyph,
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
