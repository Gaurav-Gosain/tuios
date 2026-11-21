package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	sectionEditorInnerWidth  = 46
	sectionEditorVisibleRows = 12
)

var sectionEditorHints = []overlay.Hint{
	{Key: hintListMoveLetters, Label: "select"},
	{Key: "⇧↑↓", Label: "move"},
	{Key: "←→", Label: "share"},
	{Key: overlay.EnterGlyph, Label: "on/off"},
	{Key: "r", Label: "reset"},
	{Key: "u", Label: "undo"},
	{Key: "esc", Label: "close"},
}

func (m *OS) sectionEditorLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(sectionEditorInnerWidth)
	rows, hints = m.panelBody(sectionEditorVisibleRows, 1, width, nil, sectionEditorHints)
	return width, rows, hints
}

func (m *OS) renderSectionEditor() (string, overlay.Geometry, []overlayRowHit) {
	rows := m.sectionEditorRows()
	pal := m.overlayUI()
	bg := pal.Surface

	width, visible, hints := m.sectionEditorLayout()
	m.sectionList.Fit(len(rows), visible)

	var lines []string
	start := m.sectionList.Scroll
	end := min(start+visible, len(rows))
	for i := start; i < end; i++ {
		lines = append(lines, pal.Row(m.sectionEditorLine(rows[i], i == m.sectionList.Cursor, pal, width), width, overlay.RowState{Cursor: i == m.sectionList.Cursor, Focused: true}, pal.Surface))
	}
	for shown := end - start; shown < visible; shown++ {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}
	lines = append(lines, m.sectionEditorDetail(rows, pal, width))

	title := "Rail sections"
	if m.ConfigReadOnly {
		title = "Rail sections (this session only)"
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
		if rows[i].Kind == railRowHeader {
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

func (m *OS) sectionEditorLine(row railEditorRow, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface

	if row.Kind == railRowHeader {
		head := overlay.Style(bg).Foreground(pal.FgMute).Bold(true).Render("  " + row.Name)
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

	if row.Kind == railRowEmpty {
		return overlay.Style(bg).Render(marker) +
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("nothing here")
	}

	if row.Kind == railRowAvailable && !selected {
		nameColor = pal.FgMute
	}

	tail := ""
	if row.Kind == railRowPlaced {
		tail = sectionShareLabel(row.Share)
	}
	if row.Spacer && row.Kind == railRowAvailable {
		tail = "add"
	}

	name := row.Name
	if row.Spacer {
		name = "spacer"
	}
	left := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(nameColor).Bold(selected).
			Render(overlay.Truncate(name, max(width-2-lipgloss.Width(tail)-3, 1)))
	if tail == "" {
		gap := max(width-lipgloss.Width(left), 0)
		return left + overlay.Style(bg).Render(strings.Repeat(" ", gap))
	}
	ink := pal.FgMute
	if selected && row.Kind == railRowPlaced {
		ink = pal.Fg
	}
	marked := overlay.Style(bg).Foreground(ink).Render(tail)
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(marked)-1, 1)
	return left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + marked +
		overlay.Style(bg).Render(" ")
}

func (m *OS) sectionEditorDetail(rows []railEditorRow, pal overlay.Palette, width int) string {
	if m.sectionList.Cursor < 0 || m.sectionList.Cursor >= len(rows) {
		return overlay.Style(pal.Surface).Render(" ")
	}
	return m.sectionEditorDetailFor(rows[m.sectionList.Cursor], pal, width)
}

func (m *OS) sectionEditorDetailFor(row railEditorRow, pal overlay.Palette, width int) string {
	bg := pal.Surface

	var text string
	switch {
	case row.Kind == railRowEmpty:
		text = "The rail draws nothing"
	case row.Kind == railRowAvailable && row.Spacer:
		text = "Empty space. Press enter to add one."
	case row.Kind == railRowAvailable:
		text = "Not on the rail. Press enter to add it."
	case row.Spacer && row.Share > 0:
		text = "Empty space. It asks for " + sectionShareLabel(row.Share) + " of the rail."
	case row.Spacer && row.Last:
		text = "Empty space at the end of the rail."
	case row.Spacer:
		text = "Empty space. It takes what is left over."
	case row.Share <= 0:
		text = "It takes the lines the others leave."
	default:
		text = "It takes " + sectionShareLabel(row.Share) + " of the rail at most."
	}
	return overlay.Style(bg).Foreground(pal.FgMute).Italic(true).
		Render("  " + overlay.Truncate(text, max(width-2, 1)))
}
