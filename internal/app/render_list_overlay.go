package app

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	// Scroll points at the caller's scroll offset; the renderer clamps it to the
	// rows it can actually show, which depends on the screen height.
	Scroll   *int
	EmptyMsg string
	Hints    []overlay.Hint
	// RenderRow returns the content for row i on the given row background, at
	// most width cells wide (width is the fitted panel width, not the requested
	// one); the helper fills the remainder with rowBg so the selection highlight
	// spans the row.
	RenderRow func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string
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
func (m *OS) simpleOverlayPanel(glyph, title string, bodyLines []string, hints []overlay.Hint) (string, overlay.Geometry, []overlayRowHit) {
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
		Glyph: glyph,
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
	for _, word := range strings.Fields(s) {
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
