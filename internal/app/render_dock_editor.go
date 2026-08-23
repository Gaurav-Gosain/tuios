package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Dock editor layout constants.
const (
	dockEditorInnerWidth  = 46
	dockEditorVisibleRows = 14
)

// dockEditorHints is the footer, shared by the renderer and the sizing helper
// so both measure the same panel.
var dockEditorHints = []overlay.Hint{
	{Key: "↑↓", Label: "select"},
	{Key: "⇧↑↓", Label: "move"},
	{Key: "⏎", Label: "add/remove"},
	{Key: "r", Label: "reset"},
	{Key: "u", Label: "undo"},
	{Key: "esc", Label: "close"},
}

// dockEditorLayout returns the fitted inner width and visible row count.
func (m *OS) dockEditorLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(dockEditorInnerWidth)
	// One body line that is not a row: the line describing the selection.
	rows, hints = m.panelBody(dockEditorVisibleRows, 1, width, nil, dockEditorHints)
	return width, rows, hints
}

// dockSideLabels are the region headings, in the words the config file uses.
var dockSideLabels = map[string]string{
	"left":            "Left",
	"center":          "Center",
	"right":           "Right",
	dockAvailableSide: "Not on the bar",
}

// renderDockEditor draws the dock layout editor: the three regions and their
// components in draw order, with what is not placed underneath.
func (m *OS) renderDockEditor() (string, overlay.Geometry, []overlayRowHit) {
	rows := m.dockEditorRows()
	pal := theme.UI()
	bg := pal.Surface

	if len(rows) > 0 {
		m.DockEditorSelected = clampInt(m.DockEditorSelected, 0, len(rows)-1)
	} else {
		m.DockEditorSelected = 0
	}
	width, visible, hints := m.dockEditorLayout()
	m.DockEditorScroll = scrollWindow(m.DockEditorScroll, m.DockEditorSelected, len(rows), visible)

	var lines []string
	start := m.DockEditorScroll
	end := min(start+visible, len(rows))
	for i := start; i < end; i++ {
		lines = append(lines, m.dockEditorLine(rows[i], i == m.DockEditorSelected, pal, width))
	}
	for shown := end - start; shown < visible; shown++ {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}
	lines = append(lines, m.dockEditorDetail(rows, pal, width))

	// A read-only session says so here as well as on the settings panel: the
	// editor can be opened on its own and the lists it writes go nowhere.
	title := "Dock layout"
	if m.ConfigReadOnly {
		title = "Dock layout (this session only)"
	}

	panel := overlay.Panel{
		Glyph: "", // dock
		Title: title,
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	var hits []overlayRowHit
	for i := start; i < end; i++ {
		if rows[i].Kind == dockRowHeader {
			continue
		}
		rowY := geo.BodyY + (i - start)
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: rowY, X1: geo.Width, Y1: rowY + 1},
			Idx:  i,
		})
	}
	return content, geo, hits
}

// dockEditorLine renders one line of the editor.
func (m *OS) dockEditorLine(row dockEditorRow, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface

	if row.Kind == dockRowHeader {
		label := dockSideLabels[row.Side]
		head := overlay.Style(bg).Foreground(pal.FgMute).Bold(true).Render("  " + label)
		// A rule out to the panel edge, so the regions read as bands rather
		// than as one list with words in it.
		rule := max(width-lipgloss.Width(head)-1, 0)
		return head + overlay.Style(bg).Foreground(pal.FgMute).
			Render(" "+strings.Repeat(config.GetWindowSeparatorChar(), rule))
	}

	nameColor := pal.FgDim
	marker := "  "
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
		marker = "› "
	}

	if row.Kind == dockRowEmpty {
		return overlay.Style(bg).Render(marker) +
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("nothing here")
	}

	// An available component is drawn quieter than a placed one, because the
	// list it is in is "what you could add" rather than "what is on the bar".
	if row.Kind == dockRowAvailable && !selected {
		nameColor = pal.FgMute
	}

	name := row.Name
	var tag string
	switch {
	case row.Custom:
		tag = "yours"
	case row.Fixed != "":
		tag = "pinned " + row.Fixed
	}

	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).
			Render(overlay.Truncate(name, max(width-2-lipgloss.Width(tag)-3, 1)))
	if tag == "" {
		gap := max(width-lipgloss.Width(left), 0)
		return left + overlay.Style(bg).Render(strings.Repeat(" ", gap))
	}
	marked := overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render(tag)
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(marked)-1, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + marked +
		overlay.Style(bg).Render(" ")
}

// dockEditorDetail is the line under the list, saying what the selected row is
// and what can be done to it.
func (m *OS) dockEditorDetail(rows []dockEditorRow, pal overlay.Palette, width int) string {
	bg := pal.Surface
	if m.DockEditorSelected < 0 || m.DockEditorSelected >= len(rows) {
		return overlay.Style(bg).Render(" ")
	}
	row := rows[m.DockEditorSelected]

	var text string
	switch {
	case row.Kind == dockRowEmpty && row.Side == dockAvailableSide:
		text = "Every part is on the bar"
	case row.Kind == dockRowEmpty:
		text = "This side draws nothing"
	case row.Kind == dockRowAvailable:
		text = "Not on the bar. Press enter to add it."
	case row.Custom:
		text = "Your own cell. Press enter to remove it."
	case row.Fixed != "":
		text = "This always draws on the " + row.Fixed + "."
	default:
		text = "Press shift and an arrow to move it. Past the end it changes side."
	}
	return overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
		Render("  " + overlay.Truncate(text, max(width-2, 1)))
}
