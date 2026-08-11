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
// the panes, and emphasis is carried by the same pills the dock uses. Two
// sections share the rail: the panes currently running an agent at the top,
// priority-ordered, and the session tree below them. Attention leads because
// the first question a rail full of agents has to answer is "which one needs
// me", not "what exists"; the tree keeps every behaviour it had, one section
// lower.
//
// Emphasis is spent on two things and no more, and neither of them paints a
// standing row. "This is the current one" (the attached session, the focused
// pane) is an accent mark in the rail's one-cell gutter, the same mark in both
// places; a state wanting a human takes the same cell in its severity colour,
// plus the rail's one bold. That leaves the only full-width band on the rail to
// "this is where the cursor or the pointer is", which is the thing the user is
// steering and was previously the least visible mark on screen.

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
	// sidebarRowNewSession is the "+ new" control in the rail's footer. It
	// targets nothing that exists yet.
	sidebarRowNewSession
	// sidebarRowCollapse is the footer's width stepper. Like the band chips it
	// is narrower than its line, so it carries its own columns.
	sidebarRowCollapse
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
// the only ones allowed a severity gutter mark, the rail's one bold, or an
// inked cell on the glyph rail: reserving those for the two states is what
// keeps them legible as an alarm.
func sidebarAttention(state string) bool {
	return state == "needs_input" || state == "errored"
}

// sidebarSeverityColor is the colour a state that wants a human is marked in.
func sidebarSeverityColor(state string, pal overlay.Palette) color.Color {
	switch state {
	case "needs_input":
		return pal.Warning
	case "errored":
		return pal.Warn
	default:
		return nil
	}
}

