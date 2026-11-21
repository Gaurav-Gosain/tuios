package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

type sidebarStripRowKind int

const (
	sidebarStripBadge sidebarStripRowKind = iota
	sidebarStripHeader
	sidebarStripSession
	sidebarStripTerminal
	sidebarStripAgent
	sidebarStripMore
	sidebarStripToggle
)

type sidebarStripRow struct {
	Kind      sidebarStripRowKind
	Y0, Y1    int
	SessionID string
	WindowID  string
	Label     string
}

func (r sidebarStripRow) contains(y int) bool { return y >= r.Y0 && y < r.Y1 }

type sidebarStripBadgeInfo struct {
	Count     int
	State     string
	SessionID string
	WindowID  string
}

func sidebarStripBadgeFor(sessions []sessiontree.Node) sidebarStripBadgeInfo {
	var info sidebarStripBadgeInfo
	best := 0
	for _, s := range sessions {
		for _, win := range s.Children {
			if blocked, _ := sidebarAttentionCounts(win.AgentState, win.DoneSeen); !blocked {
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
	m.sidebarSortAgents(kept)
	return kept
}

type sidebarStripGroup struct {
	kind       sidebarStripRowKind
	noun       string
	add        sidebarRowKind
	addSession string
	hasAdd     bool
	total      int

	header, top, end, moreY int
}

func (g sidebarStripGroup) shown() int { return g.end - g.top }

func sidebarStripFloor(g sidebarStripGroup) int { return 1 + min(g.total, 2) }

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

func sidebarStripPlace(groups []sidebarStripGroup, top, region int) []sidebarStripGroup {
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

type stripPart int

const (
	stripPartNone stripPart = iota
	stripPartHeader
	stripPartMark
	stripPartMore
)

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
	if sidebarLayoutHas(sidebarSectionTerminals, &m.Settings) {
		terminals = m.sidebarTerminals(sessions, shownSession)
	}

	var scratch [3]sidebarStripGroup
	groups := scratch[:0]
	groups = append(groups, sidebarStripGroup{
		kind: sidebarStripSession, noun: "session", total: len(sessions),
		add: sidebarRowNewSession, hasAdd: m.SidebarCanCreateSession(),
	})
	listed := ""
	if sidebarLayoutHas(sidebarSectionTerminals, &m.Settings) && (len(terminals) > 0 || peeking) {
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

	hoverY := -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		hoverY = m.SidebarHoverY - topMargin
	}

	nav := make([]sidebarNavRow, 0, region+3)
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
			bg, edgeFg := stripRowBg(false, pal), color.Color(nil)
			if lit {
				bg, edgeFg = agentGlyphColor(badge.State, pal), pal.Canvas
			}
			lines = append(lines, m.sidebarStripBand(sidebarStripBadgeCell(badge, cw, pal), cw, edgeLeft, bg, edgeFg, pal))
		case toggleH > 0 && i == toggleY:
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
					add = sidebarAddGlyph(&m.Settings)
					note(sidebarStripHeader, g.addSession, "", sidebarAddWords(g.add), y)
					record(g.add, g.addSession, "", y)
				} else {
					note(sidebarStripHeader, "", "", g.noun+"s", y)
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
				hidden := g.total - g.shown()
				note(sidebarStripMore, "", "", plural.Count(hidden, "more "+g.noun), y)
				record(sidebarRowCollapse, "", "", y)
				bg := stripRowBg(lit, pal)
				lines = append(lines, m.sidebarStripBand(sidebarStripMoreCell(cw, pal, bg, lit), cw, edgeLeft, bg, nil, pal))
			}
		}
	}

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

func stripRowBg(lit bool, pal overlay.Palette) color.Color {
	if lit {
		return pal.Surface
	}
	return pal.Panel
}

func (m *OS) sidebarStripBand(content string, cw int, edgeLeft bool, bg, edgeFg color.Color, pal overlay.Palette) string {
	rule := edgeFg
	switch {
	case rule != nil:
	case m.sidebarRailFocused():
		rule = theme.ReadableAt(pal.Accent, bg, theme.MarkFloor)
	default:
		rule = theme.RailRuleOn(bg)
	}
	edge := lipgloss.NewStyle().Background(bg).Foreground(rule).Render(m.Settings.GetWindowBorderLeft())
	body := sidebarFit(content, cw, bg)
	if edgeLeft {
		return body + edge
	}
	return edge + body
}

func (m *OS) sidebarStripBlank(cw int, edgeLeft bool, pal overlay.Palette) string {
	bg := stripRowBg(false, pal)
	return m.sidebarStripBand(sidebarFit("", cw, bg), cw, edgeLeft, bg, nil, pal)
}

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

func (m *OS) sidebarStripTerminalCell(e sidebarTerminalEntry, peeked bool, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if e.Focused && !peeked {
		lead, leadFg = m.Settings.GetRailFocusMark(), railFocusTint(m.sessionTint(e.SessionID, bg), pal)
	}
	mark, markFg := stripStateMark(e.State, e.DoneSeen, e.Subagents, pal, lit, &m.Settings)
	return stripCell(lead, leadFg, mark, markFg, cw, bg)
}

func (m *OS) sidebarStripAgentCell(e sidebarAgentEntry, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if e.WindowIndex >= 0 && e.WindowIndex == m.FocusedWindow {
		lead, leadFg = m.Settings.GetRailFocusMark(), railFocusTint(m.agentIdentityTint(e, bg), pal)
	}
	mark, markFg := stripStateMark(e.State, e.DoneSeen, e.Subagents, pal, lit, &m.Settings)
	return stripCell(lead, leadFg, mark, markFg, cw, bg)
}

func stripStateMark(state string, doneSeen bool, running int, pal overlay.Palette, lit bool, s *config.Settings) (string, color.Color) {
	mark, markFg := s.GetRailBullet(), stripRestingInk(lit, pal)
	if g, fg := agentMark(state, doneSeen, running, pal); g != "" && s.SidebarShowGlyphs {
		mark, markFg = g, fg
	}
	return mark, markFg
}

func stripCell(lead string, leadFg color.Color, mark string, markFg color.Color, cw int, bg color.Color) string {
	if lipgloss.Width(lead)+lipgloss.Width(mark) > cw {
		return sidebarFit(sidebarStyle(bg, markFg).Render(mark), cw, bg)
	}
	return sidebarFit(sidebarStyle(bg, leadFg).Render(lead)+sidebarStyle(bg, markFg).Render(mark), cw, bg)
}

func stripRestingInk(lit bool, pal overlay.Palette) color.Color {
	if lit {
		return pal.Fg
	}
	return pal.FgDim
}

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
	if lipgloss.Width(count+glyph) > cw {
		glyph = ""
	}
	return sidebarFit(sidebarStyle(bg, pal.Canvas).Bold(true).Render(count+glyph), cw, bg)
}

