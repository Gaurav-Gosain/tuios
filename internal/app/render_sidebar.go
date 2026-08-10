package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// sidebarFocusColor is the focused-row background, matching the dock's focused
// pill (render_dock.go) so the two chrome surfaces never disagree about what
// "focused" looks like.
const sidebarFocusColor = "#4865f2"

// sidebarRowKind distinguishes a session row from a window row for mouse routing.
type sidebarRowKind int

const (
	sidebarRowSession sidebarRowKind = iota
	sidebarRowWindow
)

// sidebarRowHit is the on-screen rectangle of one sidebar row, in absolute
// screen coordinates, plus what it points at. The mouse handlers hit-test these
// to route a click to a session switch, a window focus, or the context menu.
type sidebarRowHit struct {
	X0, Y0, X1, Y1 int
	Kind           sidebarRowKind
	SessionID      string
	WindowID       string
	// WindowIndex is the index into m.Windows for a window row of the currently
	// attached session, or -1 for a window row of another session (not directly
	// focusable without switching first) and for session rows.
	WindowIndex int
}

// Contains reports whether the absolute cell (x, y) falls on this row.
func (r sidebarRowHit) Contains(x, y int) bool {
	return x >= r.X0 && x < r.X1 && y >= r.Y0 && y < r.Y1
}

// sidebar layout variants, chosen from the reserved width so the same width that
// geometry reserves is the width this draws into.
const (
	sidebarVariantGlyph = iota
	sidebarVariantNarrow
	sidebarVariantFull
)

func sidebarVariant(w int) int {
	switch {
	case w <= config.SidebarGlyphWidth:
		return sidebarVariantGlyph
	case w <= config.SidebarNarrowWidth:
		return sidebarVariantNarrow
	default:
		return sidebarVariantFull
	}
}

// sidebarSessionExpanded reports whether a session's window rows are shown. The
// currently attached session is expanded by default; others are collapsed. The
// user's explicit toggles in SidebarCollapsed override the default.
func (m *OS) sidebarSessionExpanded(node sessiontree.Node) bool {
	if m.SidebarCollapsed != nil {
		if v, ok := m.SidebarCollapsed[node.ID]; ok {
			return !v
		}
	}
	return node.IsCurrent
}

// agentGlyphColor maps an agent state to the palette color its glyph is drawn
// in. The glyph shapes come from agentStateIndicator so the sidebar, the title
// bar, and the palette never diverge; only the color is chosen here.
func agentGlyphColor(state string, pal overlay.Palette) color.Color {
	switch state {
	case "working":
		return pal.Info
	case "needs_input":
		return pal.Warning
	case "idle":
		return pal.FgMute
	case "done":
		return pal.Success
	case "errored":
		return pal.Warn
	default:
		return pal.FgMute
	}
}

// renderSidebar composes the vertical session sidebar as a single layer, the way
// renderDock composes the dock. It returns nil when the sidebar reserves no
// columns (off, hidden, or the screen too narrow). It also records the on-screen
// hit geometry of every row into m.SidebarHits for the mouse handlers.
func (m *OS) renderSidebar() *lipgloss.Layer {
	lines, w := m.sidebarPanelLines()
	if lines == nil {
		return nil
	}
	sidebarX := 0
	if config.SidebarPosition == "right" {
		sidebarX = m.GetRenderWidth() - w
	}
	panel := strings.Join(lines, "\n")
	return lipgloss.NewLayer(panel).X(sidebarX).Y(m.GetTopMargin()).Z(config.ZIndexDock).ID("sidebar")
}