// sidebarGutter is column 0 of every rail row above the glyph width: one cell
// saying either "this is where you are" (accent) or "this one wants a human"
// (severity), and nothing at all otherwise.
//
// It replaces the full-width bands those two states used to stand on. Three
// stacked tinted rows before the user has touched anything read as zebra
// striping rather than emphasis, because they ran to the rail edge over
// trailing whitespace; and the cursor, the one thing being steered, was the
// quietest mark on the rail. A margin strip scans without painting, which frees
// the only band on a resting screen for the pointer and the keyboard cursor.
func sidebarGutter(current bool, state string, bg color.Color, pal overlay.Palette) string {
	mark, ascii := "▎", overlay.UseASCII()
	switch {
	case current:
		if ascii {
			mark = ">"
		}
		return sidebarStyle(bg, pal.Accent).Render(mark)
	case sidebarAttention(state):
		if ascii {
			mark = "!"
		}
		return sidebarStyle(bg, sidebarSeverityColor(state, pal)).Render(mark)
	default:
		return sidebarStyle(bg, nil).Render(" ")
	}
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
	// One indent at every width. A session row is chevron, space, glyph at every
	// variant, so its name always starts three cells in; narrowing panes to two
	// in the narrow rail put their titles a column left of the names above them,
	// which is the ragged edge you see before you can name it.
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

// sidebarEdgeRule is the one-cell vertical rule separating the rail from the
// panes, drawn in the window-border character at the dock separator's color:
// the rail's edge is the vertical sibling of the dock's hairline.
func sidebarEdgeRule() string {
	return lipgloss.NewStyle().Foreground(theme.NotificationRule()).Render(config.GetWindowBorderLeft())
}

// sidebarHeaderRow renders a quiet section header: the label, lowercase and
// muted, so it frames its section without competing with it. Lowercase and
// unbolded because a header is furniture: the rail spends its one bold voice on
// a row that wants a human, and spending it here would rank a label above them.
// It carries no count on purpose, because the number only restated the rows
// printed directly underneath it, and the agents section already owns up to
// what it hides with its own "+N" line.
func sidebarHeaderRow(label string, cw int, pal overlay.Palette) string {
	row := sidebarStyle(nil, nil).Render(" ") +
		sidebarStyle(nil, pal.FgMute).Render(overlay.Truncate(label, max(cw-2, 1)))
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
	if config.SidebarWorkspaces == config.SidebarWorkspacesOff {
		return "", nil
	}
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
			fg, rowBg = pal.Fg, pal.Surface
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
	if variant != sidebarVariantGlyph && config.SidebarShowAgents {
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
		// Header plus a blank row. The hairline that used to close the section
		// was the third rule on one screen, beside the rail edge and the dock
		// separator; empty space separates just as well and says nothing.
		agentSection = agentShown + 2
	}
	overflow := len(agents) - agentShown
	agentRows := agentShown
	if overflow > 0 {
		agentRows--
	}

	// The footer is pinned to the rail's last lines, so the tree scrolls above
	// it rather than under it.
	canCreate := m.SidebarCanCreateSession()
	footerCursor := func(kind sidebarRowKind) bool { return isCursor(kind, "", "") }
	footerLines, footerZones := m.sidebarFooter(variant, cw, pal, canCreate, -1, -1, footerCursor)
	footerH := len(footerLines)
	// A rail with no room for both gives its lines to the list: the footer holds
	// controls that have keys, while the rows are the only thing the rail cannot
	// say any other way.
	if footerH >= height {
		footerLines, footerZones, footerH = nil, nil, 0
	}
	treeH := max(height-agentSection-footerH, 0)

	// Hover, derived from the last motion seen inside the band. Rows are one
	// screen line each, so the hovered agent row is the pointer's distance from
	// the top less the section header, and the hovered tree row is the scroll
	// offset plus its distance past the whole agents block. Hover yields
	// entirely to a drag.
	treeHover, agentHover := -1, -1
	footerHoverLine, footerHoverX := -1, -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		delta := m.SidebarHoverY - topMargin
		footerTop := height - footerH
		switch {
		case footerH > 0 && delta >= footerTop && delta < height:
			footerHoverLine, footerHoverX = delta-footerTop, m.SidebarHoverX-contentX0
		case delta < agentSection:
			if d := delta - 1; d >= 0 && d < agentRows {
				agentHover = d
			}
		default:
			treeHover = m.SidebarScroll + delta - agentSection
		}
	}
	// Re-rendered now the pointer is resolved; the first pass only measured how
	// many lines the footer takes so the tree region could be sized.
	if footerH > 0 {
		footerLines, footerZones = m.sidebarFooter(variant, cw, pal, canCreate, footerHoverLine, footerHoverX, footerCursor)
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
		rows = append(rows, logicalRow{text: sidebarHeaderRow("sessions", cw, pal)})
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
		lines = append(lines, compose(sidebarHeaderRow("agents", cw, pal)))
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
		lines = append(lines, blank)
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
	for len(lines) < height-footerH {
		lines = append(lines, blank)
	}

	// The footer last, on the rail's own bottom lines. Its zones are recorded
	// from the columns it was drawn on and its nav rows are appended in the same
	// order, so the two stay index-for-index with each other and with the screen.
	footerTop := topMargin + len(lines)
	for _, z := range footerZones {
		y := footerTop + z.Line
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: contentX0 + z.X0, X1: contentX0 + z.X1,
			Y0: y, Y1: y + 1,
			Kind:        z.Kind,
			WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: z.Kind, WindowIndex: -1})
	}
	for _, ln := range footerLines {
		lines = append(lines, compose(ln))
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

// sidebarFooterZone is one control in the rail's footer: its kind and the
// content-relative columns it was drawn on. Two zones can share a line, so the
// footer hit-tests per zone rather than claiming the whole row.
type sidebarFooterZone struct {
	Kind   sidebarRowKind
	Line   int // index into the footer's own lines
	X0, X1 int
}

// sidebarCollapseGlyph is the footer stepper's mark and the preferred width it
// would step to, or ok false when the rail cannot move at this render width.
// The rail narrows on the way down (full, narrow, glyph) and goes straight back
// to the configured default on the way up, which is the width the user picked
// the last time they sized it.
func (m *OS) sidebarCollapseGlyph(variant int) (glyph string, target int, ok bool) {
	down, up := "«", "»"
	if overlay.UseASCII() {
		down, up = "<<", ">>"
	}
	switch variant {
	case sidebarVariantFull:
		return down, config.SidebarNarrowWidth, true
	case sidebarVariantNarrow:
		return down, config.SidebarGlyphWidth, true
	default:
		// Only offer the step back up when the screen has room to honour it;
		// a control that provably cannot move is noise.
		target = max(config.SidebarDefaultWidth, config.SidebarNarrowWidth)
		return up, target, sidebarVariant(m.sidebarWidthFor(target)) > variant
	}
}

// sidebarFooter renders the rail's pinned bottom rows: the new-session control
// on the left and the width stepper on the right, both meta voice on the bare
// canvas. Controls live down here rather than among the rows because they are
// not things the rail is listing, and a control dressed as a session row read
// as one. They share a line wherever both fit; the glyph rail gives them one
// each, which is all a two-cell rail can do.
func (m *OS) sidebarFooter(variant, cw int, pal overlay.Palette, canCreate bool,
	hoverLine, hoverX int, isCursor func(sidebarRowKind) bool,
) ([]string, []sidebarFooterZone) {
	stepGlyph, _, canStep := m.sidebarCollapseGlyph(variant)
	if !canCreate && !canStep {
		return nil, nil
	}

	newLabel := "+"
	if variant != sidebarVariantGlyph {
		newLabel = "+ new"
	}
	newW, stepW := lipgloss.Width(newLabel), lipgloss.Width(stepGlyph)

	// One line when both fit with a cell of air between them, otherwise one
	// line each.
	oneLine := !canCreate || !canStep || 1+newW+1+stepW+1 <= cw

	type placed struct {
		zone  sidebarFooterZone
		label string
	}
	var items []placed
	line := 0
	if canCreate {
		items = append(items, placed{sidebarFooterZone{Kind: sidebarRowNewSession, Line: line, X0: 1, X1: 1 + newW}, newLabel})
		if !oneLine {
			line++
		}
	}
	if canStep {
		x0 := max(cw-1-stepW, 1)
		if !oneLine {
			x0 = 1
		}
		items = append(items, placed{sidebarFooterZone{Kind: sidebarRowCollapse, Line: line, X0: x0, X1: x0 + stepW}, stepGlyph})
	}

	// Cell-addressed rather than spliced into a rendered string: two zones share
	// a line, and the first one's escape sequences would make byte offsets lie
	// to the second.
	cells := make([][]string, line+1)
	for i := range cells {
		cells[i] = make([]string, cw)
		for c := range cells[i] {
			cells[i][c] = " "
		}
	}
	zones := make([]sidebarFooterZone, 0, len(items))
	for _, it := range items {
		fg := pal.FgMute
		if (it.zone.Line == hoverLine && hoverX >= it.zone.X0 && hoverX < it.zone.X1) || isCursor(it.zone.Kind) {
			fg = pal.Fg
		}
		row := cells[it.zone.Line]
		row[it.zone.X0] = sidebarStyle(nil, fg).Render(it.label)
		for c := it.zone.X0 + 1; c < it.zone.X1 && c < cw; c++ {
			row[c] = ""
		}
		zones = append(zones, it.zone)
	}

	lines := make([]string, len(cells))
	for i, row := range cells {
		lines[i] = sidebarFit(strings.Join(row, ""), cw, nil)
	}
	return lines, zones
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
//	▎▸ ● name           3
//	^^ ^ ^              ^ window count, right-aligned, muted, collapsed only
//	|| | name: full strength on the attached session, dim on the rest
//	|| rolled-up agent glyph, state-colored
//	|expand chevron, muted; blank when there is nothing to expand
//	gutter: accent when attached, severity when a pane wants a human
//
// Emphasis ladder, quietest to loudest: other rows dim; attached session an
// accent gutter mark and a full-strength name; pointer or keyboard cursor a
// Surface band; a state wanting a human a severity gutter mark, a coloured
// glyph and the rail's one bold. No standing fill, so the only band on a
// resting rail is the one under the pointer.
//
// A drag in progress keeps the band on the dragged row while it rides the
// pointer.
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
		leadFg := pal.FgMute
		switch {
		case sidebarAttention(node.AgentState) && config.SidebarShowGlyphs:
			// Two cells is too little room for a coloured glyph to carry an
			// alarm, so at this width the cell itself is inked and the glyph is
			// knocked out of it. This outranks the current-session fill: the
			// whole job of a three-column rail is saying which session wants you.
			// The count is knocked out with it, or it would be a muted mark on
			// a saturated fill.
			bg, fg = agentGlyphColor(node.AgentState, pal), pal.Canvas
			leadFg = pal.Canvas
		case node.IsCurrent:
			bg = pal.Surface
		case hovered || dragged:
			bg = pal.RowSel
		}
		if fg == nil {
			fg = pal.FgMute
			if config.SidebarShowGlyphs && agentStateIndicator(node.AgentState) != "" {
				fg = sidebarStateColor(node.AgentState, node.DoneSeen, pal)
			}
		}
		// The spare cell carries the window count rather than a blank: at three
		// columns "how many panes" is the only other thing worth saying, and a
		// digit beside a state glyph is the whole rail in two cells. Blank for a
		// single-window session, where the count says nothing.
		lead := " "
		if config.SidebarShowCounts && node.WindowCount > 1 {
			lead = strconv.Itoa(node.WindowCount)
			if node.WindowCount > 9 {
				lead = "+"
			}
		}
		row := sidebarStyle(bg, leadFg).Render(lead) + sidebarStyle(bg, fg).Render(mark)
		return sidebarFit(row, cw, bg)
	}

	// The only band left on the rail is the transient one: pointer or keyboard
	// cursor. It sits on Surface rather than RowSel, which was one luminance
	// step off the canvas and left the cursor all but invisible. Identity and
	// attention moved to the gutter.
	var rowBg color.Color
	if hovered || dragged {
		rowBg = pal.Surface
	}

	chevron := " "
	if config.SidebarShowWindows && len(node.Children) > 0 {
		chevron = sidebarChevron(expanded)
	}

	// Gutter, chevron, a space, the state glyph, then one cell of inset: the
	// name lands on column 5, the spine the pane rows below it use, and the
	// glyph on column 3 with theirs.
	left := sidebarGutter(node.IsCurrent, node.AgentState, rowBg, pal) +
		sidebarStyle(rowBg, pal.FgMute).Render(chevron) +
		sidebarStyle(rowBg, nil).Render(" ") +
		sidebarGlyph(node.AgentState, node.DoneSeen, rowBg, pal)

	right := ""
	rightW := 0
	// Only a collapsed session counts its windows. Expanded, the rows are printed
	// directly underneath and the eye can count them, while the digit sat in the
	// same column as the window rows' own digits and meant something else.
	if config.SidebarShowCounts && node.WindowCount > 0 && variant == sidebarVariantFull && !expanded {
		countStr := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(countStr) +
			sidebarStyle(rowBg, nil).Render(" ")
		rightW = lipgloss.Width(countStr) + 1
	}

	// The attached session's name reads at full strength; the rest are dim. A
	// state wanting a human takes the rail's one bold voice, so it still leads
	// on a monochrome capture where the gutter colour is gone.
	fg := pal.FgDim
	if node.IsCurrent || hovered || dragged {
		fg = pal.Fg
	}
	nameStyle := sidebarStyle(rowBg, fg).Bold(sidebarAttention(node.AgentState))
	name := m.sidebarMarquee("s:"+node.ID, printableTitle(node.Title),
		max(cw-rightW-8, 1), hovered)
	row := left + sidebarStyle(rowBg, nil).Render(" ") +
		nameStyle.Render(name) + sidebarStyle(rowBg, nil).Render("  ")
	gap := max(cw-lipgloss.Width(row)-rightW, 0)
	row += sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", gap)) + right
	return sidebarFit(row, cw, rowBg)
}

