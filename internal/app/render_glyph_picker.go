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
	{Key: overlay.EnterGlyph, Label: "apply"},
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
	width, visible, hints := m.glyphPickerLayout()
	return renderLivePicker(livePickerPanel{
		Title:    "Glyph set",
		Query:    m.GlyphPickerQuery,
		Items:    items,
		Selected: &m.GlyphPickerSelected,
		Scroll:   &m.GlyphPickerScroll,
		Width:    width,
		Visible:  visible,
		Hints:    hints,
		Empty:    "No matching glyph sets",
		Row:      m.glyphRow,
		Footer: func(pal overlay.Palette) []string {
			return m.glyphPickerDetail(items, pal, width)
		},
	})
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
		summary = "Uses the default shapes"
	case len(sample.Dropped) == 0:
		summary = "Sets " + strconv.Itoa(sample.Named) + " roles. tuios draws them all."
	default:
		summary = "Sets " + strconv.Itoa(sample.Named) + " roles. tuios does not draw " +
			strconv.Itoa(len(sample.Dropped)) + " of them."
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
	dropped := "Wrong width. tuios draws the default: " + strings.Join(sample.Dropped, ", ")
	return append(lines, overlay.Style(bg).Foreground(pal.Warning).
		Render("  "+overlay.Truncate(dropped, max(width-2, 1))))
}
