package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The theme editor's sheet: a panel as tall as the pane region, on the side
// the rail is not on. Each row is a label, a swatch, where the colour comes
// from, its hex and, for a colour that carries text or a mark, its contrast on
// the ground it is drawn on.

// themeEditorWidth is the sheet's preferred inner width.
const themeEditorWidth = 56

// Column widths of a row, inside the two-cell cursor marker.
const (
	themeEditorLabelW  = 17
	themeEditorSourceW = 19
	themeEditorHexW    = 8
)

// themeEditorHints is the sheet's footer.
var themeEditorHints = []overlay.Hint{
	{Key: overlay.EnterGlyph, Label: "pick"},
	{Key: "e", Label: "type"},
	{Key: "x", Label: "reset"},
	{Key: "u", Label: "undo"},
	{Key: "^s", Label: "save"},
	{Key: "tab", Label: "next tab", Priority: overlay.HintOptional},
	{Key: "space", Label: "hide", Priority: overlay.HintOptional},
	{Key: "esc", Label: "close"},
}

// themeEditorTypingHints is the footer while a value is typed.
var themeEditorTypingHints = []overlay.Hint{
	{Key: overlay.EnterGlyph, Label: "use"},
	{Key: "tab", Label: "complete"},
	{Key: "esc", Label: "cancel"},
}

// themeEditorLine is one line of the sheet's list: a group heading, or a row.
type themeEditorLine struct {
	heading string
	row     int // index into the tab's rows, -1 for a heading or a blank
}

// themeEditorLines lays the tab's rows out under their group headings.
func themeEditorLines(rows []themeEditorRow) []themeEditorLine {
	var out []themeEditorLine
	group := ""
	for i, r := range rows {
		if r.group != group {
			if group != "" {
				out = append(out, themeEditorLine{row: -1})
			}
			group = r.group
			out = append(out, themeEditorLine{heading: group, row: -1})
		}
		out = append(out, themeEditorLine{row: i})
	}
	return out
}

// themeEditorPlace is where the sheet goes: its left column, its top row, its
// total width and its height. It covers the side with no rail and the rows
// between the dock's.
func (m *OS) themeEditorPlace() (x, y, w, h int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	inner := m.panelWidth(themeEditorWidth)
	w = min(inner+2*overlay.DefaultPanelPadding, rw)
	r := m.viewReserve()
	y = min(r.Top, max(rh-1, 0))
	h = max(rh-r.Top-r.Bottom, 1)
	switch {
	case m.GetRightMargin() > 0 && m.PaneLeft() == 0:
		x = 0
	case m.PaneLeft() > 0 && m.GetRightMargin() == 0:
		x = max(rw-w, 0)
	default:
		x = max(rw-w-m.GetRightMargin(), 0)
	}
	return x, y, w, h
}

// renderThemeEditor draws the sheet, or the one-line chip that stands for it
// while it is hidden, as a layer.
func (m *OS) renderThemeEditor() *lipgloss.Layer {
	st := &m.themeEditor
	pal := theme.UI()
	x, y, w, h := m.themeEditorPlace()
	z := m.overlayZ("themeeditor")
	if st.hidden {
		chip := overlay.Chip(" Theme editor: press space to show it ", pal.Accent, pal.PillFg)
		return lipgloss.NewLayer(chip).X(x).Y(y).Z(z).ID("themeeditor")
	}

	inner := max(w-2*overlay.DefaultPanelPadding, 10)
	hints := themeEditorHints
	if st.typingOn {
		hints = themeEditorTypingHints
	}
	// The rows that are not the list: top pad, title, blank, tabs, rule,
	// blank, the blank and rule above the footer, the footer and the bottom
	// pad, then the list's own notes.
	fixed := 9 + overlay.HintRowCount(hints, inner)
	notes := m.themeEditorNotes(inner, pal)
	listRows := max(h-fixed-len(notes), 1)

	rows := m.themeEditorRows()
	lines := themeEditorLines(rows)
	cursorLine := 0
	for i, l := range lines {
		if l.row == st.cursor[st.tab] {
			cursorLine = i
		}
	}
	scroll := &st.scroll[st.tab]
	if cursorLine < *scroll {
		*scroll = cursorLine
		// Keep the group's heading in view with its first row.
		if *scroll > 0 && lines[*scroll-1].heading != "" {
			*scroll--
		}
	}
	if cursorLine >= *scroll+listRows {
		*scroll = cursorLine - listRows + 1
	}
	*scroll = max(0, min(*scroll, max(len(lines)-listRows, 0)))

	var body []string
	var hits []overlayRowHit
	for i := *scroll; i < len(lines) && len(body) < listRows; i++ {
		l := lines[i]
		switch {
		case l.heading != "":
			body = append(body, overlay.Style(pal.Surface).Foreground(pal.FgDim).Bold(true).Render(l.heading))
		case l.row < 0:
			body = append(body, "")
		default:
			hits = append(hits, overlayRowHit{Idx: l.row})
			hits[len(hits)-1].Rect.Y0 = len(body)
			body = append(body, m.themeEditorRowLine(rows[l.row], l.row == st.cursor[st.tab], inner, pal))
		}
	}
	for len(body) < listRows {
		body = append(body, "")
	}
	body = append(body, notes...)

	title := "Edit theme: " + st.draft.ID
	if m.themeEditorDirty() {
		title += " *"
	}
	content, geo := overlay.Panel{
		Title:     title,
		Width:     inner,
		Tabs:      themeEditorTabs[:],
		ActiveTab: st.tab,
		Body:      strings.Join(body, "\n"),
		Hints:     hints,
	}.Render(pal)
	for i := range hits {
		y0 := geo.BodyY + hits[i].Rect.Y0
		hits[i].Rect = overlay.Rect{X0: geo.BodyX, Y0: y0, X1: geo.BodyX + inner, Y1: y0 + 1}
	}
	// The panel is as tall as its rows. The rest of the sheet's height is
	// surface, so the sheet reaches the dock whatever the list's length.
	if extra := h - geo.Height; extra > 0 {
		blank := overlay.Style(pal.Surface).Render(strings.Repeat(" ", geo.Width))
		content += strings.Repeat("\n"+blank, extra)
		geo.Height = h
	}
	m.OverlayHits = append(m.OverlayHits, overlayPanelHit{Kind: "themeeditor", OriginX: x, OriginY: y, Z: z, Geo: geo, Rows: hits})
	return lipgloss.NewLayer(content).X(x).Y(y).Z(z).ID("themeeditor")
}

