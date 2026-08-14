package app

import (
	"image/color"
	"sort"
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
// Under the spine sits the second list: the agents group, pinned to the bottom
// above the controls and fenced off by a drawn rule. It is the same interval and
// the same two columns as the spine, because one interval is what makes either
// of them scan; what tells them apart is the rule, the anchor, and that every
// mark in the group is a state glyph while the spine at rest is all dots. It
// lists panes, the spine lists sessions, and with nothing running the group is
// absent entirely rather than standing empty.
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
	// sidebarStripAgent is a row of the bottom group: one pane with something to
	// say about itself.
	sidebarStripAgent
	// sidebarStripMore is the tail mark standing in for the rows a short rail has
	// no line left to draw, in either list.
	sidebarStripMore
	// sidebarStripNew is the new-session control, and sidebarStripToggle the
	// expand arrow under it: the strip's two controls, stacked at the bottom.
	sidebarStripNew
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
	// SessionID and WindowID are what the slot addresses: a session row carries
	// the session, an agent row the pane inside it.
	SessionID string
	WindowID  string
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

// sidebarStripAgents is the queue the strip's bottom group lists: every pane
// with something to say about itself, worst first.
//
// It drops idle agents and finished ones already looked at, which the expanded
// section keeps. At two columns an idle agent draws the same quiet dot an idle
// session does, so keeping them would stand a permanent group under the spine
// saying nothing, which is the standing furniture this redesign spent a round
// removing. What is left is what the group is for: blocked, working, and
// finished but unread.
//
// The order is sidebarAgentPriority, the expanded section's own priority sort,
// so the folded rail cannot rank a pane differently from the open one. The
// section's filter is deliberately not applied: its control is not on the strip,
// and the badge pinned above the group counts every session, so a group hiding
// what the badge counts would contradict the cell above it.
func (m *OS) sidebarStripAgents(sessions []sessiontree.Node) []sidebarAgentEntry {
	all := m.sidebarAgents(sessions)
	kept := make([]sidebarAgentEntry, 0, len(all))
	for _, e := range all {
		if sidebarAgentPriority(e.State, e.DoneSeen) > 1 {
			kept = append(kept, e)
		}
	}
	sort.SliceStable(kept, func(a, b int) bool {
		return sidebarAgentPriority(kept[a].State, kept[a].DoneSeen) >
			sidebarAgentPriority(kept[b].State, kept[b].DoneSeen)
	})
	return kept
}

// sidebarStripSplit shares the strip's free rows between the session spine at
// the top and the agents group at the bottom, and says whether there is room for
// the rule between them.
//
// Neither list may starve the other, so past the point where both fit the group
// takes half and no more: the spine is where you are, the group is what wants
// you. A rail too short for a rule plus a mark keeps the spine alone, because a
// group of one line under a rule is a rule with a mark stuck to it.
func sidebarStripSplit(region, sessions, agents int) (spine, group, rule int) {
	if agents == 0 || region < 4 {
		return max(region, 0), 0, 0
	}
	free := region - 1
	// Each mark owns its interval, trailing blank included: the group's last
	// blank is what holds it off the controls under it.
	wantSpine, wantGroup := max(2*sessions-1, 0), 2*agents
	if wantSpine+wantGroup <= free {
		return free - wantGroup, wantGroup, 1
	}
	group = min(wantGroup, max(free/2, 1))
	return free - group, group, 1
}

// stripSlot is the slot row i belongs to in a list drawn from top at a fixed
// interval: the whole interval, because that is the block the marks make the eye
// read as one row.
func stripSlot(i, top, end, interval int) (y0, y1 int, ok bool) {
	if interval <= 0 || i < top || i >= end {
		return 0, 0, false
	}
	y0 = top + (i-top)/interval*interval
	return y0, min(y0+interval, end), true
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
// top-pinned below it, the agents group pinned to the bottom under its rule, and
// the expand toggle on the rail's last line but one.
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

	newH := 0
	if m.SidebarCanCreateSession() {
		newH = 1
	}

	// The head is a pad, the badge, and a pad under it; the tail is the two
	// controls and a pad below them. A rail with no room for all of it plus a mark
	// gives up the new-session control first, then the badge, then the pads: the
	// spine is the only thing the strip cannot say any other way, the way out has
	// to survive everything, and making a session is the one thing that can wait
	// for the rail to be reopened.
	headH, tailH := 1+2*badgeH, toggleH+newH+1
	switch {
	case height >= headH+tailH+1:
	case height >= headH+toggleH+2:
		newH, tailH = 0, toggleH+1
	case height >= toggleH+3:
		badgeH, headH, newH, tailH = 0, 1, 0, toggleH+1
	case height >= toggleH+1:
		badgeH, headH, newH, tailH = 0, 0, 0, toggleH
	default:
		badgeH, headH, newH, tailH, toggleH = 0, 0, 0, 0, 0
	}

	agents := m.sidebarStripAgents(sessions)
	region := max(height-headH-tailH, 0)
	spineRegion, groupRegion, ruleH := sidebarStripSplit(region, len(sessions), len(agents))

	stackTop := headH
	shown, interval, more := sidebarStripPlan(spineRegion, len(sessions))
	// Each mark owns its interval, trailing blank included, because that is what
	// the eye reads as its row. The span is clamped to the region so the last
	// slot cannot claim a line the group or the toggle below it is standing on.
	spineEnd := stackTop + min(shown*interval, spineRegion)

	// The group is pinned to the bottom, above the controls, exactly as the
	// expanded rail pins its agents section: the alarm keeps one screen position
	// whatever the rail is carrying above it, and the slack rides between them.
	shownA, intervalA, moreA := sidebarStripPlan(groupRegion, len(agents))
	groupRows := shownA * intervalA
	if moreA {
		groupRows++
	}
	groupTop := height - tailH - groupRows
	groupEnd := groupTop + shownA*intervalA
	ruleY, moreAY := groupTop-1, groupEnd
	if groupRows == 0 {
		ruleH = 0
	}

	badgeY, moreY := 1, stackTop+shown*interval
	newY := height - tailH
	toggleY := newY + newH

	// The band is the target made visible, so it covers the slot the pointer is
	// in and exactly the slot: every row of it, including the blank the mark's
	// interval owns, and every column of it, the edge rule included. A band
	// narrower than the rectangle it stands for teaches the wrong edges, which is
	// worse than either half of the mismatch alone.
	//
	// The rows nothing is recorded on take no band at all. The pads, the slack
	// between the two lists and the group's rule are furniture; painting them
	// offers a target that is not there.
	hoverY0, hoverY1 := -1, -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		hy := m.SidebarHoverY - topMargin
		switch {
		case badgeH > 0 && hy == badgeY,
			more && hy == moreY,
			moreA && hy == moreAY,
			newH > 0 && hy == newY,
			toggleH > 0 && hy == toggleY:
			hoverY0, hoverY1 = hy, hy+1
		default:
			if y0, y1, ok := stripSlot(hy, stackTop, spineEnd, interval); ok {
				hoverY0, hoverY1 = y0, y1
			}
			if y0, y1, ok := stripSlot(hy, groupTop, groupEnd, intervalA); ok {
				hoverY0, hoverY1 = y0, y1
			}
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
			// Hovered, the alarm's own ink is the band, so the hairline beside it
			// takes the badge's knockout: a rule mixed for Panel does not show on a
			// saturated fill, and the rail's frame may not break for one row.
			bg, edgeFg := stripRowBg(false, pal), color.Color(nil)
			if hovered(i) {
				bg, edgeFg = agentGlyphColor(badge.State, pal), pal.Canvas
			}
			lines = append(lines, m.sidebarStripBand(sidebarStripBadgeCell(badge, cw, pal), cw, edgeLeft, bg, edgeFg, pal))
		case i >= stackTop && i < spineEnd && (i-stackTop)%interval == 0:
			s := sessions[(i-stackTop)/interval]
			rows := min(interval, spineEnd-i)
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripSession, Y0: y, Y1: y + rows, SessionID: s.ID, Label: sidebarTooltipSessionLabel(s),
			})
			record(sidebarRowSession, s.ID, "", y, rows)
			dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
			lit := hovered(i) || dragged
			bg := stripRowBg(lit, pal)
			lines = append(lines, m.sidebarStripBand(m.sidebarStripCell(s, cw, pal, bg, lit), cw, edgeLeft, bg, nil, pal))
		case more && i == moreY:
			// The tail names what it cut, and expanding is the only way to see it,
			// so that is what a click on it does.
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripMore, Y0: y, Y1: y + 1,
				Label: strconv.Itoa(len(sessions)-shown) + " more " + plural("session", len(sessions)-shown),
			})
			record(sidebarRowCollapse, "", "", y, 1)
			bg := stripRowBg(hovered(i), pal)
			lines = append(lines, m.sidebarStripBand(sidebarStripMoreCell(cw, pal, bg, hovered(i)), cw, edgeLeft, bg, nil, pal))
		case ruleH > 0 && i == ruleY:
			// The groups are separated by a drawn rule rather than by a blank row,
			// because a blank row is already the spine's own rhythm: spending it
			// here would read as one more session, not as a boundary. Nothing is
			// recorded on it, so it never takes a band.
			lines = append(lines, m.sidebarStripBand(sidebarStripRuleCell(cw, pal), cw, edgeLeft, pal.Panel, nil, pal))
		case i >= groupTop && i < groupEnd && (i-groupTop)%intervalA == 0:
			e := agents[(i-groupTop)/intervalA]
			rows := min(intervalA, groupEnd-i)
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripAgent, Y0: y, Y1: y + rows,
				SessionID: e.SessionID, WindowID: e.WindowID,
				Label: sidebarTooltipAgentLabel(e),
			})
			record(sidebarRowAgent, e.SessionID, e.WindowID, y, rows)
			bg := stripRowBg(hovered(i), pal)
			lines = append(lines, m.sidebarStripBand(m.sidebarStripAgentCell(e, cw, pal, bg, hovered(i)), cw, edgeLeft, bg, nil, pal))
		case moreA && i == moreAY:
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripMore, Y0: y, Y1: y + 1,
				Label: strconv.Itoa(len(agents)-shownA) + " more " + plural("agent", len(agents)-shownA),
			})
			record(sidebarRowCollapse, "", "", y, 1)
			bg := stripRowBg(hovered(i), pal)
			lines = append(lines, m.sidebarStripBand(sidebarStripMoreCell(cw, pal, bg, hovered(i)), cw, edgeLeft, bg, nil, pal))
		case newH > 0 && i == newY:
			// The two controls stack rather than share a line: two content cells
			// shared out give each of them one cell, which is the target this round
			// exists to stop drawing, and the ASCII toggle is two cells wide and
			// would leave no room at all. Stacked, they also sit in the column every
			// other mark on the strip is already in.
			//
			// This is the sessions add and only that, the same thing the expanded
			// rail's sessions header means by the same glyph: a control that means
			// one thing folded and another unfolded is its own bug. The terminals
			// add has no counterpart here on purpose. The strip lists sessions and
			// the panes wanting a human; it has no terminals section, so a control
			// making a pane would point at a list that is not on the screen, and
			// the two "+" marks would then be telling the user the width decides
			// what the key means.
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripNew, Y0: y, Y1: y + 1, Label: sidebarAddWords(sidebarRowNewSession),
			})
			record(sidebarRowNewSession, "", "", y, 1)
			bg := stripRowBg(hovered(i), pal)
			lines = append(lines, m.sidebarStripBand(
				sidebarStripControlCell(sidebarAddGlyph, cw, edgeLeft, hovered(i), bg, pal), cw, edgeLeft, bg, nil, pal))
		case toggleH > 0 && i == toggleY:
			// The glyph hugs the pane-facing column, the edge the pointer arrives
			// from, but the zone is the whole band: the only control the user has
			// has to be hittable without aiming.
			m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
				Kind: sidebarStripToggle, Y0: y, Y1: y + 1, Label: "expand",
			})
			record(sidebarRowCollapse, "", "", y, 1)
			bg := stripRowBg(hovered(i), pal)
			lines = append(lines, m.sidebarStripBand(sidebarStripControlCell(toggleGlyph, cw, edgeLeft, hovered(i), bg, pal), cw, edgeLeft, bg, nil, pal))
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

