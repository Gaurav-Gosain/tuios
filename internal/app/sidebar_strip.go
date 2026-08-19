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

// The collapsed rail is three columns and it is the state the rail spends most
// of its life in, so what it carries has to be worth those columns without a
// pointer. It carries the same lists the open rail does, in the same order:
// sessions, then the terminals of the session being shown, then the panes with
// something to say. Folding is a change of width, and a rail that dropped a
// whole list when it folded was a different object at each width, which is what
// made the folded one read as broken.
//
// The terminals list is the one that used to be missing. It was left out on the
// argument that the strip lists sessions across the daemon while terminals are
// a within-session list, so the two could not share a column. That argument was
// about grammar and the open rail already answers it: it stacks the same
// daemon-wide list over the same session-scoped one and tells them apart with a
// header. The strip now does the same thing with the same header, cut to the
// one column it has: "sessions" becomes s, "terminals" becomes t, "agents"
// becomes a, each with its section's own add control beside it. What was kept
// from the old reasoning is the rest of it: no digits outside the badge, one
// mark vocabulary across every list, and severity inked once.
//
// The void is gone because nothing is spread any more. The lists are one
// contiguous stack under the badge at a one-row interval, and the empty rail
// below them is a list that ended rather than a list with holes in it. The
// agents group used to be pinned to the bottom so the alarm held a screen
// position; the badge is what holds that position now, and it always did.
//
// A pane wanting a human is therefore said three times over, each in its own
// voice: the badge counts it and goes to the worst of them, the session
// carrying it swaps its resting dot for that state's glyph, and the pane itself
// carries the glyph in the terminals list when it is here and in the agents
// list wherever it is.

// sidebarStripRowKind is what one line of the collapsed strip is.
type sidebarStripRowKind int

const (
	sidebarStripBadge sidebarStripRowKind = iota
	// sidebarStripHeader is a group's own line: the open rail's section label
	// cut to one column, and that section's add control.
	sidebarStripHeader
	sidebarStripSession
	// sidebarStripTerminal is a row of the middle group: one pane of the session
	// the strip is showing.
	sidebarStripTerminal
	// sidebarStripAgent is a row of the last group: one pane with something to
	// say about itself, wherever it lives.
	sidebarStripAgent
	// sidebarStripMore is the tail mark standing in for the rows a short rail has
	// no line left to draw, in any of the lists.
	sidebarStripMore
	// sidebarStripToggle is the expand arrow on the rail's last line but one.
	sidebarStripToggle
)