// themeEditorNotes are the lines under the list: why the draft does not
// load, the value being typed, and on the pane tab what those colours are.
func (m *OS) themeEditorNotes(width int, pal overlay.Palette) []string {
	st := &m.themeEditor
	var out []string
	note := func(s string, ink color.Color) {
		out = append(out, overlay.Style(pal.Surface).Foreground(ink).Render(overlay.Truncate(s, width)))
	}
	if st.err != nil {
		note("! "+st.err.Error(), pal.Warn)
	}
	if st.tab == themeEditorTabPane {
		note("Programs in panes use these colours.", pal.FgMute)
	}
	if !st.live {
		note("This client shows the colours here only.", pal.FgMute)
	}
	if st.typingOn {
		field := overlay.Style(pal.Card).Foreground(pal.Fg).Render(" " + st.typing + "█ ")
		out = append(out, overlay.Style(pal.Surface).Foreground(pal.FgDim).Render("Value: ")+field)
	}
	return out
}

// themeEditorRowLine draws one row: the marker, the label, the swatch, the
// source, the hex and the contrast badge.
func (m *OS) themeEditorRowLine(row themeEditorRow, cursor bool, width int, pal overlay.Palette) string {
	st := overlay.RowState{Cursor: cursor, Focused: true}
	ground := pal.Ground(st, pal.Surface)
	base := overlay.Style(ground)
	marker := "  "
	labelInk := pal.FgDim
	if cursor {
		marker, labelInk = "› ", pal.Fg
	}
	c := m.themeEditorColour(row)
	swatch := base.Render("  ")
	hex := "none"
	if c != nil && !overlay.IsNoColor(c) {
		swatch = lipgloss.NewStyle().Background(c).Render("  ")
		hex = overlay.Hex(toRGBA(c))
	}
	label := overlay.Truncate(row.label, themeEditorLabelW-1)
	source := overlay.Truncate(m.themeEditorSource(row), themeEditorSourceW-1)

	var b strings.Builder
	b.WriteString(base.Foreground(labelInk).Render(marker + padRight(label, themeEditorLabelW)))
	b.WriteString(swatch)
	b.WriteString(base.Render(" "))
	b.WriteString(base.Foreground(pal.FgMute).Render(padRight(source, themeEditorSourceW)))
	b.WriteString(base.Foreground(pal.FgDim).Render(padRight(hex, themeEditorHexW)))
	if floor, on := m.themeEditorFloor(row); floor > 0 && on != nil && c != nil && !overlay.IsNoColor(c) {
		ratio := themeEditorRatio(c, on)
		badge := fmt.Sprintf("%.1f:1", ratio)
		if ratio < floor {
			b.WriteString(base.Foreground(pal.Warn).Bold(true).Render("! " + badge))
		} else {
			b.WriteString(base.Foreground(pal.FgMute).Render("  " + badge))
		}
	}
	return pal.Row(b.String(), width, st, pal.Surface)
}

// padRight pads s with spaces to w cells.
func padRight(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