// sidebarWindowRow renders one window row under an expanded session, indented
// one level past the session name. The focused window wears the accent gutter
// mark and reads at full strength; the rest stay dim so the list reads as
// detail under its session, not as a second list of equals.
func (m *OS) sidebarWindowRow(node sessiontree.Node, variant int, cw int, pal overlay.Palette, hovered bool) string {
	title := printableTitle(node.Title)
	if title == "" {
		title = "shell"
	}

	// One level past the session's glyph column, so a window title lands on the
	// same spine as its session name.
	indent := sidebarPaneIndent(variant)

	// Same as a session row: the only band is the transient one under the
	// pointer or the keyboard cursor. Focus and attention are gutter marks.
	var rowBg color.Color
	if hovered {
		rowBg = pal.Surface
	}

	fg := pal.FgDim
	switch {
	case node.IsCurrent:
		fg = pal.Fg
	case node.AgentState == "done" && !node.DoneSeen:
		// Unseen work reads at full strength; seeing it is what dims it.
		fg = pal.Fg
	}

	// A pane on another workspace is here for orientation, not for reading: it
	// goes a step further down and names the workspace it is on, so the row
	// answers "where did it go" without a switch to find out.
	elsewhere := 0
	if ws := m.windowWorkspace(node.ID); ws > 0 && ws != m.CurrentWorkspace {
		elsewhere = ws
		fg = pal.FgMute
	}

	if hovered {
		fg = pal.Fg
	}

	right, rightW := "", 0
	if elsewhere > 0 {
		// "w4" rather than a bare "4": a plain digit in this column is a session
		// row's window count, so on adjacent lines the same mark meant two things.
		tag := "w" + strconv.Itoa(elsewhere)
		right = sidebarStyle(rowBg, pal.FgMute).Render(tag) + sidebarStyle(rowBg, nil).Render(" ")
		rightW = lipgloss.Width(tag) + 1
	}

	row := sidebarGutter(node.IsCurrent, node.AgentState, rowBg, pal) +
		sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent-1)) +
		m.sidebarWindowGlyph(node, rowBg, pal) +
		sidebarStyle(rowBg, nil).Render(" ") +
		sidebarStyle(rowBg, fg).Bold(sidebarAttention(node.AgentState)).
			Render(m.sidebarMarquee("w:"+node.ID, title, max(cw-indent-3-rightW, 1), hovered))
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
	var rowBg color.Color
	fg := pal.FgDim
	if e.State == "done" && !e.DoneSeen {
		fg = pal.Fg
	}
	if hovered {
		rowBg = pal.Surface
		fg = pal.Fg
	}

	name := printableTitle(e.Title)
	if name == "" {
		name = "shell"
	}
	// A pane in another session carries that session as a prefix. It is context,
	// not the answer, so it renders muted against the full-strength pane name and
	// gives its cells up first when the row runs out of room.
	prefix := ""
	if e.Foreign {
		if s := printableTitle(e.SessionID); s != "" {
			prefix = s + "/"
		}
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
	// The prefix yields before the name does: with room for both it is drawn in
	// full, and it is dropped entirely before a single cell of the pane name goes.
	shown := prefix
	if lipgloss.Width(shown)+2 > avail {
		shown = ""
	}
	// An agent row is only ever "current" through the pane it points at, which
	// the tree section below already marks, so its gutter carries severity only.
	row := sidebarGutter(false, e.State, rowBg, pal) +
		sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent-1)) +
		sidebarGlyph(e.State, e.DoneSeen, rowBg, pal) +
		sidebarStyle(rowBg, nil).Render(" ") +
		sidebarStyle(rowBg, pal.FgMute).Render(shown) +
		nameStyle.Render(m.sidebarMarquee("a:"+e.SessionID+"/"+e.WindowID, name, max(avail-lipgloss.Width(shown), 1), hovered))
	if label != "" {
		gap := max(cw-lipgloss.Width(row)-labelW, 0)
		row += sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", gap)) +
			sidebarStyle(rowBg, timeFg).Render(label) +
			sidebarStyle(rowBg, nil).Render(" ")
	}
	return sidebarFit(row, cw, rowBg)
}