// sidebarPanelLines builds the sidebar's rows and records their on-screen hit
// geometry into m.SidebarHits, returning the rows and the reserved width. It
// returns nil rows when the sidebar reserves nothing. Split out of renderSidebar
// so the layout can be asserted directly in tests without extracting a layer's
// content.
func (m *OS) sidebarPanelLines() ([]string, int) {
	m.SidebarHits = m.SidebarHits[:0]

	w := m.GetSidebarWidth()
	if w <= 0 {
		return nil, 0
	}
	height := m.GetUsableHeight()
	if height <= 0 {
		return nil, 0
	}

	topMargin := m.GetTopMargin()
	sidebarX := 0
	if config.SidebarPosition == "right" {
		sidebarX = m.GetRenderWidth() - w
	}

	pal := theme.UI()
	bg := pal.Panel
	variant := sidebarVariant(w)

	tree := m.BuildSessionTree()

	// Build the logical rows first (header + sessions + their windows), each with
	// the on-screen target it routes to, then window them by SidebarScroll to fit
	// the available height. Recording hits after the scroll slice keeps a row's
	// stored Y equal to where it is actually drawn.
	type logicalRow struct {
		text        string
		hit         *sidebarRowHit // nil for non-interactive rows (header/spacer)
		sessionID   string
		windowID    string
		windowIndex int
		kind        sidebarRowKind
	}

	rows := make([]logicalRow, 0, 16)

	// Header. A quiet section label rather than an accent chip: the sidebar is
	// chrome that is on screen all the time, so it identifies itself the way a
	// section heading does, not the way a floating panel announces itself. The
	// session count sits right-aligned as the one piece of summary data.
	if variant != sidebarVariantGlyph {
		rows = append(rows, logicalRow{text: m.sidebarHeaderRow(len(tree.Sessions), w, pal)})
		rows = append(rows, logicalRow{text: sidebarBlank(w, bg)})
	}

	for _, session := range tree.Sessions {
		expanded := m.sidebarSessionExpanded(session)
		rows = append(rows, logicalRow{
			text:        m.sidebarSessionRow(session, variant, expanded, w, pal),
			hit:         &sidebarRowHit{},
			sessionID:   session.ID,
			windowIndex: -1,
			kind:        sidebarRowSession,
		})

		if variant == sidebarVariantGlyph || !config.SidebarShowWindows || !expanded {
			continue
		}
		for _, win := range session.Children {
			idx := -1
			if session.IsCurrent {
				idx = m.windowIndexByID(win.ID)
			}
			rows = append(rows, logicalRow{
				text:        m.sidebarWindowRow(win, variant, w, pal),
				hit:         &sidebarRowHit{},
				sessionID:   session.ID,
				windowID:    win.ID,
				windowIndex: idx,
				kind:        sidebarRowWindow,
			})
		}
	}

	// Vertical scroll. Header rows scroll with the list so a long session list can
	// be reached; clamp so the last row is always reachable and never overscrolled.
	maxScroll := max(len(rows)-height, 0)
	m.SidebarScroll = max(min(m.SidebarScroll, maxScroll), 0)

	end := min(m.SidebarScroll+height, len(rows))
	visible := rows[m.SidebarScroll:end]

	lines := make([]string, 0, height)
	for i, r := range visible {
		screenY := topMargin + i
		if r.hit != nil {
			r.hit.X0 = sidebarX
			r.hit.X1 = sidebarX + w
			r.hit.Y0 = screenY
			r.hit.Y1 = screenY + 1
			r.hit.Kind = r.kind
			r.hit.SessionID = r.sessionID
			r.hit.WindowID = r.windowID
			r.hit.WindowIndex = r.windowIndex
			m.SidebarHits = append(m.SidebarHits, *r.hit)
		}
		lines = append(lines, r.text)
	}
	// Pad the column to the full height so the sidebar is a solid surface.
	for len(lines) < height {
		lines = append(lines, sidebarBlank(w, bg))
	}

	return lines, w
}

// windowIndexByID returns the index of the window with the given ID in m.Windows,
// or -1. Used to turn a sidebar window row into a focusable pane.
func (m *OS) windowIndexByID(id string) int {
	for i, w := range m.Windows {
		if w != nil && w.ID == id {
			return i
		}
	}
	return -1
}

// sidebarBlank is a full-width surface-filled blank row.
func sidebarBlank(w int, bg color.Color) string {
	return overlay.Style(bg).Render(strings.Repeat(" ", w))
}

// sidebarFit pads s to exactly w cells on bg, truncating (ANSI-aware) anything
// that overruns so a row can never draw past the sidebar's own column.
func sidebarFit(s string, w int, bg color.Color) string {
	if lipgloss.Width(s) > w {
		s = lipgloss.NewStyle().MaxWidth(w).Render(s)
	}
	return overlay.Fill(s, w, bg)
}

// sidebarGlyph returns the styled agent-state glyph for a row, or a single
// bg-colored space when there is no state or glyphs are disabled, so rows stay
// aligned. It always occupies exactly one cell.
func sidebarGlyph(state string, bg color.Color, pal overlay.Palette) string {
	if !config.SidebarShowGlyphs {
		return overlay.Style(bg).Render(" ")
	}
	g := agentStateIndicator(state)
	if g == "" {
		return overlay.Style(bg).Render(" ")
	}
	return overlay.Style(bg).Foreground(agentGlyphColor(state, pal)).Render(g)
}

// sidebarHeaderRow renders the quiet section header: the label on the left and
// the session count right-aligned, both muted so the header frames the list
// without competing with it.
func (m *OS) sidebarHeaderRow(sessionCount, w int, pal overlay.Palette) string {
	bg := pal.Panel
	count := strconv.Itoa(sessionCount)
	countW := lipgloss.Width(count)
	label := overlay.Truncate("Sessions", max(w-countW-3, 1))
	left := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.FgDim).Bold(true).Render(label)
	gap := max(w-lipgloss.Width(left)-countW-1, 0)
	row := left + overlay.Style(bg).Render(strings.Repeat(" ", gap)) +
		overlay.Style(bg).Foreground(pal.FgMute).Render(count) +
		overlay.Style(bg).Render(" ")
	return sidebarFit(row, w, bg)
}

