package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The collapsed rail is three columns: an edge rule and two cells. That is
// enough for exactly three things, and the composition is what makes them read
// as a design rather than as leftovers. The alarm is pinned to the top edge
// where a glance lands; the control is pinned to the bottom corner where the
// rail meets the panes; the identity stack floats centred between them. The
// old strip stacked everything from the top and left the rest blank, which read
// as debris.

// sidebarStripRowKind is what one line of the collapsed strip is.
type sidebarStripRowKind int

const (
	sidebarStripBadge sidebarStripRowKind = iota
	sidebarStripSession
	sidebarStripToggle
)

// sidebarStripRow is one drawn line of the collapsed strip, recorded by the
// renderer as it draws. Only the session rows and the toggle are clickable, so
// they also carry hit rectangles; this list exists because the strip's hover
// tooltip has to name what is under the pointer, including the badge, which is
// a readout rather than a control.
type sidebarStripRow struct {
	Kind sidebarStripRowKind
	Y    int
	// SessionID is set on a session row; the tooltip resolves the rest from it.
	SessionID string
}

// sidebarStripBadgeInfo is the alarm block at the strip's top: how many panes
// want a human anywhere, and the worst state among them.
type sidebarStripBadgeInfo struct {
	Count int
	State string
}

// sidebarStripBadgeFor counts the panes wanting a human across every session
// and picks the loudest state among them. Zero count means no badge at all:
// an alarm that is always on the screen is not an alarm.
func sidebarStripBadgeFor(sessions []sessiontree.Node) sidebarStripBadgeInfo {
	var info sidebarStripBadgeInfo
	best := 0
	for _, s := range sessions {
		for _, win := range s.Children {
			if !sidebarAttention(win.AgentState) {
				continue
			}
			info.Count++
			if r := sessiontree.AgentRank(win.AgentState, win.DoneSeen); r > best {
				info.State, best = win.AgentState, r
			}
		}
	}
	return info
}

// sidebarStripStackTop is where the session stack starts: centred in the lines
// the badge and the toggle leave. Centring plus pinned extremes rather than
// plain centring, so the alarm and the control keep fixed positions the eye can
// learn while the stack floats.
func sidebarStripStackTop(height, badge, toggle, rows int) (top, shown int) {
	region := max(height-badge-toggle, 0)
	shown = min(rows, region)
	return badge + (region-shown)/2, shown
}

// sidebarStripLines draws the collapsed rail: the attention badge pinned to the
// top, the session stack centred, and the expand toggle in the bottom
// pane-facing corner.
func (m *OS) sidebarStripLines(sessions []sessiontree.Node, w, cw, height, topMargin, sidebarX, contentX0 int,
	pal overlay.Palette, compose func(string) string, blank string,
) ([]string, int) {
	m.sidebarStripRows = m.sidebarStripRows[:0]

	badge := sidebarStripBadgeFor(sessions)
	badgeH := 0
	if badge.Count > 0 && height > 1 {
		badgeH = 1
	}
	toggleGlyph, canToggle := m.sidebarCollapseGlyph(sidebarVariantGlyph)
	toggleH := 0
	if canToggle && height > badgeH {
		toggleH = 1
	}
	stackTop, shown := sidebarStripStackTop(height, badgeH, toggleH, len(sessions))

	hover := -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		hover = m.SidebarHoverY - topMargin
	}

	nav := make([]sidebarNavRow, 0, shown+1)
	lines := make([]string, 0, height)
	for len(lines) < height {
		y, i := topMargin+len(lines), len(lines)
		switch {
		case badgeH > 0 && i == 0:
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{Kind: sidebarStripBadge, Y: y})
			lines = append(lines, compose(sidebarStripBadgeCell(badge, cw, pal)))
		case i >= stackTop && i < stackTop+shown:
			s := sessions[i-stackTop]
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{Kind: sidebarStripSession, Y: y, SessionID: s.ID})
			m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
				X0: sidebarX, X1: sidebarX + w,
				Y0: y, Y1: y + 1,
				Kind: sidebarRowSession, SessionID: s.ID, WindowIndex: -1,
			})
			nav = append(nav, sidebarNavRow{Kind: sidebarRowSession, SessionID: s.ID, WindowIndex: -1})
			dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
			lines = append(lines, compose(m.sidebarStripCell(s, cw, pal, i == hover, dragged)))
		case toggleH > 0 && i == height-1:
			// The pane-facing corner: the control sits where the rail meets the
			// panes, which is the edge the pointer arrives from. It is measured
			// from that edge inwards, so the two-cell ASCII form still lands
			// against it instead of being clipped by it.
			tw := lipgloss.Width(toggleGlyph)
			x0 := max(cw-tw, 0)
			if config.SidebarPosition == "right" {
				x0 = 0
			}
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{Kind: sidebarStripToggle, Y: y})
			m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
				X0: contentX0 + x0, X1: contentX0 + min(x0+tw, cw),
				Y0: y, Y1: y + 1,
				Kind: sidebarRowCollapse, WindowIndex: -1,
			})
			nav = append(nav, sidebarNavRow{Kind: sidebarRowCollapse, WindowIndex: -1})
			lines = append(lines, compose(sidebarStripToggleCell(toggleGlyph, x0, cw, i == hover, pal)))
		default:
			lines = append(lines, blank)
		}
	}

	// The strip has one list, so a wheel has nowhere else to send a scroll.
	for s := range m.sidebarSectionY {
		m.sidebarSectionY[s] = [2]int{topMargin, topMargin}
	}
	m.SidebarNav = nav
	m.sidebarFollowSession = ""
	if m.SidebarCursor >= len(nav) {
		m.SidebarCursor = max(len(nav)-1, 0)
	}
	return lines, w
}

