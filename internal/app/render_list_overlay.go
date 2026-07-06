package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// listOverlay configures the shared search-and-list overlay used by the command
// palette, session switcher, layout picker and theme picker. It renders a panel
// with an optional search line, a scrolling list of rows, a scroll indicator,
// and returns the per-row hit rects for mouse routing.
type listOverlay struct {
	Glyph      string
	Title      string
	Width      int
	MaxVisible int
	Search     bool
	Query      string
	Count      int
	Selected   int
	Scroll     int
	EmptyMsg   string
	Hints      []overlay.Hint
	// RenderRow returns the content for row i on the given row background. It
	// should be at most Width cells wide; the helper fills the remainder with
	// rowBg so the selection highlight spans the row.
	RenderRow func(i int, selected bool, rowBg color.Color, pal overlay.Palette) string
}

// renderListOverlay renders cfg into a panel and returns the string, geometry
// and hit rows.
func (m *OS) renderListOverlay(cfg listOverlay) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface

	var lines []string
	rowYOffset := 0
	if cfg.Search {
		cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
		search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
			overlay.Style(bg).Foreground(pal.Fg).Render(cfg.Query) + cursor
		lines = append(lines, search, overlay.Rule(cfg.Width, bg, pal))
		rowYOffset = 2
	}

	start := cfg.Scroll
	end := min(start+cfg.MaxVisible, cfg.Count)
	shown := 0
	for i := start; i < end; i++ {
		rowBg := bg
		if i == cfg.Selected {
			rowBg = pal.RowSel
		}
		lines = append(lines, overlay.Fill(cfg.RenderRow(i, i == cfg.Selected, rowBg, pal), cfg.Width, rowBg))
		shown++
	}
	if cfg.Count == 0 {
		msg := cfg.EmptyMsg
		if msg == "" {
			msg = "Nothing here"
		}
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+msg))
		shown++
	}
	for shown < cfg.MaxVisible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}
	if cfg.Count > cfg.MaxVisible {
		info := fmt.Sprintf("%d of %d", cfg.Selected+1, cfg.Count)
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}

	panel := overlay.Panel{
		Glyph: cfg.Glyph,
		Title: cfg.Title,
		Width: cfg.Width,
		Body:  strings.Join(lines, "\n"),
		Hints: cfg.Hints,
	}
	content, geo := panel.Render(pal)

	rows := make([]overlayRowHit, 0, end-start)
	for i := start; i < end; i++ {
		rowY := geo.BodyY + (i - start) + rowYOffset
		rows = append(rows, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, rows
}

// simpleOverlayPanel renders a plain informational panel (no list) for overlay
// sub-states such as confirmations or empty/unavailable messages.
func (m *OS) simpleOverlayPanel(glyph, title string, bodyLines []string, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	styled := make([]string, len(bodyLines))
	for i, l := range bodyLines {
		styled[i] = overlay.Style(bg).Foreground(pal.FgDim).Render("  " + l)
	}
	panel := overlay.Panel{
		Glyph: glyph,
		Title: title,
		Width: 52,
		Body:  strings.Join(styled, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// moveListSelection advances a (selected, scroll) pair by delta within a list
// of count items showing maxVisible rows, keeping the selection in view. Shared
// by the mouse wheel for the list overlays.
func moveListSelection(selected, scroll *int, count, maxVisible, delta int) {
	if count == 0 {
		return
	}
	*selected = clampInt(*selected+delta, 0, count-1)
	if *selected < *scroll {
		*scroll = *selected
	}
	if *selected >= *scroll+maxVisible {
		*scroll = *selected - maxVisible + 1
	}
}

// listRowMarker returns the two-cell leading marker for a list row.
func listRowMarker(selected bool) string {
	if selected {
		return "› "
	}
	return "  "
}

// listRowLine composes a standard "marker + label ... trailing" list row on the
// given background, right-aligning the trailing text. Used by the list-based
// overlays for a consistent look.
func listRowLine(width int, marker, label, trailing string, labelColor, trailingColor color.Color, bold bool, bg color.Color, pal overlay.Palette) string {
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(labelColor).Bold(bold).Render(label)
	trail := ""
	if trailing != "" {
		trail = overlay.Style(bg).Foreground(trailingColor).Render(trailing)
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(trail), 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + trail
}
