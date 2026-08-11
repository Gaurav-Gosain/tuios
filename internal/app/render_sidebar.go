package app

import (
	"image/color"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The sidebar is drawn as chrome in tuios's own visual language rather than as
// a filled panel: rows sit directly on the terminal background (like the dock),
// a single muted rule in the window-border character separates the rail from
// the panes, and emphasis is carried by the same pills the dock uses. The
// focused window gets the dock's blue focus pill, the current session a quiet
// Surface-colored pill, everything else stays dim text. Two sections share the
// rail: the panes currently running an agent at the top, priority-ordered, and
// the session tree below them. Attention leads because the first question a
// rail full of agents has to answer is "which one needs me", not "what exists";
// the tree keeps every behaviour it had, one section lower.

// sidebarFocusColor is the focused-row pill fill, matching the dock's focused
// pill (render_dock.go) so the two chrome surfaces never disagree about what
// "focused" looks like.
const sidebarFocusColor = "#4865f2"

// sidebarRowKind distinguishes what a sidebar row points at for mouse routing.
type sidebarRowKind int

const (
	sidebarRowSession sidebarRowKind = iota
	sidebarRowWindow
	// sidebarRowAgent is a row in the running-agents section; it targets a
	// window exactly like sidebarRowWindow, it just lives in the other section.
	sidebarRowAgent
	// sidebarRowWorkspace is one chip of the workspace band under the current
	// session. Unlike every other kind it is narrower than its line: several
	// chips share one row, each with its own columns.
	sidebarRowWorkspace
	// sidebarRowNewSession is the "+ new session" affordance at the foot of the
	// sessions list. It targets nothing that exists yet.
	sidebarRowNewSession
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
	// Workspace is the target of a band chip, 0 on every other kind.
	Workspace int
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

// sidebarStateColor is agentGlyphColor with the unread bit folded in: a
// finished pane that has been looked at goes muted, so colour on a done row
// means "not yet seen" rather than "finished at some point".
func sidebarStateColor(state string, doneSeen bool, pal overlay.Palette) color.Color {
	if state == "done" && doneSeen {
		return pal.FgMute
	}
	return agentGlyphColor(state, pal)
}

// sidebarAttention reports the states that mean a human is required. They are
// the only ones allowed to ink a whole cell or tint a whole row: reserving fill
// for those two is what keeps the fill legible as an alarm.
func sidebarAttention(state string) bool {
	return state == "needs_input" || state == "errored"
}

// sidebarAttentionTint is the resting row background for an attention state:
// the severity colour mixed a fifth of the way into the canvas, so the row
// reads as lit without becoming a coloured slab its own text has to fight.
func sidebarAttentionTint(state string, pal overlay.Palette) color.Color {
	var sev color.Color
	switch state {
	case "needs_input":
		sev = pal.Warning
	case "errored":
		sev = pal.Warn
	default:
		return nil
	}
	if sev == nil || pal.Canvas == nil {
		return nil
	}
	const mix = 22 // percent severity in the mix
	br, bgc, bb, _ := pal.Canvas.RGBA()
	sr, sg, sb, _ := sev.RGBA()
	c := func(base, s uint32) uint8 { return uint8((base*(100-mix) + s*mix) / 100 >> 8) }
	return color.RGBA{R: c(br, sr), G: c(bgc, sg), B: c(bb, sb), A: 0xff}
}

// agentElapsedBucket is the minute the stamp currently reads as, for the render
// cache to key on. Minute granularity is deliberate: a seconds readout would
// rebuild the whole rail once a second forever, while minutes cost at most one
// rebuild per pane per minute, on a frame that was happening anyway. Zero for an
// unstamped pane, so a rail with no agents folds a constant and never rebuilds
// on time alone.
func agentElapsedBucket(stateAt int64) int64 {
	if stateAt <= 0 {
		return 0
	}
	return int64(time.Since(time.Unix(0, stateAt)) / time.Minute)
}

// agentElapsed is how long a pane has been in its state, in at most three cells:
// "<1m", "7m", "3h", "2d". It replaces a state word, which only repeated what the
// glyph and the sort order already said. Blank for the resting states, where the
// age is trivia, and blank without a stamp.
func agentElapsed(state string, stateAt int64, now time.Time) string {
	if stateAt <= 0 || state == "idle" || state == "" {
		return ""
	}
	d := now.Sub(time.Unix(0, stateAt))
	switch {
	case d < time.Minute: // covers clock skew, which would otherwise read negative
		return "<1m"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

// sidebarAgentEntry is one pane running an agent, flattened out of the session
// tree for the agents section.
type sidebarAgentEntry struct {
	SessionID   string
	WindowID    string
	Title       string
	State       string
	DoneSeen    bool
	StateAt     int64
	WindowIndex int
	// Foreign marks a pane of a session other than the attached one, whose row
	// carries the session name for context.
	Foreign bool
}

// sidebarStyle returns a style carrying the given colors, either of which may
// be nil. A nil background deliberately leaves the terminal's own background
// in place: the rail is lines of text, not a filled slab.
func sidebarStyle(bg, fg color.Color) lipgloss.Style {
	s := lipgloss.NewStyle()
	if bg != nil {
		s = s.Background(bg)
	}
	if fg != nil {
		s = s.Foreground(fg)
	}
	return s
}

// sidebarFit truncates (ANSI-aware) and pads s to exactly cw cells on bg, so a
// row can never draw past the rail's own columns.
func sidebarFit(s string, cw int, bg color.Color) string {
	if lipgloss.Width(s) > cw {
		s = lipgloss.NewStyle().MaxWidth(cw).Render(s)
	}
	if d := cw - lipgloss.Width(s); d > 0 {
		s += sidebarStyle(bg, nil).Render(strings.Repeat(" ", d))
	}
	return s
}

// chromeGlyphs are the symbol codepoints we draw ourselves: the agent-state
// indicators and the session chevron. They sit inside the decorative blocks
// printableTitle strips, so they are named rather than kept by range.
var chromeGlyphs = map[rune]bool{
	'●': true, '▲': true, '○': true, '■': true, // agentStateIndicator
	'▾': true, '▸': true, // sidebarChevron
}

// printableTitle strips what a terminal cannot be trusted to render out of a
// title before it is shown as chrome (sidebar rows, the window title badge, the
// command palette, the dock): control characters and private-use codepoints
// (nerd-font icons shells love to put in titles, which show as tofu boxes
// without the right font), decorative symbol and emoji codepoints (an agent
// setting a dingbat or emoji in its title otherwise tofus wherever we echo it),
// plus everything non-ASCII when ASCII-only rendering is on. Titles are foreign
// data; our own chrome glyphs are audited, so they are kept by codepoint.
// Titles have to be laundered.
func printableTitle(s string) string {
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			// C0/C1 controls.
		case r >= 0xe000 && r <= 0xf8ff:
			// BMP private use area.
		case r >= 0xf0000:
			// Plane 15/16 private use.
		case r >= 0x25a0 && r <= 0x2bff && !chromeGlyphs[r]:
			// Geometric Shapes through Miscellaneous Symbols and Arrows. Agents
			// park spinners and status ornaments in here (Claude Code alone uses
			// U+2733 idle and a U+2802/U+2810 Braille spinner) and they tofu in
			// any font that stops at Latin. Box Drawing and Block Elements end at
			// U+259F, so a title may still draw with them.
		case r >= 0xfe00 && r <= 0xfe0f:
			// Variation Selectors (VS1-16), including the emoji VS16.
		case r >= 0x1f000 && r <= 0x1faff:
			// Emoji and pictographic planes (Regional Indicator flag halves
			// U+1F1E6-1F1FF sit inside this span).
		case ascii && r > 0x7e:
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// sidebarChevron is the expand/collapse marker for a session row.
func sidebarChevron(expanded bool) string {
	if overlay.UseASCII() {
		if expanded {
			return "v"
		}
		return ">"
	}
	if expanded {
		return "▾"
	}
	return "▸"
}

// sidebarPaneIndent is the gutter a pane row spends before its state glyph: one
// level in from the session row that owns it. Window rows, the workspace band
// and the agents section all measure from it, so the rail keeps one glyph column
// and one name spine (indent+2) whatever kind of row you are looking at.
func sidebarPaneIndent(variant int) int {
	if variant == sidebarVariantNarrow {
		return 2
	}
	return 3
}

// sidebarGlyph returns the styled agent-state glyph for a row, or a single
// space on the row background when there is no state or glyphs are disabled,
// so rows stay aligned. It always occupies exactly one cell.
func sidebarGlyph(state string, doneSeen bool, bg color.Color, pal overlay.Palette) string {
	if !config.SidebarShowGlyphs {
		return sidebarStyle(bg, nil).Render(" ")
	}
	g := agentStateIndicator(state)
	if g == "" {
		return sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarStyle(bg, sidebarStateColor(state, doneSeen, pal)).Render(g)
}

// sidebarPill wraps text in the dock's pill caps on the given fill. rowBg is
// what sits behind the caps (nil for the bare terminal background), so a pill
// on a hovered row does not punch holes in the hover bar.
func sidebarPill(text string, fill, fg, rowBg color.Color) string {
	caps := sidebarStyle(rowBg, fill)
	return caps.Render(config.GetDockPillLeftChar()) +
		lipgloss.NewStyle().Background(fill).Foreground(fg).Bold(true).Render(text) +
		caps.Render(config.GetDockPillRightChar())
}

// sidebarEdgeRule is the one-cell vertical rule separating the rail from the
// panes, drawn in the window-border character at the dock separator's color:
// the rail's edge is the vertical sibling of the dock's hairline.
func sidebarEdgeRule() string {
	return lipgloss.NewStyle().Foreground(theme.NotificationRule()).Render(config.GetWindowBorderLeft())
}

// sidebarRuleRow is a full-content-width hairline, used to fence off the
// agents section.
func sidebarRuleRow(cw int) string {
	return lipgloss.NewStyle().Foreground(theme.NotificationRule()).
		Render(strings.Repeat(config.GetWindowSeparatorChar(), cw))
}

// sidebarHeaderRow renders a quiet section header: just the label, muted, so it
// frames its section without competing with it. It carries no count on purpose,
// because the number only restated the rows printed directly underneath it, and
// the agents section already owns up to what it hides with its own "+N" line.
func sidebarHeaderRow(label string, cw int, pal overlay.Palette) string {
	row := sidebarStyle(nil, nil).Render(" ") +
		sidebarStyle(nil, pal.FgDim).Bold(true).Render(overlay.Truncate(label, max(cw-2, 1)))
	return sidebarFit(row, cw, nil)
}

// sidebarChipSpan is one band chip's columns, measured from the rail's content
// start. Several of these share a single drawn row, which is why the band is the
// one place a row maps to more than one hit rectangle.
type sidebarChipSpan struct {
	Workspace int
	X0, X1    int
}

// sidebarWorkspaceBand renders the current session's workspaces as one row of
// chips: the same set the dock strip names, re-rendered in the rail. It returns
// the row and where each chip landed, or nothing when there is nowhere a click
// could take you (one workspace) or no room to say so.
//
// cursorWS is the workspace the keyboard cursor sits on and hoverX the pointer's
// column within the content, both 0 and -1 when absent. A chip that would not
// fit is dropped rather than clipped: half a chip is half a click target.
func (m *OS) sidebarWorkspaceBand(variant, cw int, pal overlay.Palette, cursorWS, hoverX int) (string, []sidebarChipSpan) {
	occupied := m.occupiedWorkspaces()
	if len(occupied) < 2 {
		return "", nil
	}

	indent := sidebarPaneIndent(variant)
	row := strings.Repeat(" ", indent)
	x := indent
	spans := make([]sidebarChipSpan, 0, len(occupied))
	for _, n := range occupied {
		w := workspaceChipWidth(n)
		if x+w > cw {
			break
		}
		active := n == m.CurrentWorkspace
		fg, rowBg := pal.FgMute, color.Color(nil)
		if !active && (n == cursorWS || (hoverX >= x && hoverX < x+w)) {
			fg, rowBg = pal.Fg, pal.RowSel
		}
		row += workspaceChip(n, active, fg, rowBg)
		spans = append(spans, sidebarChipSpan{Workspace: n, X0: x, X1: x + w})
		x += w
	}
	if len(spans) < 2 {
		return "", nil
	}
	return sidebarFit(row, cw, nil), spans
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

// sidebarPanelLinesForTree lays the rail out for a given tree and records the
// on-screen hit geometry of every row into m.SidebarHits, returning the rows
// and the reserved width. It returns nil rows when the sidebar reserves
// nothing.
//
// The rail is two sections: the running-agents section at the top, ordered by
// how much it wants a human (hidden entirely when no pane is running an agent,
// or when the rail is too small to carry it), and the session tree below a
// hairline, scrolled by m.SidebarScroll. Every emitted line is exactly the
// reserved width: the content columns plus the one-cell edge rule on the side
// facing the panes.
func (m *OS) sidebarPanelLinesForTree(tree sessiontree.Tree) ([]string, int) {
	m.SidebarHits = m.SidebarHits[:0]
	m.SidebarSessionIDs = m.SidebarSessionIDs[:0]

	// Re-armed each frame: a marquee row sets it, so a key left standing after
	// the row stops drawing hovered means the scroll is over and the tick idles.
	m.sidebarMarqueeSeen = false
	defer func() {
		if !m.sidebarMarqueeSeen {
			m.SidebarMarqueeKey = ""
		}
	}()

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
	edgeLeft := config.SidebarPosition != "right"
	if !edgeLeft {
		sidebarX = m.GetRenderWidth() - w
	}
	// First content column: a right-hand rail spends its first band column on
	// the edge rule, so the content starts one cell in.
	contentX0 := sidebarX
	if !edgeLeft {
		contentX0++
	}

	pal := theme.UI()
	variant := sidebarVariant(w)
	cw := w - 1 // content columns beside the edge rule
	edge := sidebarEdgeRule()
	// While the rail owns the keyboard its edge rule burns accent instead of the
	// dock's muted hairline, so the focus is legible at the frame, not only on a
	// single highlighted row.
	if m.SidebarFocused {
		edge = lipgloss.NewStyle().Foreground(pal.Accent).Render(config.GetWindowBorderLeft())
	}

	// The keyboard cursor tracks a row by identity, not by index, so it survives a
	// relayout: the target is the session the last action asked to follow, else
	// the nav row the cursor was on last frame. Rows matching it draw the same
	// RowSel bar hover uses; the two share one cursor.
	var cursorTarget sidebarNavRow
	haveCursorTarget := false
	switch {
	case m.sidebarFollowSession != "":
		cursorTarget = sidebarNavRow{Kind: sidebarRowSession, SessionID: m.sidebarFollowSession}
		haveCursorTarget = true
	case m.SidebarCursor >= 0 && m.SidebarCursor < len(m.SidebarNav):
		cursorTarget = m.SidebarNav[m.SidebarCursor]
		haveCursorTarget = true
	}
	isCursor := func(kind sidebarRowKind, sessionID, windowID string) bool {
		return m.SidebarFocused && haveCursorTarget &&
			cursorTarget.Kind == kind && cursorTarget.SessionID == sessionID && cursorTarget.WindowID == windowID
	}
	// The band's cursor is a chip rather than a row, so it is addressed by
	// workspace; 0 means the cursor is elsewhere.
	cursorWS := 0
	if m.SidebarFocused && haveCursorTarget && cursorTarget.Kind == sidebarRowWorkspace {
		cursorWS = cursorTarget.Workspace
	}
	nav := make([]sidebarNavRow, 0, 16)
	cursorLogical := -1

	// compose attaches the edge rule on the pane-facing side.
	compose := func(content string) string {
		if edgeLeft {
			return content + edge
		}
		return edge + content
	}
	blank := compose(strings.Repeat(" ", cw))

	sessions := tree.Sessions
	if m.SidebarDrag.Dragging {
		// Mid-drag the draft order is displayed live, so the dragged row itself
		// is the drop indicator: where it sits is where it lands.
		sessions = orderByKey(sessions, func(n sessiontree.Node) string { return n.ID }, m.SidebarDrag.Order)
	}
	for _, s := range sessions {
		m.SidebarSessionIDs = append(m.SidebarSessionIDs, s.ID)
	}

	// Flatten the panes running agents. Sessions with known windows contribute:
	// the attached one from live state, others from the cached listing, so
	// agents on other sessions surface here marked Foreign.
	var agents []sidebarAgentEntry
	if variant != sidebarVariantGlyph {
		for _, s := range sessions {
			for _, win := range s.Children {
				if win.AgentState == "" {
					continue
				}
				idx := -1
				if s.IsCurrent {
					idx = m.windowIndexByID(win.ID)
				}
				agents = append(agents, sidebarAgentEntry{
					SessionID:   s.ID,
					WindowID:    win.ID,
					Title:       win.Title,
					State:       win.AgentState,
					DoneSeen:    win.DoneSeen,
					StateAt:     win.StateAt,
					WindowIndex: idx,
					Foreign:     !s.IsCurrent,
				})
			}
		}
		// Ordered by need, stable within a rank so panes keep their tree order.
		// This is what makes the cap below safe: what it hides is provably the
		// calm end of the list, never the pane waiting on an answer.
		sort.SliceStable(agents, func(a, b int) bool {
			return sessiontree.AgentRank(agents[a].State, agents[a].DoneSeen) >
				sessiontree.AgentRank(agents[b].State, agents[b].DoneSeen)
		})
	}

	// Section geometry: the agents section takes at most a third of the rail
	// and only exists when there is something to show and room to show it.
	// agentRows is how many of those lines are real rows; a capped section
	// spends its last line owning up to what it hides.
	agentShown, agentSection := 0, 0
	if len(agents) > 0 && height >= 8 {
		agentShown = min(len(agents), max(height/3, 1))
		agentSection = agentShown + 2 // header + hairline
	}
	overflow := len(agents) - agentShown
	agentRows := agentShown
	if overflow > 0 {
		agentRows--
	}
	treeH := height - agentSection

	// Hover, derived from the last motion seen inside the band. Rows are one
	// screen line each, so the hovered agent row is the pointer's distance from
	// the top less the section header, and the hovered tree row is the scroll
	// offset plus its distance past the whole agents block. Hover yields
	// entirely to a drag.
	treeHover, agentHover := -1, -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		delta := m.SidebarHoverY - topMargin
		if delta < agentSection {
			if d := delta - 1; d >= 0 && d < agentRows {
				agentHover = d
			}
		} else {
			treeHover = m.SidebarScroll + delta - agentSection
		}
	}

	// Nav rows are published in drawn order, so the agents lead here exactly as
	// they lead on screen and j/k walks what the eye reads.
	for i := 0; i < agentRows; i++ {
		e := agents[i]
		nav = append(nav, sidebarNavRow{Kind: sidebarRowAgent, SessionID: e.SessionID, WindowID: e.WindowID, WindowIndex: e.WindowIndex})
	}

	// Build the logical tree rows (header + sessions + expanded windows), each
	// with the target it routes to, then window them by SidebarScroll.
	type logicalRow struct {
		text        string
		interactive bool
		sessionID   string
		windowID    string
		windowIndex int
		kind        sidebarRowKind
		// chips is set on the band row only: it hit-tests per chip instead of
		// claiming the whole line.
		chips []sidebarChipSpan
	}
	rows := make([]logicalRow, 0, 16)

	if variant != sidebarVariantGlyph {
		rows = append(rows, logicalRow{text: sidebarHeaderRow("Sessions", cw, pal)})
	}

	for _, session := range sessions {
		expanded := m.sidebarSessionExpanded(session)
		dragged := m.SidebarDrag.Dragging && session.ID == m.SidebarDrag.SessionID
		if isCursor(sidebarRowSession, session.ID, "") {
			cursorLogical = len(rows)
		}
		rows = append(rows, logicalRow{
			text:        m.sidebarSessionRow(session, variant, expanded, cw, pal, len(rows) == treeHover || isCursor(sidebarRowSession, session.ID, ""), dragged),
			interactive: true,
			sessionID:   session.ID,
			windowIndex: -1,
			kind:        sidebarRowSession,
		})
		nav = append(nav, sidebarNavRow{Kind: sidebarRowSession, SessionID: session.ID, WindowIndex: -1})

		if variant == sidebarVariantGlyph || !expanded {
			continue
		}

		// The band rides directly under the session it belongs to. Only the
		// attached session has workspace data at all (a foreign session's
		// workspaces are not on the wire), so only it gets one.
		if session.IsCurrent {
			hoverX := -1
			if len(rows) == treeHover && m.SidebarHoverActive {
				hoverX = m.SidebarHoverX - contentX0
			}
			if band, chips := m.sidebarWorkspaceBand(variant, cw, pal, cursorWS, hoverX); len(chips) > 0 {
				rows = append(rows, logicalRow{text: band, interactive: true, sessionID: session.ID, kind: sidebarRowWorkspace, windowIndex: -1, chips: chips})
				for _, c := range chips {
					if cursorWS == c.Workspace {
						cursorLogical = len(rows) - 1
					}
					nav = append(nav, sidebarNavRow{Kind: sidebarRowWorkspace, SessionID: session.ID, WindowIndex: -1, Workspace: c.Workspace})
				}
			}
		}

		if !config.SidebarShowWindows {
			continue
		}
		for _, win := range session.Children {
			idx := -1
			if session.IsCurrent {
				idx = m.windowIndexByID(win.ID)
			}
			if isCursor(sidebarRowWindow, session.ID, win.ID) {
				cursorLogical = len(rows)
			}
			rows = append(rows, logicalRow{
				text:        m.sidebarWindowRow(win, variant, cw, pal, len(rows) == treeHover || isCursor(sidebarRowWindow, session.ID, win.ID)),
				interactive: true,
				sessionID:   session.ID,
				windowID:    win.ID,
				windowIndex: idx,
				kind:        sidebarRowWindow,
			})
			nav = append(nav, sidebarNavRow{Kind: sidebarRowWindow, SessionID: session.ID, WindowID: win.ID, WindowIndex: idx})
		}
	}

	// The pill sits after the last session, which is exactly where the session it
	// creates will land: orderByKey appends. Standalone has no session list to
	// add to, so the row is absent rather than dimmed; a permanently dead control
	// is noise.
	if m.SidebarCanCreateSession() {
		hovered := len(rows) == treeHover || isCursor(sidebarRowNewSession, "", "")
		if isCursor(sidebarRowNewSession, "", "") {
			cursorLogical = len(rows)
		}
		rows = append(rows, logicalRow{
			text:        sidebarNewSessionRow(variant, cw, pal, hovered),
			interactive: true,
			windowIndex: -1,
			kind:        sidebarRowNewSession,
		})
		nav = append(nav, sidebarNavRow{Kind: sidebarRowNewSession, WindowIndex: -1})
	}

	// Keyboard cursor auto-scroll: a cursor in the tree region is kept on screen,
	// so j/k past the fold scrolls the list the way a wheel would.
	if m.SidebarFocused && cursorLogical >= 0 && treeH > 0 {
		if cursorLogical < m.SidebarScroll {
			m.SidebarScroll = cursorLogical
		} else if cursorLogical >= m.SidebarScroll+treeH {
			m.SidebarScroll = cursorLogical - treeH + 1
		}
	}

	// Vertical scroll over the tree region only; the agents section is pinned.
	// Clamp so the last row is always reachable and never overscrolled.
	maxScroll := max(len(rows)-treeH, 0)
	m.SidebarScroll = max(min(m.SidebarScroll, maxScroll), 0)

	end := min(m.SidebarScroll+treeH, len(rows))
	lines := make([]string, 0, height)
	recordHit := func(kind sidebarRowKind, sessionID, windowID string, windowIndex int) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + 1,
			Kind:        kind,
			SessionID:   sessionID,
			WindowID:    windowID,
			WindowIndex: windowIndex,
		})
	}
	if agentSection > 0 {
		lines = append(lines, compose(sidebarHeaderRow("Agents", cw, pal)))
		for i := range agentShown {
			if i >= agentRows {
				// Stands in for the rows it hides, so it starts on their name spine.
				more := overlay.Ellipsis() + " +" + strconv.Itoa(overflow+1)
				lines = append(lines, compose(sidebarFit(
					strings.Repeat(" ", sidebarPaneIndent(variant)+2)+
						sidebarStyle(nil, pal.FgMute).Render(more), cw, nil)))
				continue
			}
			e := agents[i]
			recordHit(sidebarRowAgent, e.SessionID, e.WindowID, e.WindowIndex)
			lines = append(lines, compose(m.sidebarAgentRow(e, variant, cw, pal, i == agentHover || isCursor(sidebarRowAgent, e.SessionID, e.WindowID))))
		}
		lines = append(lines, compose(sidebarRuleRow(cw)))
	}

	for _, r := range rows[m.SidebarScroll:end] {
		switch {
		case len(r.chips) > 0:
			// One rectangle per chip, published in drawn order so the hits stay
			// aligned with the nav rows the band added.
			y := topMargin + len(lines)
			for _, c := range r.chips {
				m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
					X0: contentX0 + c.X0, X1: contentX0 + c.X1,
					Y0: y, Y1: y + 1,
					Kind:        r.kind,
					SessionID:   r.sessionID,
					WindowIndex: -1,
					Workspace:   c.Workspace,
				})
			}
		case r.interactive:
			recordHit(r.kind, r.sessionID, r.windowID, r.windowIndex)
		}
		lines = append(lines, compose(r.text))
	}
	for len(lines) < height {
		lines = append(lines, blank)
	}

	// Publish the frame's navigable rows for the keyboard, then re-anchor the
	// cursor onto the row it was tracking so its index stays valid across a
	// relayout (reorder, switch, expand/collapse). A follow request is consumed
	// here, once the row it named exists in the new layout.
	m.SidebarNav = nav
	m.sidebarFollowSession = ""
	if haveCursorTarget {
		m.SidebarCursor = 0
		for i, r := range nav {
			if sidebarNavRowsEqual(r, cursorTarget) {
				m.SidebarCursor = i
				break
			}
		}
	}
	if m.SidebarCursor >= len(nav) {
		m.SidebarCursor = max(len(nav)-1, 0)
	}

	return lines, w
}

// sidebarNewSessionRow renders the "+ new session" affordance: dim, the whole
// row a click target, lit like any other row under the pointer or the cursor.
func sidebarNewSessionRow(variant, cw int, pal overlay.Palette, hovered bool) string {
	label := "new session"
	if variant == sidebarVariantNarrow {
		label = "new"
	}

	var rowBg color.Color
	fg := pal.FgMute
	if hovered {
		rowBg, fg = pal.RowSel, pal.Fg
	}
	if variant == sidebarVariantGlyph {
		row := sidebarStyle(rowBg, nil).Render(" ") + sidebarStyle(rowBg, fg).Render("+")
		return sidebarFit(row, cw, rowBg)
	}
	// The + takes the session rows' glyph column and the label their name spine,
	// so the row that makes a session scans as one of them.
	row := sidebarStyle(rowBg, nil).Render("  ") +
		sidebarStyle(rowBg, fg).Render("+") +
		sidebarStyle(rowBg, nil).Render("  ") +
		sidebarStyle(rowBg, fg).Render(overlay.Truncate(label, max(cw-6, 1)))
	return sidebarFit(row, cw, rowBg)
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

// sidebarSessionRow renders one session row for the given variant.
//
// Full variant anatomy, kept to fixed columns so the rows scan vertically:
//
//	▾ ● name           3
//	^ ^ ^              ^ window count, right-aligned, muted
//	| | name: the current session's name sits in a quiet Surface pill,
//	| |       the others are dim text
//	| rolled-up agent glyph, state-colored
//	expand chevron, muted; blank when there is nothing to expand
//
// Hover puts the overlay row-selection bar under the row; a drag in progress
// keeps that bar on the dragged row while it rides the pointer.
func (m *OS) sidebarSessionRow(node sessiontree.Node, variant int, expanded bool, cw int, pal overlay.Palette, hovered, dragged bool) string {
	if variant == sidebarVariantGlyph {
		// One rolled-up glyph per session. A session with no agent state still
		// marks its slot with a dim dot, otherwise its row would be an invisible
		// strip of nothing to aim a click at.
		mark := agentStateIndicator(node.AgentState)
		if mark == "" || !config.SidebarShowGlyphs {
			mark = "·"
			if overlay.UseASCII() {
				mark = "."
			}
		}
		var bg, fg color.Color
		switch {
		case sidebarAttention(node.AgentState) && config.SidebarShowGlyphs:
			// Two cells is too little room for a coloured glyph to carry an
			// alarm, so at this width the cell itself is inked and the glyph is
			// knocked out of it. This outranks the current-session fill: the
			// whole job of a three-column rail is saying which session wants you.
			bg, fg = agentGlyphColor(node.AgentState, pal), pal.Canvas
		case node.IsCurrent:
			bg = lipgloss.Color(sidebarFocusColor)
		case hovered || dragged:
			bg = pal.RowSel
		}
		if fg == nil {
			fg = pal.FgMute
			if config.SidebarShowGlyphs && agentStateIndicator(node.AgentState) != "" {
				fg = sidebarStateColor(node.AgentState, node.DoneSeen, pal)
			}
		}
		row := sidebarStyle(bg, nil).Render(" ") + sidebarStyle(bg, fg).Render(mark)
		return sidebarFit(row, cw, bg)
	}

	var rowBg color.Color
	switch {
	case hovered || dragged:
		rowBg = pal.RowSel
	default:
		rowBg = sidebarAttentionTint(node.AgentState, pal)
	}

	chevron := " "
	if config.SidebarShowWindows && len(node.Children) > 0 {
		chevron = sidebarChevron(expanded)
	}

	// Chevron on the first column, one space, then the state glyph. The name
	// follows immediately: the pill's cap and inner space already separate it, and
	// plain rows pad by the same two cells, so both sit on one spine without a
	// wide empty gutter.
	left := sidebarStyle(rowBg, pal.FgMute).Render(chevron) +
		sidebarStyle(rowBg, nil).Render(" ") +
		sidebarGlyph(node.AgentState, node.DoneSeen, rowBg, pal)

	right := ""
	rightW := 0
	if config.SidebarShowCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		countStr := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(countStr) +
			sidebarStyle(rowBg, nil).Render(" ")
		rightW = lipgloss.Width(countStr) + 1
	}

	avail := max(cw-lipgloss.Width(left)-rightW-1, 1)
	name := printableTitle(node.Title)
	var nameSeg string
	if node.IsCurrent {
		// The attached session is a quiet raised chip, one luminance step up,
		// marked in place: the list order never moves to flatter it.
		name = overlay.Truncate(name, max(avail-4, 1))
		nameSeg = sidebarPill(" "+name+" ", pal.Surface, pal.Fg, rowBg)
	} else {
		fg := pal.FgDim
		if hovered || dragged {
			fg = pal.Fg
		}
		// Match the current session's pill inset (a cap plus its inner space, two
		// cells each side) so a plain row's name lands in the same column as the
		// pilled row's. Without it the name jumps two cells left row to row as the
		// eye moves off the current session.
		name = m.sidebarMarquee("s:"+node.ID, name, max(avail-4, 1), hovered)
		pad := sidebarStyle(rowBg, nil).Render("  ")
		nameSeg = pad + sidebarStyle(rowBg, fg).Bold(dragged).Render(name) + pad
	}

	row := left + nameSeg
	gap := max(cw-lipgloss.Width(row)-rightW, 0)
	row += sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", gap)) + right
	return sidebarFit(row, cw, rowBg)
}

// sidebarWindowRow renders one window row under an expanded session, indented
// one level past the session name. The focused window carries the dock's blue
// focus pill; the rest stay dim so the list reads as detail under its session,
// not as a second list of equals.
func (m *OS) sidebarWindowRow(node sessiontree.Node, variant int, cw int, pal overlay.Palette, hovered bool) string {
	title := printableTitle(node.Title)
	if title == "" {
		title = "shell"
	}

	// One level past the session's glyph column, so a window title lands on the
	// same spine as its session name.
	indent := sidebarPaneIndent(variant)

	// A rename in flight owns the row it targets, focused pane or not: the
	// buffer is what the user is editing and it has to be where they are looking.
	if m.RenamingWindow && node.ID == m.RenameTargetID {
		// The buffer draws on the title's own spine, so the text does not jump
		// sideways the moment a rename starts.
		text := overlay.Truncate(printableTitle(m.RenameBuffer), max(cw-indent-4, 1)) + "_"
		row := sidebarStyle(pal.Card, nil).Render(strings.Repeat(" ", indent+2)) +
			sidebarStyle(pal.Card, pal.Fg).Render(text)
		return sidebarFit(row, cw, pal.Card)
	}

	if node.IsCurrent {
		// The pointer lights this row like any other; the pill sits on the hover
		// bar rather than cancelling it, the way the current session's row does.
		var pillBg color.Color
		if hovered {
			pillBg = pal.RowSel
		}
		// On the saturated focus fill the state colors vanish, so glyph and
		// title share the pill foreground; the shape still carries the state.
		// Fixed pill overhead: caps and inner padding (3), plus glyph and its
		// gap (2) when one is shown.
		inner := overlay.Truncate(title, max(cw-indent-4, 1))
		if config.SidebarShowGlyphs {
			if g := agentStateIndicator(node.AgentState); g != "" {
				inner = g + " " + overlay.Truncate(title, max(cw-indent-6, 1))
			}
		}
		// The pill's cap sits on the glyph column (indent), so its inner text
		// lands on the same spine as the session name and the non-focused
		// siblings, both at indent+2. Starting a cell earlier made the focused
		// pill jog left of everything else.
		lead := sidebarStyle(pillBg, nil).Render(strings.Repeat(" ", indent))
		// An accent is the pane's identity and has to outlive focus. The pill's
		// saturated fill swallows a colored mark, so the accent takes the gutter
		// cell just before the cap, where it still reads. Agent state still wins
		// the glyph inside the pill, matching the unfocused precedence.
		if node.AgentState == "" && config.SidebarShowGlyphs {
			if idx, ok := m.WindowAccent(node.ID); ok {
				lead = sidebarStyle(pillBg, nil).Render(strings.Repeat(" ", indent-1)) +
					sidebarStyle(pillBg, accentColor(idx)).Render(accentMark())
			}
		}
		row := lead + sidebarPill(" "+inner+" ", lipgloss.Color(sidebarFocusColor), lipgloss.Color("#ffffff"), pillBg)
		return sidebarFit(row, cw, pillBg)
	}

	rowBg := sidebarAttentionTint(node.AgentState, pal)
	fg := pal.FgDim
	if node.AgentState == "done" && !node.DoneSeen {
		// Unseen work reads at full strength; seeing it is what dims it.
		fg = pal.Fg
	}

	// A pane on another workspace is here for orientation, not for reading: it
	// goes a step further down and names the workspace it is on, so the digit
	// answers "where did it go" without a switch to find out.
	elsewhere := 0
	if ws := m.windowWorkspace(node.ID); ws > 0 && ws != m.CurrentWorkspace {
		elsewhere = ws
		fg = pal.FgMute
	}

	if hovered {
		rowBg = pal.RowSel
		fg = pal.Fg
	}

	right, rightW := "", 0
	if elsewhere > 0 {
		digit := strconv.Itoa(elsewhere)
		right = sidebarStyle(rowBg, pal.FgMute).Render(digit) + sidebarStyle(rowBg, nil).Render(" ")
		rightW = lipgloss.Width(digit) + 1
	}

	row := sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent)) +
		m.sidebarWindowGlyph(node, rowBg, pal) +
		sidebarStyle(rowBg, nil).Render(" ") +
		sidebarStyle(rowBg, fg).Render(m.sidebarMarquee("w:"+node.ID, title, max(cw-indent-3-rightW, 1), hovered))
	if rightW > 0 {
		gap := max(cw-lipgloss.Width(row)-rightW, 0)
		row += sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", gap)) + right
	}
	return sidebarFit(row, cw, rowBg)
}

