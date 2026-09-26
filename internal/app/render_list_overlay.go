package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// listOverlay configures the shared search-and-list overlay used by the command
// palette, session switcher, layout picker and theme picker. It renders a panel
// with an optional search line, a scrolling list of rows, a scroll indicator,
// and returns the per-row hit rects for mouse routing.
type listOverlay struct {
	Title      string
	Width      int
	MaxVisible int
	Search     bool
	Query      string
	Count      int
	Selected   int
	// Scroll points at the caller's scroll offset; the renderer clamps it to the
	// rows it can actually show, which depends on the screen height.
	Scroll   *int
	EmptyMsg string
	Hints    []overlay.Hint
	// Detail is lines shown under the list, above the position line, each at
	// most the fitted width: more about the selected row than the row holds.
	// DetailFor builds them for the fitted width.
	DetailFor func(width int) []string
	// RenderRow returns the content for row i on the given row background, at
	// most width cells wide (width is the fitted panel width, not the requested
	// one); the helper fills the remainder with rowBg so the selection highlight
	// spans the row.
	//
	// Every fragment of the row, spacers included, has to carry rowBg. Padding
	// with a bare " " measures the same but paints nothing, so those cells are
	// written with the pen reset and the desktop shows through the panel there.
	RenderRow func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string
	// Position, when set, is where the cursor is for the readout, counted in
	// what a person counts: the Inbox's items, not its group headings.
	Position func() (n, of int)
}

// listOverlayLayout returns the fitted inner width and visible row count for a
// list overlay, so the keyboard and wheel navigation can scroll by the same
// number of rows the renderer draws.
func (m *OS) listOverlayLayout(cfg listOverlay) (width, rows int, hints []overlay.Hint) {
	width = m.panelWidth(cfg.Width)
	// Body lines that are not list rows: the scroll indicator, plus the search
	// input and its rule when the list has one.
	extra := 1
	if cfg.Search {
		extra += 2
	}
	if cfg.DetailFor != nil {
		if n := len(cfg.DetailFor(width)); n > 0 {
			extra += n + 1 // the lines and a rule above them
		}
	}
	rows, hints = m.panelBody(cfg.MaxVisible, extra, width, nil, cfg.Hints)
	return width, rows, hints
}