// stripRowBg is the ground one line of the strip stands on: the hover band
// where the pointer is, and the strip's own Panel everywhere else.
func stripRowBg(lit bool, pal overlay.Palette) color.Color {
	if lit {
		return pal.Surface
	}
	return pal.Panel
}

// sidebarStripBand paints one line of the strip: its content cells and the
// hairline rule beside them, every cell of it on the line's own ground. The band
// is the whole point of the collapsed rail. At rest it measures 1.19:1 against
// Canvas, which makes it a ground rather than a message, and which is also why
// the rule stays: on a terminal that drops the fill the rule is the only edge
// left.
//
// The rule shares the line's ground rather than keeping Panel under it, so a
// hover band is one unbroken rectangle three columns wide. It used to stop a
// column short of the rectangle it was standing for, on the pane-facing side the
// pointer arrives from. edgeFg overrides the hairline's colour for a line whose
// ground is inked, where a hairline mixed for Panel would not show at all.
func (m *OS) sidebarStripBand(content string, cw int, edgeLeft bool, bg, edgeFg color.Color, pal overlay.Palette) string {
	rule := edgeFg
	switch {
	case rule != nil:
	case m.SidebarFocused:
		rule = pal.Accent
	default:
		rule = theme.NotificationRule()
	}
	edge := lipgloss.NewStyle().Background(bg).Foreground(rule).Render(config.GetWindowBorderLeft())
	body := sidebarFit(content, cw, bg)
	if edgeLeft {
		return body + edge
	}
	return edge + body
}