// windowWorkspace is the workspace a rail row's window sits on, or 0 when this
// client does not hold it (a pane of a session it is not attached to, whose
// workspace is not on the wire).
func (m *OS) windowWorkspace(id string) int {
	if i := m.windowIndexByID(id); i >= 0 {
		return m.Windows[i].Workspace
	}
	return 0
}

// sidebarWindowGlyph is the one cell in front of a window's name: its agent
// state when it has one, else the accent the user gave it. State outranks
// identity, so a working pane keeps its state dot and the accent waits.
func (m *OS) sidebarWindowGlyph(node sessiontree.Node, rowBg color.Color, pal overlay.Palette) string {
	if node.AgentState == "" && config.SidebarShowGlyphs {
		if idx, ok := m.WindowAccent(node.ID); ok {
			return sidebarStyle(rowBg, accentColor(idx)).Render(accentMark())
		}
	}
	return sidebarGlyph(node.AgentState, node.DoneSeen, rowBg, pal)
}

// sidebarAgentRow renders one row of the running-agents section: state glyph,
// pane name (session-qualified when the pane lives in another session), and,
// in the full variant, the state word right-aligned and muted.
func (m *OS) sidebarAgentRow(e sidebarAgentEntry, variant int, cw int, pal overlay.Palette, hovered bool) string {
	rowBg := sidebarAttentionTint(e.State, pal)
	fg := pal.FgDim
	if e.State == "done" && !e.DoneSeen {
		fg = pal.Fg
	}
	if hovered {
		rowBg = pal.RowSel
		fg = pal.Fg
	}

	name := printableTitle(e.Title)
	if name == "" {
		name = "shell"
	}
	if e.Foreign {
		name = printableTitle(e.SessionID) + "/" + name
	}

	// How long the pane has been in this state, in place of a state word: the
	// glyph, colour and sort position already say which state it is, while the
	// duration is the part nothing else carries. A pane waiting twenty minutes
	// on input reads very differently from one that just asked.
	label := ""
	labelW := 0
	if variant == sidebarVariantFull {
		label = agentElapsed(e.State, e.StateAt, time.Now())
		if label != "" {
			labelW = lipgloss.Width(label) + 1
		}
	}

	// An agent row points at a pane, so it takes a window row's columns: same
	// glyph column, same name spine, read straight down the rail.
	indent := sidebarPaneIndent(variant)
	avail := max(cw-indent-2-labelW-1, 1)
	nameStyle := sidebarStyle(rowBg, fg)
	timeFg := pal.FgMute
	if sidebarAttention(e.State) {
		// The only bold text in the section, so the rows that want a human still
		// win on a monochrome capture where the tint and glyph colour are gone.
		nameStyle = nameStyle.Bold(true)
		timeFg = sidebarStateColor(e.State, e.DoneSeen, pal)
	}
	row := sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent)) +
		sidebarGlyph(e.State, e.DoneSeen, rowBg, pal) +
		sidebarStyle(rowBg, nil).Render(" ") +
		nameStyle.Render(m.sidebarMarquee("a:"+e.SessionID+"/"+e.WindowID, name, avail, hovered))
	if label != "" {
		gap := max(cw-lipgloss.Width(row)-labelW, 0)
		row += sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", gap)) +
			sidebarStyle(rowBg, timeFg).Render(label) +
			sidebarStyle(rowBg, nil).Render(" ")
	}
	return sidebarFit(row, cw, rowBg)
}