// renderListOverlay renders cfg into a panel and returns the string, geometry
// and hit rows.
func (m *OS) renderListOverlay(cfg listOverlay) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface

	cfg.Width, cfg.MaxVisible, cfg.Hints = m.listOverlayLayout(cfg)
	scroll := 0
	if cfg.Scroll != nil {
		*cfg.Scroll = scrollWindow(*cfg.Scroll, cfg.Selected, cfg.Count, cfg.MaxVisible)
		scroll = *cfg.Scroll
	}

	var lines []string
	rowYOffset := 0
	if cfg.Search {
		search := overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil()) +
			overlay.Style(bg).Foreground(pal.Fg).Render(cfg.Query) +
			overlay.Cursor(" ", bg, pal.Fg)
		lines = append(lines, search, overlay.Rule(cfg.Width, bg, pal))
		rowYOffset = 2
	}

	start := scroll
	end := min(start+cfg.MaxVisible, cfg.Count)
	shown := 0
	for i := start; i < end; i++ {
		rowBg := bg
		if i == cfg.Selected {
			rowBg = pal.RowSel
		}
		lines = append(lines, overlay.Fill(cfg.RenderRow(i, i == cfg.Selected, rowBg, pal, cfg.Width), cfg.Width, rowBg))
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
	// Where the cursor is in the list, under the list it counts. Under the
	// detail it read as the detail's own count: "4 of 7" under a plan of seven
	// lines, all shown.
	if cfg.Count > cfg.MaxVisible {
		n, of := cfg.Selected+1, cfg.Count
		if cfg.Position != nil {
			n, of = cfg.Position()
		}
		info := fmt.Sprintf("%d of %d", n, of)
		lines = append(lines, overlay.Style(bg).Foreground(pal.FgMute).Italic(true).Render("  "+info))
	} else {
		lines = append(lines, overlay.Style(bg).Render(" "))
	}
	if cfg.DetailFor != nil {
		if detail := cfg.DetailFor(cfg.Width); len(detail) > 0 {
			lines = append(lines, overlay.Rule(cfg.Width, bg, pal))
			for _, l := range detail {
				lines = append(lines, overlay.Style(bg).Foreground(pal.Fg).Render(l))
			}
		}
	}

	panel := overlay.Panel{
		Title: cfg.Title,
		Width: cfg.Width,
		Body:  strings.Join(lines, "\n"),
		Hints: cfg.Hints,
	}
	content, geo := panel.Render(pal)

	rows := make([]overlayRowHit, 0, max(end-start, 0))
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
func (m *OS) simpleOverlayPanel(title string, bodyLines []string, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(simplePanelWidth)
	// A message narrower than the panel is one line; a longer one wraps rather
	// than running off the edge.
	var styled []string
	for _, l := range bodyLines {
		for _, wrapped := range wrapPlain(l, width-2) {
			styled = append(styled, overlay.Style(bg).Foreground(pal.FgDim).Render("  "+wrapped))
		}
	}
	panel := overlay.Panel{
		Title: title,
		Width: width,
		Body:  strings.Join(styled, "\n"),
		Hints: hints,
	}
	content, geo := panel.Render(pal)
	return content, geo, nil
}

// simplePanelWidth is the preferred inner width of an informational panel.
const simplePanelWidth = 52

// wrapPlain breaks an unstyled string onto lines of at most width cells,
// preferring word boundaries. An empty input yields one empty line so blank
// spacer lines survive.
func wrapPlain(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" || lipgloss.Width(s) <= width {
		return []string{s}
	}
	var out []string
	line := ""
	flush := func() {
		if line != "" {
			out = append(out, line)
			line = ""
		}
	}
	for word := range strings.FieldsSeq(s) {
		// A word longer than the line (a path, say) is broken across lines
		// rather than dropped.
		for lipgloss.Width(word) > width {
			flush()
			runes := []rune(word)
			cut := len(runes)
			for cut > 1 && lipgloss.Width(string(runes[:cut])) > width {
				cut--
			}
			out = append(out, string(runes[:cut]))
			word = string(runes[cut:])
		}
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// moveListSelection advances a (selected, scroll) pair by delta within a list
// of count items showing maxVisible rows under the list rule (see listWraps),
// keeping the selection in view. Shared by the keyboard and the mouse wheel
// for the list overlays.
func (m *OS) moveListSelection(selected, scroll *int, count, maxVisible, delta int) {
	if count == 0 {
		return
	}
	*selected = m.listStep(*selected, delta, count)
	*scroll = listnav.Scroll(*scroll, *selected, count, maxVisible)
}

// SessionSwitcherMove moves the session switcher's selection by delta.
func (m *OS) SessionSwitcherMove(delta int) {
	n := len(FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery))
	m.moveListSelection(&m.SessionSwitcherSelected, &m.SessionSwitcherScroll, n, listOverlayRows, delta)
}

// LayoutPickerMove moves the layout picker's selection by delta.
func (m *OS) LayoutPickerMove(delta int) {
	n := len(FilterLayoutTemplates(m.LayoutPickerItems, m.LayoutPickerQuery))
	m.moveListSelection(&m.LayoutPickerSelected, &m.LayoutPickerScroll, n, listOverlayRows, delta)
}

// HostPickerMove moves the machine picker's selection by delta.
func (m *OS) HostPickerMove(delta int) {
	n := len(FilterHostPickerItems(m.HostPickerItems, m.HostPickerQuery))
	m.moveListSelection(&m.HostPickerSelected, &m.HostPickerScroll, n, listOverlayRows, delta)
}

// AggregateViewMove moves the window picker's selection by delta. The renderer
// keeps it in view.
func (m *OS) AggregateViewMove(delta int) {
	n := len(FilterAggregateViewItems(m.GetAggregateViewItems(), m.AggregateViewQuery))
	if n == 0 {
		m.AggregateViewSelected = 0
		return
	}
	m.AggregateViewSelected = m.listStep(m.AggregateViewSelected, delta, n)
}

// listOverlayRows is the page the list overlays scroll by before the renderer
// fits them to the screen, and the page a page key moves them by.
const listOverlayRows = 10

// listRowMarker returns the two-cell leading marker for a list row.
func listRowMarker(selected bool) string {
	if selected {
		return overlay.Sigil()
	}
	return "  "
}

// listRowSpans composes a list row from segments the caller has already styled,
// right-aligning the trailing one. It is listRowLine for rows whose two halves
// are not one colour each, such as a label followed by a dimmer identity.
func listRowSpans(width int, marker, left, right string, bg color.Color, pal overlay.Palette) string {
	m := overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(marker)
	// The label yields to the trailing segment, which is the row's facts: a
	// workspace's pane count, a session's state. Clamping the gap alone left the
	// row over-wide and the panel's backstop then took those facts off the right
	// end, so a named workspace showed its name and nothing else.
	ell := overlay.Style(bg).Foreground(pal.FgMute).Render(overlay.Ellipsis())
	avail := width - lipgloss.Width(m) - lipgloss.Width(right) - 1
	if lipgloss.Width(left) > avail {
		left = ansi.Truncate(left, max(avail-lipgloss.Width(ell), 0), "") + ell
	}
	gap := max(width-lipgloss.Width(m)-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return m + left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
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
