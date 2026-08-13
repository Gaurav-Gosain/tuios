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

// sidebarStripRow is one drawn slot of the collapsed strip, recorded by the
// renderer as it draws. Every slot is also a hit rectangle; this list exists
// alongside them because the tooltip has to name what is under the pointer in
// words, which a rectangle does not carry.
type sidebarStripRow struct {
	Kind sidebarStripRowKind
	// Y0 and Y1 are the absolute screen rows the slot owns, which is every row
	// up to the next mark rather than the one the glyph sits on: the strip draws
	// at a fixed interval, so the interval is what the eye reads as the row.
	Y0, Y1 int
	// SessionID is set on a session row, so a click can still address it.
	SessionID string
	// Label is what the hover tooltip says about this row, built here from the
	// same tree the cells were drawn from. Building it at draw time rather than
	// at hover time is what stops the label and the cell under it from ever
	// describing different frames.
	Label string
}

// contains reports whether absolute screen row y falls in this slot.
func (r sidebarStripRow) contains(y int) bool { return y >= r.Y0 && y < r.Y1 }

// sidebarStripBadgeInfo is the alarm block at the strip's top: how many panes
// want a human anywhere, the worst state among them, and which pane that is.
type sidebarStripBadgeInfo struct {
	Count int
	State string
	// SessionID and WindowID address the pane the badge is counting from, so a
	// click on the alarm goes to what is alarming. The badge used to be a pure
	// readout, which made the strip's largest, loudest object the one thing on it
	// that did nothing.
	SessionID string
	WindowID  string
}

// sidebarStripBadgeFor counts the panes wanting a human across every session
// and picks the loudest state among them. Zero count means no badge at all:
// an alarm that is always on the screen is not an alarm. Ties go to the first
// in rail order, so the badge points where the spine below it does.
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
				info.SessionID, info.WindowID = s.ID, win.ID
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
func (m *OS) sidebarStripLines(sessions []sessiontree.Node, w, cw, height, topMargin, sidebarX int,
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
	region := max(height-headH-tailH, 0)
	shown, interval, more := sidebarStripPlan(region, len(sessions))
	// Each mark owns its interval, trailing blank included, because that is what
	// the eye reads as its row. The span is clamped to the region so the last
	// slot cannot claim a line the toggle or the slack below it is standing on.
	spineEnd := stackTop + min(shown*interval, region)
	badgeY, moreY, toggleY := 1, stackTop+shown*interval, height-tailH

	// The pointer highlights the whole slot it is in, not the one line the mark
	// sits on: the highlight is the target made visible, and a target you cannot
	// see the edges of is a target you have to aim at.
	hoverY0, hoverY1 := -1, -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		hoverY0 = m.SidebarHoverY - topMargin
		hoverY1 = hoverY0 + 1
		if hoverY0 >= stackTop && hoverY0 < spineEnd {
			hoverY0 = stackTop + (hoverY0-stackTop)/interval*interval
			hoverY1 = min(hoverY0+interval, spineEnd)
		}
	}
	hovered := func(i int) bool { return i >= hoverY0 && i < hoverY1 }

	nav := make([]sidebarNavRow, 0, shown+3)
	// record claims a slot for a target: the whole band width, including the edge
	// rule (a third of this rail's columns), and every row of the slot. The
	// mismatch this replaces was a one-cell-tall rectangle under a two-row mark,
	// which asked the user to hit half the object they could see.
	record := func(kind sidebarRowKind, sessionID, windowID string, y, rows int) {
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + rows,
			Kind: kind, SessionID: sessionID, WindowID: windowID, WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: kind, SessionID: sessionID, WindowID: windowID, WindowIndex: -1})
	}

	lines := make([]string, 0, height)
	for i := range height {
		y := topMargin + i
		switch {
		case badgeH > 0 && i == badgeY:
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripBadge, Y0: y, Y1: y + 1, Label: sidebarTooltipBadgeLabel(badge),
			})
			record(sidebarRowAgent, badge.SessionID, badge.WindowID, y, 1)
			lines = append(lines, m.sidebarStripBand(sidebarStripBadgeCell(badge, cw, pal), cw, edgeLeft, pal))
		case i >= stackTop && i < spineEnd && (i-stackTop)%interval == 0:
			s := sessions[(i-stackTop)/interval]
			rows := min(interval, spineEnd-i)
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripSession, Y0: y, Y1: y + rows, SessionID: s.ID, Label: sidebarTooltipSessionLabel(s),
			})
			record(sidebarRowSession, s.ID, "", y, rows)
			dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
			lines = append(lines, m.sidebarStripBand(m.sidebarStripCell(s, cw, pal, hovered(i), dragged), cw, edgeLeft, pal))
		case more && i == moreY:
			// The tail names what it cut, and expanding is the only way to see it,
			// so that is what a click on it does.
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripMore, Y0: y, Y1: y + 1,
				Label: strconv.Itoa(len(sessions)-shown) + " more " + plural("session", len(sessions)-shown),
			})
			record(sidebarRowCollapse, "", "", y, 1)
			lines = append(lines, m.sidebarStripBand(sidebarStripMoreCell(cw, pal), cw, edgeLeft, pal))
		case toggleH > 0 && i == toggleY:
			// The glyph hugs the pane-facing column, the edge the pointer arrives
			// from, but the zone is the whole band: the only control the user has
			// has to be hittable without aiming.
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripToggle, Y0: y, Y1: y + 1, Label: "expand",
			})
			record(sidebarRowCollapse, "", "", y, 1)
			lines = append(lines, m.sidebarStripBand(sidebarStripToggleCell(toggleGlyph, cw, edgeLeft, hovered(i), pal), cw, edgeLeft, pal))
		default:
			lines = append(lines, m.sidebarStripBlank(cw, edgeLeft, hovered(i), pal))
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

// sidebarStripBlank is a band line with no mark on it: a pad, the spacer inside
// a slot, or the slack. It takes the hover fill with the rest of its slot, which
// is what draws the target's real edges.
func (m *OS) sidebarStripBlank(cw int, edgeLeft, hovered bool, pal overlay.Palette) string {
	bg := color.Color(pal.Panel)
	if hovered {
		bg = pal.Surface
	}
	return m.sidebarStripBand(sidebarFit("", cw, bg), cw, edgeLeft, pal)
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