// sidebarStripRow is one drawn slot of the collapsed strip, recorded by the
// renderer as it draws. Every slot is also a hit rectangle; this list exists
// alongside them because the tooltip has to name what is under the pointer in
// words, which a rectangle does not carry.
type sidebarStripRow struct {
	Kind sidebarStripRowKind
	// Y0 and Y1 are the absolute screen rows the slot owns. The strip draws one
	// row per item, so a slot is one row.
	Y0, Y1 int
	// SessionID and WindowID are what the slot addresses: a session row carries
	// the session, a terminal or agent row the pane inside it.
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
// in rail order, so the badge points where the stack below it does.
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

// sidebarStripAgents is the queue the strip's last group lists: every pane with
// something to say about itself that the terminals group above it is not
// already showing, worst first.
//
// It drops idle agents and finished ones already looked at, which the expanded
// section keeps. At two columns an idle agent draws the same quiet dot an idle
// session does, so keeping them would stand a permanent group under the others
// saying nothing. What is left is what the group is for: blocked, working, and
// finished but unread.
//
// It also drops the panes of the session whose terminals are on the screen,
// which the expanded section does not. That section can afford the overlap
// because it names every row; here the same pane would draw the same nameless
// glyph twice and read as two panes in trouble. What is left is the group's own
// question, which nothing else on the strip answers: what wants a human
// somewhere the strip is not showing.
//
// The order is sidebarAgentPriority, the expanded section's own priority sort,
// so the folded rail cannot rank a pane differently from the open one. The
// section's all/here filter is deliberately not applied: its control is not on
// the strip.
func (m *OS) sidebarStripAgents(sessions []sessiontree.Node, listed string) []sidebarAgentEntry {
	all := m.sidebarAgents(sessions)
	kept := make([]sidebarAgentEntry, 0, len(all))
	for _, e := range all {
		if listed != "" && e.SessionID == listed {
			continue
		}
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

// sidebarStripGroup is one of the strip's lists, and after placement it is also
// where every row of that list landed.
type sidebarStripGroup struct {
	kind sidebarStripRowKind
	// noun is what one item is called. Its first letter is the header, which is
	// how the strip's header and the open rail's stay one label: the open rail
	// writes the plural in full, the strip has one column and writes its first.
	noun string
	// add is the section's own add control and addSession what it would make a
	// pane in, drawn only when hasAdd. The agents group has none: an agent is a
	// pane running an agent CLI, so a "+" here would be a second name for
	// new-terminal.
	add        sidebarRowKind
	addSession string
	hasAdd     bool
	// total is how many items the list holds, whatever the region can draw.
	total int

	// header is the row the group's own line sits on, or -1 when the rail is too
	// short for one. top and end bracket its marks, and moreY is the tail mark
	// owning up to the items with no line left, or -1.
	header, top, end, moreY int
}

// shown is how many of the group's items got a line.
func (g sidebarStripGroup) shown() int { return g.end - g.top }

// sidebarStripFloor is the fewest rows a group is worth drawing at all: its
// header, one mark, and the tail mark that owns up to the rest. A group given
// less than that is a header over a cut, which says less than leaving the group
// out and saying it in the badge.
func sidebarStripFloor(g sidebarStripGroup) int { return 1 + min(g.total, 2) }

// sidebarStripPlan is how many of a list's items fit in the rows it was given,
// and whether a tail mark has to own up to the rest. A list that is cut ends by
// saying so rather than by stopping.
func sidebarStripPlan(rows, total int) (shown int, more bool) {
	switch {
	case rows <= 0 || total == 0:
		return 0, false
	case total <= rows:
		return total, false
	default:
		return rows - 1, true
	}
}

// sidebarStripPlace lays the groups into region rows starting at top, one
// contiguous stack with no slack between them, and returns the ones that got a
// place.
//
// Groups leave from the bottom when the region cannot carry them all, which is
// the order the expanded rail gives its own sections up in and for the same
// reason: what the agents group says is also in the badge, and what the
// terminals group says is also one click away, while the sessions list is the
// only thing the strip cannot say any other way. That is also why the last
// group standing gives up its header before its marks.
func sidebarStripPlace(groups []sidebarStripGroup, top, region int) []sidebarStripGroup {
	// Sized to the three lists the strip can hold, so a relayout allocates
	// nothing.
	var alloc [3]int
	groups = groups[:min(len(groups), len(alloc))]

	n := len(groups)
	for n > 0 {
		need := 0
		for _, g := range groups[:n] {
			need += sidebarStripFloor(g)
		}
		if need <= region {
			break
		}
		n--
	}
	headers := true
	if n == 0 {
		if region <= 0 || len(groups) == 0 {
			return groups[:0]
		}
		n, headers = 1, false
	}
	groups = groups[:n]

	free := region
	if headers {
		free -= n
	}
	// Every group starts out asking for all of it, and the one furthest above
	// its floor gives a row back until the stack fits. Shrinking the longest
	// list rather than the last one is what stops forty sessions from crowding
	// out the three panes beside them.
	sum := 0
	for i, g := range groups {
		alloc[i] = g.total
		sum += alloc[i]
	}
	for sum > free {
		pick, slack := -1, 0
		for i, g := range groups {
			if s := alloc[i] - min(g.total, 2); s >= slack && s > 0 {
				pick, slack = i, s
			}
		}
		if pick < 0 {
			break
		}
		alloc[pick]--
		sum--
	}
	if !headers {
		alloc[0] = min(free, groups[0].total)
	}

	y := top
	for i := range groups {
		groups[i].header = -1
		if headers {
			groups[i].header = y
			y++
		}
		shown, more := sidebarStripPlan(alloc[i], groups[i].total)
		groups[i].top, groups[i].end = y, y+shown
		y += shown
		groups[i].moreY = -1
		if more {
			groups[i].moreY = y
			y++
		}
	}
	return groups
}

// stripPart is what one row of the strip belongs to inside a group.
type stripPart int

const (
	stripPartNone stripPart = iota
	stripPartHeader
	stripPartMark
	stripPartMore
)

// stripAt says which group row i belongs to and which of its parts, plus the
// item index for a mark. One lookup for the draw and the hover both, so the two
// can never disagree about what a row is.
func stripAt(groups []sidebarStripGroup, i int) (int, stripPart, int) {
	for gi, g := range groups {
		switch {
		case g.header >= 0 && i == g.header:
			return gi, stripPartHeader, 0
		case i >= g.top && i < g.end:
			return gi, stripPartMark, i - g.top
		case g.moreY >= 0 && i == g.moreY:
			return gi, stripPartMore, 0
		}
	}
	return -1, stripPartNone, 0
}

// sidebarStripLines draws the collapsed rail: a Panel band the full height of
// the rail, carrying the attention badge under a pad, the three lists stacked
// contiguously under it, and the expand toggle on the rail's last line but one.
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

	// A pad above the badge and one below the toggle, and a rail with no room
	// for them gives them up first: they are spacing. Next to go is the alarm,
	// which the pane running the agent shows for itself. The way out survives
	// everything, because a fold with no way back is a trap.
	padTop, padBot := 1, 1
	switch {
	case height >= badgeH+toggleH+3:
	case height >= badgeH+toggleH+1:
		padTop, padBot = 0, 0
	default:
		padTop, padBot, badgeH = 0, 0, 0
	}
	if height < toggleH {
		toggleH = 0
	}
	headH, tailH := padTop+badgeH, toggleH+padBot
	badgeY, toggleY := padTop, height-tailH
	region := max(height-headH-tailH, 0)

	shownSession, peeking := m.sidebarShownSession(sessions)
	var terminals []sidebarTerminalEntry
	if config.SidebarShowWindows {
		terminals = m.sidebarTerminals(sessions, shownSession)
	}

	var scratch [3]sidebarStripGroup
	groups := scratch[:0]
	groups = append(groups, sidebarStripGroup{
		kind: sidebarStripSession, noun: "session", total: len(sessions),
		add: sidebarRowNewSession, hasAdd: m.SidebarCanCreateSession(),
	})
	// A peeked session with no panes keeps its header, or the group would vanish
	// and the strip would read as the attached session having none.
	listed := ""
	if config.SidebarShowWindows && (len(terminals) > 0 || peeking) {
		listed = shownSession
		groups = append(groups, sidebarStripGroup{
			kind: sidebarStripTerminal, noun: "terminal", total: len(terminals),
			add: sidebarRowNewWindow, addSession: shownSession, hasAdd: true,
		})
	}
	agents := m.sidebarStripAgents(sessions, listed)
	if len(agents) > 0 {
		groups = append(groups, sidebarStripGroup{
			kind: sidebarStripAgent, noun: "agent", total: len(agents),
		})
	}
	groups = sidebarStripPlace(groups, headH, region)

	// The band is the target made visible, so it covers the row the pointer is
	// in and exactly that row, every column of it including the edge rule. The
	// rows nothing is recorded on take no band at all: the pads, the group
	// headers with no control on them and the empty rail under the stack are
	// furniture, and painting them offers a target that is not there.
	hoverY := -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		hoverY = m.SidebarHoverY - topMargin
	}

	nav := make([]sidebarNavRow, 0, region+3)
	// record claims a row for a target: the whole band width, including the edge
	// rule (a third of this rail's columns). The mismatch this replaces was a
	// one-cell-tall rectangle under a two-row mark, which asked the user to hit
	// half the object they could see.
	record := func(kind sidebarRowKind, sessionID, windowID string, y int) {
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + 1,
			Kind: kind, SessionID: sessionID, WindowID: windowID, WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: kind, SessionID: sessionID, WindowID: windowID, WindowIndex: -1})
	}
	note := func(kind sidebarStripRowKind, sessionID, windowID, label string, y int) {
		m.sidebarStripRows = append(m.sidebarStripRows, sidebarStripRow{
			Kind: kind, Y0: y, Y1: y + 1,
			SessionID: sessionID, WindowID: windowID, Label: label,
		})
	}

	lines := make([]string, 0, height)
	for i := range height {
		y := topMargin + i
		lit := i == hoverY
		switch {
		case badgeH > 0 && i == badgeY:
			note(sidebarStripBadge, badge.SessionID, badge.WindowID, sidebarTooltipBadgeLabel(badge), y)
			record(sidebarRowAgent, badge.SessionID, badge.WindowID, y)
			// Hovered, the alarm's own ink is the band, so the hairline beside it
			// takes the badge's knockout: a rule mixed for Panel does not show on a
			// saturated fill, and the rail's frame may not break for one row.
			bg, edgeFg := stripRowBg(false, pal), color.Color(nil)
			if lit {
				bg, edgeFg = agentGlyphColor(badge.State, pal), pal.Canvas
			}
			lines = append(lines, m.sidebarStripBand(sidebarStripBadgeCell(badge, cw, pal), cw, edgeLeft, bg, edgeFg, pal))
		case toggleH > 0 && i == toggleY:
			// The glyph hugs the pane-facing column, the edge the pointer arrives
			// from, but the zone is the whole band: the way out has to be hittable
			// without aiming.
			note(sidebarStripToggle, "", "", "expand", y)
			record(sidebarRowCollapse, "", "", y)
			bg := stripRowBg(lit, pal)
			lines = append(lines, m.sidebarStripBand(sidebarStripControlCell(toggleGlyph, cw, edgeLeft, lit, bg, pal), cw, edgeLeft, bg, nil, pal))
		default:
			gi, part, idx := stripAt(groups, i)
			if part == stripPartNone {
				lines = append(lines, m.sidebarStripBlank(cw, edgeLeft, pal))
				continue
			}
			g := groups[gi]
			switch part {
			case stripPartHeader:
				add := ""
				if g.hasAdd {
					// The whole row is the control, which is why the label shares it:
					// a one-cell target on a three-column rail is not a control, and
					// naming the list the "+" adds to is what the open rail's header
					// does with the same glyph in the same place.
					add = sidebarAddGlyph
					note(sidebarStripHeader, g.addSession, "", sidebarAddWords(g.add), y)
					record(g.add, g.addSession, "", y)
				} else {
					// A header with no control is a label, so it takes no band.
					note(sidebarStripHeader, "", "", plural(g.noun, 2), y)
					lit = false
				}
				bg := stripRowBg(lit, pal)
				lines = append(lines, m.sidebarStripBand(
					sidebarStripHeaderCell(g.noun, add, cw, lit, bg, pal), cw, edgeLeft, bg, nil, pal))
			case stripPartMark:
				switch g.kind {
				case sidebarStripSession:
					s := sessions[idx]
					note(sidebarStripSession, s.ID, "", sidebarTooltipSessionLabel(s), y)
					record(sidebarRowSession, s.ID, "", y)
					// The row being dragged wears the band wherever it has been
					// dropped to, so the draft order reads as the live one.
					lit = lit || (m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID)
					bg := stripRowBg(lit, pal)
					lines = append(lines, m.sidebarStripBand(m.sidebarStripCell(s, cw, pal, bg, lit), cw, edgeLeft, bg, nil, pal))
				case sidebarStripTerminal:
					e := terminals[idx]
					note(sidebarStripTerminal, e.SessionID, e.WindowID, sidebarTooltipTerminalLabel(e), y)
					record(sidebarRowWindow, e.SessionID, e.WindowID, y)
					bg := stripRowBg(lit, pal)
					lines = append(lines, m.sidebarStripBand(m.sidebarStripTerminalCell(e, peeking, cw, pal, bg, lit), cw, edgeLeft, bg, nil, pal))
				default:
					e := agents[idx]
					note(sidebarStripAgent, e.SessionID, e.WindowID, sidebarTooltipAgentLabel(e), y)
					record(sidebarRowAgent, e.SessionID, e.WindowID, y)
					bg := stripRowBg(lit, pal)
					lines = append(lines, m.sidebarStripBand(m.sidebarStripAgentCell(e, cw, pal, bg, lit), cw, edgeLeft, bg, nil, pal))
				}
			case stripPartMore:
				// The tail names what it cut, and expanding is the only way to see
				// it, so that is what a click on it does.
				hidden := g.total - g.shown()
				note(sidebarStripMore, "", "", strconv.Itoa(hidden)+" more "+plural(g.noun, hidden), y)
				record(sidebarRowCollapse, "", "", y)
				bg := stripRowBg(lit, pal)
				lines = append(lines, m.sidebarStripBand(sidebarStripMoreCell(cw, pal, bg, lit), cw, edgeLeft, bg, nil, pal))
			}
		}
	}

	// The strip windows its lists with a tail mark rather than a scroll, so a
	// wheel has nowhere to send one.
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
		// Measured against the band: the accent is the theme's and read 2.76:1
		// unlifted, on a rail whose whole job when focused is to look focused.
		rule = theme.ReadableAt(pal.Accent, bg, theme.MarkFloor)
	default:
		// FgMute, the token this codebase gives separators, rather than the
		// notification rule this used to borrow. That rule measured 1.06:1 on the
		// band, so the boundary the comment above calls "the only edge left" was
		// not on screen at all whenever the rail was not focused, which is nearly
		// always. FgMute is furniture, and furniture you can see.
		// The text floor deliberately does not apply here; a hairline held to
		// 4.5:1 would be louder than the marks it frames. It sits at 3.71:1
		// since the quiet tier moved a step up the ramp.
		rule = pal.FgMute
	}
	edge := lipgloss.NewStyle().Background(bg).Foreground(rule).Render(config.GetWindowBorderLeft())
	body := sidebarFit(content, cw, bg)
	if edgeLeft {
		return body + edge
	}
	return edge + body
}

