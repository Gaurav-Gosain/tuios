package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	dockEditorInnerWidth  = 46
	dockEditorVisibleRows = 14
)

var dockEditorHints = []overlay.Hint{
	{Key: hintListMoveLetters, Label: "select"},
	{Key: "⇧↑↓", Label: "move"},
	{Key: overlay.EnterGlyph, Label: "add/remove"},
	{Key: "r", Label: "reset"},
	{Key: "u", Label: "undo"},
	{Key: "esc", Label: "close"},
}

func (m *OS) dockEditorLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(dockEditorInnerWidth)
	rows, hints = m.panelBody(dockEditorVisibleRows, 1, width, nil, dockEditorHints)
	return width, rows, hints
}

var dockSideLabels = map[string]string{
	"left":            "Left",
	"center":          "Center",
	"right":           "Right",
	dockAvailableSide: "Not on the bar",
}

func (m *OS) renderDockEditor() (string, overlay.Geometry, []overlayRowHit) {
	rows := m.dockEditorRows()
	pal := m.overlayUI()
	bg := pal.Surface

	width, visible, hints := m.dockEditorLayout()
	m.dockList.Fit(len(rows), visible)

	var lines []string
	start := m.dockList.Scroll
	end := min(start+visible, len(rows))
	for i := start; i < end; i++ {
		lines = append(lines, pal.Row(m.dockEditorLine(rows[i], i == m.dockList.Cursor, pal, width), width, overlay.RowState{Cursor: i == m.dockList.Cursor, Focused: true}, pal.Surface))
	}
	for shown := end - start; shown < visible; shown++ {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}
	lines = append(lines, m.dockEditorDetail(rows, pal, width))

	title := "Dock layout"
	if m.ConfigReadOnly {
		title = "Dock layout (this session only)"
	}

	panel := overlay.Panel{
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

func (m *OS) dockEditorLine(row dockEditorRow, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface

	if row.Kind == dockRowHeader {
		label := dockSideLabels[row.Side]
		head := overlay.Style(bg).Foreground(pal.FgMute).Bold(true).Render("  " + label)
		rule := max(width-lipgloss.Width(head)-1, 0)
		return head + overlay.Style(bg).Foreground(pal.FgMute).
			Render(" "+strings.Repeat(m.Settings.GetWindowSeparatorChar(), rule))
	}

	nameColor := pal.FgDim
	marker := "  "
	if selected {
		bg = pal.Ground(overlay.RowState{Cursor: true, Focused: true}, bg)
		nameColor = pal.Fg
		marker = "› "
	}

	if row.Kind == dockRowEmpty {
		return overlay.Style(bg).Render(marker) +
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("nothing here")
	}

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

func (m *OS) dockEditorDetail(rows []dockEditorRow, pal overlay.Palette, width int) string {
	bg := pal.Surface
	if m.dockList.Cursor < 0 || m.dockList.Cursor >= len(rows) {
		return overlay.Style(bg).Render(" ")
	}
	row := rows[m.dockList.Cursor]

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
