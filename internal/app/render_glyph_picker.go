package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Glyph picker layout constants, matching the theme picker's so the two panels
// are the same object with a different preview.
const (
	glyphPickerInnerWidth  = 52
	glyphPickerVisibleRows = 10
)

// glyphPickerHints is the footer, shared by the renderer and the sizing helper
// so both measure the same panel.
var glyphPickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: "↑↓", Label: "preview"},
	{Key: "⏎", Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

// glyphPickerLayout returns the fitted inner width and visible row count.
func (m *OS) glyphPickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(glyphPickerInnerWidth)
	// Body lines that are not set rows: the search input, its rule, and the
	// two lines describing the selected set.
	rows, hints = m.panelBody(glyphPickerVisibleRows, 4, width, nil, glyphPickerHints)
	return width, rows, hints
}

// renderGlyphPicker draws the searchable glyph-set picker with a live shape
// preview per set, returning the panel, geometry, and per-row hit rects.
func (m *OS) renderGlyphPicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.glyphPickerItems()
	pal := theme.UI()
	bg := pal.Surface

	if len(items) > 0 {
		m.GlyphPickerSelected = clampInt(m.GlyphPickerSelected, 0, len(items)-1)
	} else {
		m.GlyphPickerSelected = 0
	}
	width, visible, hints := m.glyphPickerLayout()
	m.GlyphPickerScroll = scrollWindow(m.GlyphPickerScroll, m.GlyphPickerSelected, len(items), visible)

	var lines []string

	cursor := overlay.Style(bg).Foreground(pal.Accent).Render("█")
	search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(bg).Foreground(pal.Fg).Render(m.GlyphPickerQuery) + cursor
	lines = append(lines, search, overlay.Rule(width, bg, pal))

	start := m.GlyphPickerScroll
	end := min(start+visible, len(items))
	shown := 0
	for i := start; i < end; i++ {
		lines = append(lines, m.glyphRow(items[i], i == m.GlyphPickerSelected, pal, width))
		shown++
	}
	if len(items) == 0 {
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
			Render("  No matching glyph sets"))
		shown++
	}
	for shown < visible {
		lines = append(lines, overlay.Style(bg).Render(" "))
		shown++
	}

	lines = append(lines, m.glyphPickerDetail(items, pal, width)...)

	panel := overlay.Panel{
		Glyph: "󰊄", // glyph set
		Title: "Glyph set",
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
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

// glyphRow renders one set: its name on the left and the shape strip on the
// right, with a highlight bar when selected.
func (m *OS) glyphRow(id string, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface
	nameColor := pal.FgDim
	marker := "  "
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
		marker = "› "
	}

	sample := m.GlyphPickerSamples[id]
	strip := sample.Frame
	stripW := lipgloss.Width(strip)
	// On a panel too narrow for a name and a strip, the name wins: the strip is
	// a preview of something the row already names.
	if stripW+8 > width {
		strip, stripW = "", 0
	}

	name := overlay.Truncate(id, max(width-2-stripW-2, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(name)

	// The strip is drawn in the chrome's own ink rather than the row's, because
	// it is a preview of chrome.
	shape := overlay.Style(bg).Foreground(pal.Fg).Render(strip)
	gap := max(width-lipgloss.Width(left)-stripW, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + shape
}

// glyphPickerDetail is the two lines under the list: what the selected set says
// and what of it draws.
//
// The distinction is the one list-glyphs reports in two columns. A set states
// only the roles it changes, and a role whose glyph is the wrong width for its
// slot is dropped back to the default silently, because a window control the
// pointer no longer lands on is the worse failure. Silently on screen too was
// the gap: the set looked selected and drew someone else's shapes.
func (m *OS) glyphPickerDetail(items []string, pal overlay.Palette, width int) []string {
	bg := pal.Surface
	blank := overlay.Style(bg).Render(" ")
	if m.GlyphPickerSelected < 0 || m.GlyphPickerSelected >= len(items) {
		return []string{blank, blank}
	}
	id := items[m.GlyphPickerSelected]
	sample := m.GlyphPickerSamples[id]

	var summary string
	switch {
	case sample.Named == 0:
		summary = "Draws the shapes tuios ships"
	case len(sample.Dropped) == 0:
		summary = "Names " + strconv.Itoa(sample.Named) + " roles, all drawn"
	default:
		summary = "Names " + strconv.Itoa(sample.Named) + " roles, " +
			strconv.Itoa(len(sample.Dropped)) + " not drawn"
	}
	if sample.ASCII {
		summary += " · ASCII"
	}
	if set := theme.ResolveGlyphSet(id); set.Inherits != "" {
		summary += " · from " + set.Inherits
	}

	lines := []string{
		overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
			Render("  " + overlay.Truncate(summary, max(width-2, 1))),
	}

	if len(sample.Dropped) == 0 {
		return append(lines, blank)
	}
	// Named individually: "two roles were dropped" sends the author back to the
	// file to work out which, and the width rule is the reason every time.
	dropped := "wrong width, so default: " + strings.Join(sample.Dropped, ", ")
	return append(lines, overlay.Style(bg).Foreground(pal.Warning).
		Render("  "+overlay.Truncate(dropped, max(width-2, 1))))
}