// sidebarStripBlank is a band line with no mark on it: a pad, or the rail below
// the stack. Nothing is recorded on it, so it never takes the hover band.
func (m *OS) sidebarStripBlank(cw int, edgeLeft bool, pal overlay.Palette) string {
	bg := stripRowBg(false, pal)
	return m.sidebarStripBand(sidebarFit("", cw, bg), cw, edgeLeft, bg, nil, pal)
}

// sidebarStripHeaderCell is a group's own line in the strip's two cells: the
// section label the open rail writes out in full, cut to its first column, and
// that section's add control beside it.
//
// The label takes the spine, the column every mark in the list below it sits
// in, so a header reads as the head of its own column on either side of the
// screen. The control takes the gutter. Neither mirrors, because the strip's
// content columns never do; the toggle at the foot is the one thing on this
// rail that flips, and it flips because it is an arrow.
//
// Both marks are lifted off raw FgMute for the same reason the strip's other
// controls were: at 2.19:1 against the band the glyphs naming the lists would
// have been the least visible thing on the rail.
func sidebarStripHeaderCell(noun, add string, cw int, lit bool, bg color.Color, pal overlay.Palette) string {
	fg := theme.Readable(pal.FgMute, bg)
	if lit {
		fg = pal.Fg
	}
	label := noun
	if r := []rune(noun); len(r) > 0 {
		label = string(r[0])
	}
	if add == "" {
		add = " "
	}
	return sidebarFit(sidebarStyle(bg, fg).Render(add)+sidebarStyle(bg, fg).Render(label), cw, bg)
}

