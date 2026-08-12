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

// The collapsed rail is three columns, and what was wrong with it was the space
// its marks sat in rather than the marks. On bare Canvas the strip has no ground
// of its own, so it merges with the glyph margin of whatever agent TUI is
// running in the pane beside it: two columns of small marks three cells apart
// read as one object. The strip is now a full-height Panel band across all three
// columns. Guest margins sit on Canvas, strip marks sit on Panel, and the
// hairline rule stays on the pane-facing column because it is the boundary that
// survives a terminal with no background colour to give.
//
// Inside the band there is one spine: one glyph per session, always the same
// column, at a fixed interval, pinned to the top under the badge. A top-aligned
// single-column list at a fixed interval reads as a list; the old centred stack
// of digits, glyphs and inked cells at irregular intervals read as debris.
//
// Severity is inked exactly once, in the badge. A session wanting a human swaps
// its resting dot for its own glyph in its severity colour, which is a mark and
// not a second alarm; two saturated blocks on a three-column strip are
// decoration. Window-count digits are gone everywhere but the badge count: the
// hover tooltip already says "name · N terminals", and the digits were most of
// the mixed vocabulary that stopped the stack reading as one list.

// sidebarStripRowKind is what one line of the collapsed strip is.
type sidebarStripRowKind int

const (
	sidebarStripBadge sidebarStripRowKind = iota
	sidebarStripSession
	// sidebarStripMore is the tail mark standing in for the sessions a short
	// rail has no line left to draw.
	sidebarStripMore
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
	// SessionID is set on a session row, so a click can still address it.
	SessionID string
	// Label is what the hover tooltip says about this row, built here from the
	// same tree the cells were drawn from. Building it at draw time rather than
	// at hover time is what stops the label and the cell under it from ever
	// describing different frames.
	Label string
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

// sidebarStripPlan decides how many session marks the spine shows and at what
// interval. The blank row between marks is what makes them scan as a list, so a
// short rail gives it up before it gives up a mark: full spacing while it fits,
// then packed rows, then a tail mark owning up to the ones left undrawn.
func sidebarStripPlan(region, sessions int) (shown, interval int, more bool) {
	switch {
	case region <= 0 || sessions == 0:
		return 0, 1, false
	case 2*sessions-1 <= region:
		return sessions, 2, false
	case sessions <= region:
		return sessions, 1, false
	default:
		return region - 1, 1, true
	}
}

// sidebarStripLines draws the collapsed rail: a Panel band the full height of
// the rail, carrying the attention badge under a pad, the session spine
// top-pinned below it, and the expand toggle on the rail's last line but one.
func (m *OS) sidebarStripLines(sessions []sessiontree.Node, w, cw, height, topMargin, sidebarX, contentX0 int,
	pal overlay.Palette, edgeLeft bool,
) ([]string, int) {
	m.sidebarStripRows = m.sidebarStripRows[:0]

	badge := sidebarStripBadgeFor(sessions)
	badgeH := 0
	if badge.Count > 0 {
		badgeH = 1
	}
	toggleGlyph, canToggle := m.sidebarCollapseGlyph(sidebarVariantGlyph)
	toggleH := 0
	if canToggle {
		toggleH = 1
	}

	// The head is a pad, the badge, and a pad under it; the tail is the toggle
	// and a pad below it. A rail with no room for both plus a mark drops the
	// badge first and then the pads, because the spine is the only thing the
	// strip cannot say any other way.
	headH, tailH := 1+2*badgeH, toggleH+1
	switch {
	case height >= headH+tailH+1:
	case height >= toggleH+3:
		badgeH, headH, tailH = 0, 1, toggleH+1
	case height >= toggleH+1:
		badgeH, headH, tailH = 0, 0, toggleH
	default:
		badgeH, headH, tailH, toggleH = 0, 0, 0, 0
	}

	stackTop := headH
	shown, interval, more := sidebarStripPlan(height-headH-tailH, len(sessions))
	badgeY, moreY, toggleY := 1, stackTop+shown*interval, height-tailH

	hover := -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		hover = m.SidebarHoverY - topMargin
	}

	nav := make([]sidebarNavRow, 0, shown+1)
	lines := make([]string, 0, height)
	for i := range height {
		y := topMargin + i
		switch {
		case badgeH > 0 && i == badgeY:
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripBadge, Y: y, Label: sidebarTooltipBadgeLabel(badge),
			})
			lines = append(lines, m.sidebarStripBand(sidebarStripBadgeCell(badge, cw, pal), cw, edgeLeft, pal))
		case i >= stackTop && i < stackTop+shown*interval && (i-stackTop)%interval == 0:
			s := sessions[(i-stackTop)/interval]
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripSession, Y: y, SessionID: s.ID, Label: sidebarTooltipSessionLabel(s),
			})
			m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
				X0: sidebarX, X1: sidebarX + w,
				Y0: y, Y1: y + 1,
				Kind: sidebarRowSession, SessionID: s.ID, WindowIndex: -1,
			})
			nav = append(nav, sidebarNavRow{Kind: sidebarRowSession, SessionID: s.ID, WindowIndex: -1})
			dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
			lines = append(lines, m.sidebarStripBand(m.sidebarStripCell(s, cw, pal, i == hover, dragged), cw, edgeLeft, pal))
		case more && i == moreY:
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripMore, Y: y,
				Label: strconv.Itoa(len(sessions)-shown) + " more " + plural("session", len(sessions)-shown),
			})
			lines = append(lines, m.sidebarStripBand(sidebarStripMoreCell(cw, pal), cw, edgeLeft, pal))
		case toggleH > 0 && i == toggleY:
			// The glyph hugs the pane-facing column, the edge the pointer arrives
			// from, but the zone is both content cells: a one-cell target on a
			// three-cell rail is the only control the user has and it has to be
			// hittable without aiming.
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripToggle, Y: y, Label: "expand",
			})
			m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
				X0: contentX0, X1: contentX0 + cw,
				Y0: y, Y1: y + 1,
				Kind: sidebarRowCollapse, WindowIndex: -1,
			})
			nav = append(nav, sidebarNavRow{Kind: sidebarRowCollapse, WindowIndex: -1})
			lines = append(lines, m.sidebarStripBand(sidebarStripToggleCell(toggleGlyph, cw, edgeLeft, i == hover, pal), cw, edgeLeft, pal))
		default:
			lines = append(lines, m.sidebarStripBand("", cw, edgeLeft, pal))
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