// sidebarStripBadgeCell is the alarm block: how many panes want a human and the
// worst state among them, with the whole cell inked in that severity. Two cells
// is too little room for a coloured glyph to carry an alarm, so the ink is the
// cell and the glyph is knocked out of it.
func sidebarStripBadgeCell(info sidebarStripBadgeInfo, cw int, pal overlay.Palette) string {
	count := strconv.Itoa(info.Count)
	if info.Count > 9 {
		count = "+"
	}
	glyph := agentStateIndicator(info.State)
	if glyph == "" {
		glyph = "!"
	}
	bg := agentGlyphColor(info.State, pal)
	return sidebarFit(sidebarStyle(bg, pal.Canvas).Render(count+glyph), cw, bg)
}

// sidebarStripToggleCell draws the expand arrow on its own line, on the given
// content column.
func sidebarStripToggleCell(glyph string, x0, cw int, hovered bool, pal overlay.Palette) string {
	fg := pal.FgMute
	if hovered {
		fg = pal.Fg
	}
	row := strings.Repeat(" ", max(x0, 0)) + sidebarStyle(nil, fg).Render(glyph)
	return sidebarFit(row, cw, nil)
}

// sidebarStripCell is one session's two cells on the collapsed strip: a lead
// cell of identity and a state glyph.
//
// The lead cell is the accent mark on the attached session, the window count on
// the others, and blank when there is neither. The attached session wore a
// Surface fill before, which is the one standing band the rest of the rail
// spent a round getting rid of; the same mark it wears in the gutter of the
// expanded rail says it without painting anything.
//
// A session wanting a human inks its whole cell, which outranks everything
// else: the entire job of a three-column rail is saying which session wants you.
func (m *OS) sidebarStripCell(node sessiontree.Node, cw int, pal overlay.Palette, hovered, dragged bool) string {
	mark := agentStateIndicator(node.AgentState)
	if mark == "" || !config.SidebarShowGlyphs {
		mark = "·"
		if overlay.UseASCII() {
			mark = "."
		}
	}

	lead, leadFg := " ", pal.FgMute
	switch {
	case node.IsCurrent:
		lead = "▎"
		if overlay.UseASCII() {
			lead = ">"
		}
		leadFg = pal.Accent
	case config.SidebarShowCounts && node.WindowCount > 1:
		lead = strconv.Itoa(node.WindowCount)
		if node.WindowCount > 9 {
			lead = "+"
		}
	}

	var bg, fg color.Color
	switch {
	case sidebarAttention(node.AgentState) && config.SidebarShowGlyphs:
		// The count is knocked out with the glyph, or it would be a muted mark
		// on a saturated fill.
		bg, fg, leadFg = agentGlyphColor(node.AgentState, pal), pal.Canvas, pal.Canvas
	case hovered || dragged:
		bg = pal.RowSel
	}
	if fg == nil {
		fg = pal.FgMute
		if config.SidebarShowGlyphs && agentStateIndicator(node.AgentState) != "" {
			fg = sidebarStateColor(node.AgentState, node.DoneSeen, pal)
		}
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+sidebarStyle(bg, fg).Render(mark), cw, bg)
}
