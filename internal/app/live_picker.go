package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type livePickerPanel struct {
	Title   string
	Items   []string
	List    *listnav.State
	Width   int
	Visible int
	Hints   []overlay.Hint
	Empty   string
	Row     func(id string, selected bool, pal overlay.Palette, width int) string
	Footer  func(pal overlay.Palette) []string
}

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
		rowY := geo.BodyY + (i - start) + 2
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}