// sidebarStripBlank is a band line with no mark on it: a pad, the spacer inside
// a slot, or the slack. It takes the hover fill with the rest of its slot, which
// is what draws the target's real edges.
func (m *OS) sidebarStripBlank(cw int, edgeLeft, hovered bool, pal overlay.Palette) string {
	bg := stripRowBg(hovered, pal)
	return m.sidebarStripBand(sidebarFit("", cw, bg), cw, edgeLeft, bg, nil, pal)
}

// sidebarStripRuleCell is the boundary above the agents group: a dim rule across
// both content cells. It is the one piece of furniture the strip draws, and it
// earns the line because it is what stops the group's marks reading as more
// sessions. It is a glyph, not a fill, so it survives a terminal that drops the
// band's own ground.
func sidebarStripRuleCell(cw int, pal overlay.Palette) string {
	mark := "─"
	if overlay.UseASCII() {
		mark = "-"
	}
	return sidebarFit(sidebarStyle(pal.Panel, theme.NotificationRule()).Render(strings.Repeat(mark, cw)), cw, pal.Panel)
}

// sidebarStripAgentCell is one pane of the bottom group in two cells: the gutter
// marks the pane you are looking at, the spine column carries the pane's state
// glyph in the colour the rest of the rail draws it in.
//
// The gutter says "current" here and severity in the expanded section, which
// looks like a divergence and is not: the expanded rail marks the focused pane
// in its terminals section, and the strip has no terminals section, so the mark
// has nowhere else to live. Severity in the gutter would also ink the same state
// twice in a two-cell row, which is the double-inking the redesign removed.
func (m *OS) sidebarStripAgentCell(e sidebarAgentEntry, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if e.WindowIndex >= 0 && e.WindowIndex == m.FocusedWindow {
		lead, leadFg = "▎", railFocusTint(m.agentIdentityTint(e, bg), pal)
		if overlay.UseASCII() {
			lead = ">"
		}
	}

	mark, markFg := "·", stripRestingInk(lit, pal)
	if overlay.UseASCII() {
		mark = "."
	}
	if g := agentStateIndicator(e.State); g != "" && config.SidebarShowGlyphs {
		mark, markFg = g, sidebarStateColor(e.State, e.DoneSeen, pal)
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+sidebarStyle(bg, markFg).Render(mark), cw, bg)
}