// sidebarStripTerminalCell is one pane of the middle group in two cells: the
// gutter marks the pane in focus, the spine column carries the pane's own state.
//
// The focus mark takes the session's colour, exactly as the open rail's
// terminals section does, and for the same reason: one session's panes are on
// screen at a time, so the mark says which session they belong to rather than
// separating them from each other. A peeked session's panes are somebody else's
// and carry no focus mark at all, which is also what the open rail does.
func (m *OS) sidebarStripTerminalCell(e sidebarTerminalEntry, peeked bool, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if e.Focused && !peeked {
		lead, leadFg = "▎", railFocusTint(m.sessionTint(e.SessionID, bg), pal)
		if overlay.UseASCII() {
			lead = ">"
		}
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+
		stripStateMark(e.State, e.DoneSeen, pal, bg, lit), cw, bg)
}

// sidebarStripAgentCell is one pane of the last group in two cells: the gutter
// marks the pane you are looking at, the spine column carries the pane's state
// glyph in the colour the rest of the rail draws it in.
//
// The gutter says "current" here and severity in the expanded section, which
// looks like a divergence and is not: severity in the gutter would ink the same
// state twice in a two-cell row, which is the double-inking this rail spent a
// round removing.
func (m *OS) sidebarStripAgentCell(e sidebarAgentEntry, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if e.WindowIndex >= 0 && e.WindowIndex == m.FocusedWindow {
		lead, leadFg = "▎", railFocusTint(m.agentIdentityTint(e, bg), pal)
		if overlay.UseASCII() {
			lead = ">"
		}
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+
		stripStateMark(e.State, e.DoneSeen, pal, bg, lit), cw, bg)
}