func sidebarStripMoreCell(cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	mark := "⋮"
	if overlay.UseASCII() {
		mark = ":"
	}
	fg := theme.Readable(pal.FgMute, bg)
	if lit {
		fg = pal.Fg
	}
	return sidebarFit(sidebarStyle(bg, nil).Render(" ")+
		sidebarStyle(bg, fg).Render(mark), cw, bg)
}

func sidebarStripControlCell(glyph string, cw int, edgeLeft, lit bool, bg color.Color, pal overlay.Palette) string {
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

func (m *OS) sidebarStripCell(node sessiontree.Node, cw int, pal overlay.Palette, bg color.Color, lit bool) string {
	lead, leadFg := " ", color.Color(nil)
	if node.IsCurrent {
		lead, leadFg = m.Settings.GetRailFocusMark(), railFocusTint(m.sessionTint(node.ID, bg), pal)
	}

	mark, markFg := m.Settings.GetRailBullet(), stripRestingInk(lit, pal)
	if m.Settings.SidebarShowGlyphs {
		switch {
		case sidebarAttention(node.AgentState):
			mark = agentStateIndicator(node.AgentState)
			markFg = theme.Readable(sidebarSeverityColor(node.AgentState, pal), bg)
		case node.AgentState == "done" && !node.DoneSeen:
			mark, markFg = agentStateIndicator(node.AgentState), pal.Success
		}
	}
	return stripCell(lead, leadFg, mark, markFg, cw, bg)
}