// sidebarCurrentMarker is the one-cell edge bar that marks the current session,
// drawn in the success color at the very edge of the rail so it aligns down the
// column and reads without a legend.
func sidebarCurrentMarker(current bool, bg color.Color, pal overlay.Palette) string {
	if !current {
		return overlay.Style(bg).Render(" ")
	}
	mark := "▎"
	if overlay.UseASCII() {
		mark = "|"
	}
	return overlay.Style(bg).Foreground(pal.Success).Render(mark)
}

// sidebarSessionRow renders one session row for the given variant.
//
// Full variant anatomy, kept to fixed columns so the rows scan vertically:
//
//	▎ ▾ ● name          3
//	^ ^ ^ ^             ^ window count, right-aligned, muted
//	| | | name: bold on the current session, dim on the others
//	| | agent glyph, state-colored
//	| expand chevron, muted
//	current-session edge bar
//
// The current session's row is additionally raised one luminance step (Surface
// on the Panel rail), so "where am I" reads even in a monochrome capture.
func (m *OS) sidebarSessionRow(node sessiontree.Node, variant int, expanded bool, w int, pal overlay.Palette) string {
	if variant == sidebarVariantGlyph {
		// One centered rolled-up glyph per session; the current session gets the
		// focus pill so it is findable without a name.
		bg := color.Color(pal.Panel)
		if node.IsCurrent {
			bg = lipgloss.Color(sidebarFocusColor)
		}
		g := sidebarGlyph(node.AgentState, bg, pal)
		return sidebarFit(overlay.Style(bg).Render(" ")+g, w, bg)
	}

	bg := color.Color(pal.Panel)
	if node.IsCurrent {
		bg = pal.Surface
	}

	left := sidebarCurrentMarker(node.IsCurrent, bg, pal)

	// Expand/collapse chevron for sessions with known windows; a blank cell
	// otherwise so the glyph and name columns stay aligned.
	if variant == sidebarVariantFull {
		chevron := " "
		if config.SidebarShowWindows && len(node.Children) > 0 {
			chevron = "▸"
			if expanded {
				chevron = "▾"
			}
			if overlay.UseASCII() {
				chevron = ">"
				if expanded {
					chevron = "v"
				}
			}
		}
		left += overlay.Style(bg).Render(" ") +
			overlay.Style(bg).Foreground(pal.FgMute).Render(chevron) +
			overlay.Style(bg).Render(" ")
	}

	left += sidebarGlyph(node.AgentState, bg, pal) + overlay.Style(bg).Render(" ")

	// Right side: window count, muted, with a trailing pad off the edge.
	right := ""
	rightW := 0
	if config.SidebarShowCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		countStr := strconv.Itoa(node.WindowCount)
		right = overlay.Style(bg).Foreground(pal.FgMute).Render(countStr) +
			overlay.Style(bg).Render(" ")
		rightW = lipgloss.Width(countStr) + 1
	}

	nameW := max(w-lipgloss.Width(left)-rightW-1, 1)
	nameStyle := overlay.Style(bg).Foreground(pal.FgDim)
	if node.IsCurrent {
		nameStyle = overlay.Style(bg).Foreground(pal.Fg).Bold(true)
	}
	name := nameStyle.Render(overlay.Truncate(node.Title, nameW))

	row := left + name
	gap := max(w-lipgloss.Width(row)-rightW, 0)
	row += overlay.Style(bg).Render(strings.Repeat(" ", gap)) + right
	return sidebarFit(row, w, bg)
}

// sidebarWindowRow renders one window row under an expanded session, indented
// one level past the session name. The focused window carries the dock's focus
// pill across the full row; the rest stay dim so the list reads as detail under
// its session, not as a second list of equals.
func (m *OS) sidebarWindowRow(node sessiontree.Node, variant int, w int, pal overlay.Palette) string {
	bg := color.Color(pal.Panel)
	fg := pal.FgDim
	bold := false
	focused := node.IsCurrent
	if focused {
		bg = lipgloss.Color(sidebarFocusColor)
		fg = lipgloss.Color("#ffffff")
		bold = true
	}

	indent := "      " // glyph lands two cells past the session glyph column
	if variant == sidebarVariantNarrow {
		indent = "  "
	}

	// On the focus pill the glyph goes white with the title: state colors were
	// tuned against the dark rail and some vanish on saturated blue. The shape
	// still carries the state, as everywhere else.
	var glyph string
	if focused {
		g := agentStateIndicator(node.AgentState)
		if g == "" || !config.SidebarShowGlyphs {
			glyph = overlay.Style(bg).Render(" ")
		} else {
			glyph = overlay.Style(bg).Foreground(fg).Render(g)
		}
	} else {
		glyph = sidebarGlyph(node.AgentState, bg, pal)
	}

	leftFixed := lipgloss.Width(indent) + 2
	titleW := max(w-leftFixed-1, 1)
	title := overlay.Style(bg).Foreground(fg).Bold(bold).Render(overlay.Truncate(node.Title, titleW))

	row := overlay.Style(bg).Render(indent) + glyph + overlay.Style(bg).Render(" ") + title
	return sidebarFit(row, w, bg)
}
