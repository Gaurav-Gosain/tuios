package app

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	glyphPickerInnerWidth  = 52
	glyphPickerVisibleRows = 10
)

var glyphPickerHints = []overlay.Hint{
	{Key: "type", Label: "filter"},
	{Key: hintListMove, Label: "preview"},
	{Key: overlay.EnterGlyph, Label: "apply"},
	{Key: "esc", Label: "cancel"},
}

func (m *OS) glyphPickerLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(glyphPickerInnerWidth)
	rows, hints = m.panelBody(glyphPickerVisibleRows, 4, width, nil, glyphPickerHints)
	return width, rows, hints
}

func (m *OS) renderGlyphPicker() (string, overlay.Geometry, []overlayRowHit) {
	items := m.glyphPickerItems()
	width, visible, hints := m.glyphPickerLayout()
	return m.renderLivePicker(livePickerPanel{
		Title:   "Glyph set",
		Items:   items,
		List:    &m.glyphList,
		Width:   width,
		Visible: visible,
		Hints:   hints,
		Empty:   "No matching glyph sets",
		Row:     m.glyphRow,
		Footer: func(pal overlay.Palette) []string {
			return m.glyphPickerDetail(items, pal, width)
		},
	})
}

func (m *OS) glyphRow(id string, selected bool, pal overlay.Palette, width int) string {
	st := overlay.RowState{Cursor: selected, Focused: true}
	bg := pal.Ground(st, pal.Surface)
	nameColor := pal.FgDim
	marker := "  "
	if selected {
		nameColor = pal.Fg
		marker = "› "
	}

	sample := m.GlyphPickerSamples[id]
	strip := sample.Frame
	stripW := lipgloss.Width(strip)
	if stripW+8 > width {
		strip, stripW = "", 0
	}

	name := overlay.Truncate(id, max(width-2-stripW-2, 1))
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).Render(name)

	shape := overlay.Style(bg).Foreground(pal.Fg).Render(strip)
	gap := max(width-lipgloss.Width(left)-stripW, 1)
	return pal.Mark(left+overlay.Style(bg).Render(strings.Repeat(" ", gap)), st) + shape
}

func (m *OS) glyphPickerDetail(items []string, pal overlay.Palette, width int) []string {
	bg := pal.Surface
	blank := overlay.Style(bg).Render(" ")
	if m.glyphList.Cursor < 0 || m.glyphList.Cursor >= len(items) {
		return []string{blank, blank}
	}
	id := items[m.glyphList.Cursor]
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
	dropped := "Wrong width. tuios draws the default: " + strings.Join(sample.Dropped, ", ")
	return append(lines, overlay.Style(bg).Foreground(pal.Warning).
		Render("  "+overlay.Truncate(dropped, max(width-2, 1))))
}