// sidebarStripBand paints one line of the strip: its content cells and the
// hairline rule beside them, every cell of it on Panel. The band is the whole
// point of the collapsed rail. It measures 1.19:1 against Canvas, which makes it
// a ground rather than a message, and which is also why the rule stays: on a
// terminal that drops the fill the rule is the only edge left.
func (m *OS) sidebarStripBand(content string, cw int, edgeLeft bool, pal overlay.Palette) string {
	rule := theme.NotificationRule()
	if m.SidebarFocused {
		rule = pal.Accent
	}
	edge := lipgloss.NewStyle().Background(pal.Panel).Foreground(rule).Render(config.GetWindowBorderLeft())
	body := sidebarFit(content, cw, pal.Panel)
	if edgeLeft {
		return body + edge
	}
	return edge + body
}

// sidebarStripBadgeCell is the alarm: how many panes want a human anywhere and
// the worst state among them, knocked out of a cell inked in that severity. It
// is the strip's only filled cell and its only digit, which is what lets one
// glance answer "does anything want me" before reading anything else.
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
	return sidebarFit(sidebarStyle(bg, pal.Canvas).Bold(true).Render(count+glyph), cw, bg)
}

// sidebarStripMoreCell is the spine's tail when a short rail cannot draw every
// session: one muted mark on the spine's own column, so the list ends by saying
// it is cut rather than by stopping.
func sidebarStripMoreCell(cw int, pal overlay.Palette) string {
	mark := "⋮"
	if overlay.UseASCII() {
		mark = ":"
	}
	return sidebarFit(sidebarStyle(pal.Panel, nil).Render(" ")+
		sidebarStyle(pal.Panel, pal.FgMute).Render(mark), cw, pal.Panel)
}

// sidebarStripToggleCell draws the expand arrow against the pane-facing edge,
// which is the edge the pointer arrives from. It is measured from that edge
// inwards, so the two-cell ASCII form still lands against it.
func sidebarStripToggleCell(glyph string, cw int, edgeLeft, hovered bool, pal overlay.Palette) string {
	fg := pal.FgMute
	if hovered {
		fg = pal.Fg
	}
	x0 := max(cw-lipgloss.Width(glyph), 0)
	if !edgeLeft {
		x0 = 0
	}
	return sidebarFit(sidebarStyle(pal.Panel, nil).Render(strings.Repeat(" ", x0))+
		sidebarStyle(pal.Panel, fg).Render(glyph), cw, pal.Panel)
}

// sidebarStripCell is one session's two cells on the spine: the accent bar in
// column 0 when this is the session you are attached to, and its one mark in
// column 1.
//
// At rest the mark is a dim dot, and that is nearly always what the strip shows,
// which is the state worth designing for. A session wanting a human swaps the
// dot for its own state glyph in its severity colour; an unread finish keeps its
// square. Working does not mark at all: it is not an alarm, the panes already
// show it, and spending the spine on it was what made every row a different
// shape.
func (m *OS) sidebarStripCell(node sessiontree.Node, cw int, pal overlay.Palette, hovered, dragged bool) string {
	bg := color.Color(pal.Panel)
	if hovered || dragged {
		bg = pal.Surface
	}

	lead, leadFg := " ", color.Color(nil)
	if node.IsCurrent {
		lead, leadFg = "▎", pal.Accent
		if overlay.UseASCII() {
			lead = ">"
		}
	}

	mark, markFg := "·", color.Color(pal.FgDim)
	if overlay.UseASCII() {
		mark = "."
	}
	if config.SidebarShowGlyphs {
		switch {
		case sidebarAttention(node.AgentState):
			mark, markFg = agentStateIndicator(node.AgentState), sidebarSeverityColor(node.AgentState, pal)
		case node.AgentState == "done" && !node.DoneSeen:
			mark, markFg = agentStateIndicator(node.AgentState), pal.Success
		}
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+sidebarStyle(bg, markFg).Render(mark), cw, bg)
}
