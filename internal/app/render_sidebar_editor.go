package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Rail layout editor panel size.
const (
	sectionEditorInnerWidth  = 46
	sectionEditorVisibleRows = 12
)

// sectionEditorHints is the footer, shared by the renderer and the sizing
// helper so both measure the same panel.
//
// The keys are the dock editor's, plus the one control the rail has that the
// dock does not: the share.
var sectionEditorHints = []overlay.Hint{
	{Key: "↑↓", Label: "select"},
	{Key: "⇧↑↓", Label: "move"},
	{Key: "←→", Label: "share"},
	{Key: "⏎", Label: "on/off"},
	{Key: "r", Label: "reset"},
	{Key: "u", Label: "undo"},
	{Key: "esc", Label: "close"},
}

// sectionEditorLayout returns the fitted inner width and visible row count.
func (m *OS) sectionEditorLayout() (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(sectionEditorInnerWidth)
	// One body line that is not a row: the line describing the selection.
	rows, hints = m.panelBody(sectionEditorVisibleRows, 1, width, nil, sectionEditorHints)
	return width, rows, hints
}

// renderSectionEditor draws the rail layout editor: the sections in the order
// the rail stacks them with the share each may claim, and what is off the rail
// underneath.
func (m *OS) renderSectionEditor() (string, overlay.Geometry, []overlayRowHit) {
	rows := m.sectionEditorRows()
	pal := theme.UI()
	bg := pal.Surface

	if len(rows) > 0 {
		m.SectionEditorSelected = clampInt(m.SectionEditorSelected, 0, len(rows)-1)
	} else {
		m.SectionEditorSelected = 0
	}
	width, visible, hints := m.sectionEditorLayout()
	m.SectionEditorScroll = scrollWindow(m.SectionEditorScroll, m.SectionEditorSelected, len(rows), visible)

	var lines []string
	start := m.SectionEditorScroll
	end := min(start+visible, len(rows))
	for i := start; i < end; i++ {
		lines = append(lines, m.sectionEditorLine(rows[i], i == m.SectionEditorSelected, pal, width))
	}
	for shown := end - start; shown < visible; shown++ {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}
	lines = append(lines, m.sectionEditorDetail(rows, pal, width))

	// A read-only session says so here as well as on the settings panel: the
	// editor can be opened on its own and the layout it writes goes nowhere.
	title := "Rail sections"
	if m.ConfigReadOnly {
		title = "Rail sections (this session only)"
	}

	panel := overlay.Panel{
		Glyph: "",
		Title: title,
		Width: width,
		Body:  strings.Join(lines, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)

	// The rectangles are recorded here, off the lines this loop just drew,
	// rather than worked out again by the click handler. A second copy of the
	// arithmetic is how a scrolled list starts sending clicks to the wrong row.
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

// sectionEditorLine renders one line of the editor.
func (m *OS) sectionEditorLine(row railEditorRow, selected bool, pal overlay.Palette, width int) string {
	bg := pal.Surface

	if row.Kind == railRowHeader {
		head := overlay.Style(bg).Foreground(pal.FgMute).Bold(true).Render("  " + row.Name)
		// A rule out to the panel edge, so the two lists read as bands rather
		// than as one list with words in it.
		rule := max(width-lipgloss.Width(head)-1, 0)
		return head + overlay.Style(bg).Foreground(pal.FgMute).
			Render(" "+strings.Repeat(m.Settings.GetWindowSeparatorChar(), rule))
	}

	nameColor := pal.FgDim
	marker := "  "
	if selected {
		bg = pal.RowSel
		nameColor = pal.Fg
		marker = "› "
	}

	if row.Kind == railRowEmpty {
		return overlay.Style(bg).Render(marker) +
			overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("nothing here")
	}

	// A section that is off the rail is drawn quieter than one on it, because
	// the list it is in is "what you could add" rather than "what you have".
	if row.Kind == railRowAvailable && !selected {
		nameColor = pal.FgMute
	}

	// The tail is the share for a placed entry, and nothing for one that is off
	// the rail: a section that is not drawn has no share to read.
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

// sectionEditorDetail is the line under the list, saying what the selected row
// is and what can be done to it.
//
// Every line here describes the layout, never this frame. That is deliberate:
// the panel measures nothing, so it must claim nothing a measurement could
// contradict. A spacer with a share gets those lines on a rail with room and
// nothing at all on one without, because empty space is the first thing the
// rail gives up when its sections cannot fit; a line reading "it keeps 40% of
// the rail" would be a promise this panel is in no position to make, and it
// would be wrong exactly when the user most needed to know. So a share is what
// the entry asks for, a bare spacer takes what nothing else needs, and a
// section's share is a ceiling and is spelled "at most".
func (m *OS) sectionEditorDetail(rows []railEditorRow, pal overlay.Palette, width int) string {
	if m.SectionEditorSelected < 0 || m.SectionEditorSelected >= len(rows) {
		return overlay.Style(pal.Surface).Render(" ")
	}
	return m.sectionEditorDetailFor(rows[m.SectionEditorSelected], pal, width)
}

// sectionEditorDetailFor is the description of one row, split out so a test can
// ask what the panel would say about a row without building the whole list.
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