// stripRestingInk is the colour a mark saying nothing in particular is drawn in.
// Under the pointer it comes up to full strength, which is the same thing
// hovering a row does on the expanded rail, so the pointer means one thing at
// both widths. A mark already carrying a state keeps its state colour: the
// pointer may not repaint an alarm, and a strip busy enough to have one is
// exactly when the band has to stay readable as a band.
func stripRestingInk(lit bool, pal overlay.Palette) color.Color {
	if lit {
		return pal.Fg
	}
	return pal.FgDim
}

// sidebarStripBadgeCell is the alarm: how many panes want a human anywhere and
// the worst state among them, knocked out of a cell inked in that severity. It
// is the strip's only filled cell and its only digit, which is what lets one
// glance answer "does anything want me" before reading anything else.
//
// It is the one target whose band is not the hover ground: under the pointer the
// alarm's own ink runs the width of the band instead, so touching it makes it
// louder rather than laying a quiet slab over the loudest thing on the rail.
// That keeps severity inked exactly once and adds no emphasis the strip did not
// already have.
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
func sidebarStripMoreCell(cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	mark := "⋮"
	if overlay.UseASCII() {
		mark = ":"
	}
	fg := color.Color(pal.FgMute)
	if lit {
		fg = pal.Fg
	}
	return sidebarFit(sidebarStyle(bg, nil).Render(" ")+
		sidebarStyle(bg, fg).Render(mark), cw, bg)
}

// sidebarStripControlCell draws one of the strip's two controls against the
// pane-facing edge, which is the edge the pointer arrives from and the column
// every other mark on the strip already sits in. It is measured from that edge
// inwards, so the two-cell ASCII form still lands against it.
func sidebarStripControlCell(glyph string, cw int, edgeLeft, lit bool, bg color.Color, pal overlay.Palette) string {
	fg := color.Color(pal.FgMute)
	if lit {
		fg = pal.Fg
	}
	x0 := max(cw-lipgloss.Width(glyph), 0)
	if !edgeLeft {
		x0 = 0
	}
	return sidebarFit(sidebarStyle(bg, nil).Render(strings.Repeat(" ", x0))+
		sidebarStyle(bg, fg).Render(glyph), cw, bg)
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
func (m *OS) sidebarStripCell(node sessiontree.Node, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if node.IsCurrent {
		lead, leadFg = "▎", railFocusTint(m.sessionTint(node.ID, bg), pal)
		if overlay.UseASCII() {
			lead = ">"
		}
	}

	mark, markFg := "·", stripRestingInk(lit, pal)
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