// stripStateMark is a pane's one cell on the spine: the quiet dot every list on
// this rail rests at, or the pane's state glyph in its own colour when it has
// something to say. One vocabulary across the lists is what lets the stack be
// read as one object at two cells wide.
func stripStateMark(state string, doneSeen bool, pal overlay.Palette, bg color.Color, lit bool) string {
	mark, markFg := "·", stripRestingInk(lit, pal)
	if overlay.UseASCII() {
		mark = "."
	}
	if g := agentStateIndicator(state); g != "" && config.SidebarShowGlyphs {
		mark, markFg = g, sidebarStateColor(state, doneSeen, pal)
	}
	return sidebarStyle(bg, markFg).Render(mark)
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

// sidebarStripMoreCell is a list's tail when a short rail cannot draw every
// item: one muted mark on the spine's own column, so the list ends by saying it
// is cut rather than by stopping.
func sidebarStripMoreCell(cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	mark := "⋮"
	if overlay.UseASCII() {
		mark = ":"
	}
	// Measured rather than picked. Raw FgMute is the separator token and read
	// 2.19:1 on the band, which is where the workspace pills were when a pill you
	// could switch to looked absent. This mark is the only thing that says a list
	// is cut, so a strip with more sessions than lines looked like a strip with
	// exactly as many sessions as lines. Readable lifts it to 4.88:1 and leaves
	// it under the resting dots' 7.37:1, so it clears the floor without becoming
	// another item.
	fg := theme.Readable(pal.FgMute, bg)
	if lit {
		fg = pal.Fg
	}
	return sidebarFit(sidebarStyle(bg, nil).Render(" ")+
		sidebarStyle(bg, fg).Render(mark), cw, bg)
}

// sidebarStripControlCell draws the strip's toggle against the pane-facing
// edge, which is the edge the pointer arrives from. It is measured from that
// edge inwards, so the two-cell ASCII form still lands against it.
func sidebarStripControlCell(glyph string, cw int, edgeLeft, lit bool, bg color.Color, pal overlay.Palette) string {
	// A control on the folded rail is one of the few things a click can act on
	// there, and at raw FgMute it measured 2.19:1 against the band, 3.71:1 once
	// the quiet tier moved up a step: the strip's controls were its least visible
	// marks. Lifted to the text floor for the same reason the tail mark is, and
	// by the same call.
	fg := theme.Readable(pal.FgMute, bg)
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

// sidebarStripCell is one session's two cells: the accent bar in the gutter when
// this is the session you are attached to, and its one mark beside it.
//
// At rest the mark is a dim dot, and that is nearly always what the strip shows,
// which is the state worth designing for. A session wanting a human swaps the
// dot for its own state glyph in its severity colour; an unread finish keeps its
// square. Working does not mark at all: it is not an alarm, the terminals group
// under it already shows it, and spending the list on it was what made every row
// a different shape.
func (m *OS) sidebarStripCell(node sessiontree.Node, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if node.IsCurrent {
		// Left at the raw tint, measured and deliberately. It is 2.76:1 on the
		// band, the number the current workspace pill was lifted from, and
		// Readable would clear it. The strip is the rail at another width rather
		// than another object, so this bar has to be the hue the expanded rail
		// draws for the same session, and lifting one width alone splits them.
		// It is also a filled block rather than type, and it marks the one
		// session the peek already names. Lifting both widths together is the
		// right fix and is a change to the expanded rail, not to this audit.
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
			// Held to the floor against the band it is drawn on. errored measured
			// 4.06:1 raw, so the one state that means something broke was the
			// least legible mark the list could show; needs_input already cleared
			// at 6.49:1 and Readable leaves it alone.
			mark = agentStateIndicator(node.AgentState)
			markFg = theme.Readable(sidebarSeverityColor(node.AgentState, pal), bg)
		case node.AgentState == "done" && !node.DoneSeen:
			mark, markFg = agentStateIndicator(node.AgentState), pal.Success
		}
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+sidebarStyle(bg, markFg).Render(mark), cw, bg)
}
