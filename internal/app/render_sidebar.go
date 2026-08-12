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
// the panes, and emphasis is carried by the same pills the dock uses.
//
// Three flat sections share the rail, top to bottom: the sessions that exist,
// the terminals of the one being looked at, and the agents wanting a human.
// Agents pin to the bottom so the alarm block sits at a stable screen position
// at any rail height, and the slack rides above it. The session->window tree
// this replaces indented, which cost three separate name spines; flat sections
// land the whole rail on one: gutter col 0, glyph col 1, text col 3, and any
// right-aligned figure inset one cell from the rail's own edge.
//
// Emphasis is spent on two things and no more, and neither of them paints a
// standing row. "This is the current one" (the attached session, the focused
// pane) is an accent mark in the rail's one-cell gutter, the same mark in both
// places; a state wanting a human takes the same cell in its severity colour,
// plus the rail's one bold. That leaves the only full-width band on the rail to
// "this is where the cursor or the pointer is", which is the thing the user is
// steering.

// sidebarRowKind distinguishes what a sidebar row points at for mouse routing.
type sidebarRowKind int

const (
	sidebarRowSession sidebarRowKind = iota
	// sidebarRowWindow is a row of the terminals section: one pane of the
	// session currently being shown there.
	sidebarRowWindow
	// sidebarRowAgent is a row in the agents section; it targets a window
	// exactly like sidebarRowWindow, it just lives in the other section.
	sidebarRowAgent
	// sidebarRowAgentFilter is the all/here token in the agents header, and the
	// hint row a filter that hides everything leaves behind. Both cycle the
	// filter, which is the only thing either of them can usefully mean.
	sidebarRowAgentFilter
	// sidebarRowAgentSort is the pri/rec token beside it. Like the footer's
	// controls these two are narrower than their line, so they carry their own
	// columns rather than claiming the whole header.
	sidebarRowAgentSort
	// sidebarRowNewSession is the "+ new" control in the rail's footer. It
	// targets nothing that exists yet.
	sidebarRowNewSession
	// sidebarRowCollapse is the footer's collapse toggle. Like the footer's
	// other control it is narrower than its line, so it carries its own columns.
	sidebarRowCollapse
)

// sidebarSection identifies one of the rail's three stacked lists. Each owns
// its own scroll offset and its own band of screen lines, so the wheel scrolls
// the one under the pointer and neither header can be scrolled away.
type sidebarSection int

const (
	sidebarSectionSessions sidebarSection = iota
	sidebarSectionTerminals
	sidebarSectionAgents
	sidebarSectionCount
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
	return sidebarGutterTinted(current, state, nil, bg, pal)
}

// sidebarGutterTinted is sidebarGutter with the current-mark drawn in a colour
// of the caller's choosing: the focused pane's gutter burns the accent the user
// gave that pane, so the row wears exactly one identity bar instead of an
// accent chip beside a focus mark. tint nil falls back to the rail accent.
func sidebarGutterTinted(current bool, state string, tint, bg color.Color, pal overlay.Palette) string {
	mark, ascii := "▎", overlay.UseASCII()
	switch {
	case current:
		if ascii {
			mark = ">"
		}
		if tint == nil {
			tint = pal.Accent
		}
		return sidebarStyle(bg, tint).Render(mark)
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
	// SessionLabel is what to print for SessionID: the session's display name
	// when it has one. Identity keys the row, the label only fronts it.
	SessionLabel string
	// Foreign marks a pane of a session other than the attached one, whose row
	// carries the session name for context.
	Foreign bool
}

// sidebarTerminalEntry is one pane of the session the terminals section is
// showing, whether that is the attached session or a peeked one.
type sidebarTerminalEntry struct {
	SessionID string
	WindowID  string
	Title     string
	State     string
	DoneSeen  bool
	Focused   bool
	// Tag is the quiet right-hand mark saying which workspace the pane sits on,
	// empty for a pane on the session's own current workspace and for one whose
	// workspace this client cannot know. Resolved where the session's context is
	// still in hand, so the row itself does not have to go looking for it.
	Tag string
	// WindowIndex is the index into m.Windows, or -1 for a pane of a session
	// this client is not attached to.
	WindowIndex int
	// workspace orders the section (workspace, then the session's own pane
	// order). It draws nothing; Tag is what a row prints.
	workspace int
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
// indicators. They sit inside the decorative blocks printableTitle strips, so
// they are named rather than kept by range.
var chromeGlyphs = map[rune]bool{
	'●': true, '▲': true, '○': true, '■': true, // agentStateIndicator
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

// sidebarNameCol is the column every rail row's text starts on: gutter, glyph,
// one cell of air. One spine for all three sections, which is what the flat
// layout buys over the tree.
const sidebarNameCol = 3

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

// sidebarQuietDot is the placeholder a session row puts in its glyph column
// when nothing in it is running an agent: the column stays occupied, so the
// names below it never step left and the section reads as one list.
func sidebarQuietDot(bg color.Color, pal overlay.Palette) string {
	if !config.SidebarShowGlyphs {
		return sidebarStyle(bg, nil).Render(" ")
	}
	dot := "·"
	if overlay.UseASCII() {
		dot = "."
	}
	return sidebarStyle(bg, pal.FgMute).Render(dot)
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
// printed directly underneath it, and a capped section already owns up to what
// it hides with its own "+N" line.
//
// right is an already-styled trailing element (the peeked session's name, the
// agents section's controls), inset one cell from the rail's edge so it lands
// on the same spine the rows' figures do.
func sidebarHeaderRow(label, right string, cw int, pal overlay.Palette) string {
	row := sidebarStyle(nil, nil).Render(" ") +
		sidebarStyle(nil, pal.FgMute).Render(overlay.Truncate(label, max(cw-2, 1)))
	if rw := lipgloss.Width(right); rw > 0 {
		gap := max(cw-lipgloss.Width(row)-rw-1, 0)
		row += strings.Repeat(" ", gap) + right + " "
	}
	return sidebarFit(row, cw, nil)
}

// sidebarAgentsHeaderW is the columns the "agents" label occupies, its leading
// inset included. The header's controls refuse to draw over it.
const sidebarAgentsHeaderW = 7

// sidebarTokenSpan is one clickable token inside a header row, in
// content-relative columns. Several share a line, so the header hit-tests per
// token rather than claiming the whole row, exactly as the footer does.
type sidebarTokenSpan struct {
	Kind   sidebarRowKind
	X0, X1 int
}

// sidebarAgentsControls renders the agents header's filter and sort tokens,
// right-aligned in meta voice, and says where each landed so the renderer can
// publish a rectangle for it. A token at its default value reads FgMute, so the
// header stays silent until a control is actually biting; a non-default one
// reads Fg, which is the whole reason the section's shape is not a mystery.
//
// Returns nothing when the header has no room for both tokens: half a control
// is half a click target.
func (m *OS) sidebarAgentsControls(cw, headerW int, pal overlay.Palette, hoverX int) (string, []sidebarTokenSpan) {
	filter, sort := "all", "pri"
	filterOn, sortOn := false, false
	if m.sidebarAgentsFilter() == sidebarAgentsSession {
		filter, filterOn = "here", true
	}
	if m.sidebarAgentsSort() == sidebarAgentsRecent {
		sort, sortOn = "rec", true
	}
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}

	fw, sw := lipgloss.Width(filter), lipgloss.Width(sort)
	total := fw + lipgloss.Width(sep) + sw
	x0 := cw - 1 - total
	if x0 < headerW+1 {
		return "", nil
	}
	spans := []sidebarTokenSpan{
		{Kind: sidebarRowAgentFilter, X0: x0, X1: x0 + fw},
		{Kind: sidebarRowAgentSort, X0: x0 + fw + lipgloss.Width(sep), X1: x0 + total},
	}
	ink := func(on bool, s sidebarTokenSpan) color.Color {
		if on || (hoverX >= s.X0 && hoverX < s.X1) {
			return pal.Fg
		}
		return pal.FgMute
	}
	return sidebarStyle(nil, ink(filterOn, spans[0])).Render(filter) +
		sidebarStyle(nil, pal.FgMute).Render(sep) +
		sidebarStyle(nil, ink(sortOn, spans[1])).Render(sort), spans
}

// sidebarComposeRow assembles one rail row on the single spine: gutter, glyph,
// a cell of air, the name, and an optional right-aligned figure inset one cell
// from the rail's edge. Every piece arrives already styled; name must already
// be truncated to sidebarNameAvail so the fit below cannot eat the figure.
func sidebarComposeRow(gutter, glyph, name, right string, cw int, bg color.Color) string {
	row := gutter + glyph + sidebarStyle(bg, nil).Render(" ") + name
	if rw := lipgloss.Width(right); rw > 0 {
		gap := max(cw-lipgloss.Width(row)-rw-1, 0)
		row += sidebarStyle(bg, nil).Render(strings.Repeat(" ", gap)) +
			right + sidebarStyle(bg, nil).Render(" ")
	}
	return sidebarFit(row, cw, bg)
}

// sidebarNameAvail is how many cells a row's name may take: everything between
// the spine and the right-aligned figure's inset.
func sidebarNameAvail(cw, rightW int) int {
	if rightW > 0 {
		rightW++ // the inset cell
	}
	return max(cw-sidebarNameCol-rightW, 1)
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

// sidebarBudget divides avail lines between the three sections, per the design's
// table: sessions are content-sized up to about a quarter of the rail, agents up
// to about a third, and the terminals section takes the slack because it is the
// list the user actually works in. A rail too short for the terminals floor
// shrinks agents first, then sessions, and never below their own floors.
func sidebarBudget(avail, nS, nT, nA int) (sH, tH, aH int) {
	avail = max(avail, 0)
	sFloor, tFloor, aFloor := min(nS, 2), min(nT, 3), min(nA, 2)

	sH = min(nS, max(avail/4, sFloor))
	aH = min(nA, max(avail/3, aFloor))
	tH = avail - sH - aH
	if tH < tFloor {
		aH = max(aFloor, aH-(tFloor-tH))
		tH = avail - sH - aH
		if tH < tFloor {
			sH = max(sFloor, sH-(tFloor-tH))
			tH = avail - sH - aH
		}
	}
	tH = max(min(tH, nT), 0)

	// The floors can still overrun a rail with almost no lines at all, so the
	// last word belongs to the space that exists: give it up from the quietest
	// section outwards.
	for sH+tH+aH > avail {
		switch {
		case aH > 0:
			aH--
		case sH > 0:
			sH--
		default:
			tH--
		}
	}
	return sH, tH, aH
}

// sidebarWindowSection windows one section's rows onto the lines it was given,
// returning the first row to draw and how many, plus how many rows are hidden
// below the fold. A section that does not fit spends its last line on "… +N",
// except at the bottom of its own scroll where there is nothing left to own up
// to and the line goes back to being a row: that is what keeps the last row
// reachable by wheel.
func sidebarWindowSection(scroll, rows, lines int) (start, shown, hidden int) {
	if lines <= 0 || rows <= 0 {
		return 0, 0, 0
	}
	if rows <= lines {
		return 0, rows, 0
	}
	maxScroll := rows - lines
	start = max(min(scroll, maxScroll), 0)
	if start == maxScroll {
		return start, lines, 0
	}
	return start, lines - 1, rows - start - (lines - 1)
}

// sidebarPanelLinesForTree lays the rail out for a given tree and records the
// on-screen hit geometry of every row into m.SidebarHits, returning the rows
// and the reserved width. It returns nil rows when the sidebar reserves
// nothing.
//
// Every emitted line is exactly the reserved width: the content columns plus
// the one-cell edge rule on the side facing the panes.
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

	if variant == sidebarVariantGlyph {
		return m.sidebarStripLines(sessions, w, cw, height, topMargin, sidebarX, contentX0, pal, compose, blank)
	}

	// The keyboard cursor tracks a row by identity, not by index, so it survives a
	// relayout: the target is the session the last action asked to follow, else
	// the nav row the cursor was on last frame. Rows matching it draw the same
	// band hover uses; the two share one cursor.
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

	// The three lists, built before anything is drawn: the budget needs their
	// counts, and hover has to resolve against the same arithmetic the draw uses.
	shown, peeking := m.sidebarShownSession(sessions)
	terminals := m.sidebarTerminals(sessions, shown)
	agents, agentsTotal := m.sidebarFilterAgents(m.sidebarAgents(sessions, variant))
	m.sidebarSortAgents(agents)
	// A filter that hides everything leaves one row saying so and offering the
	// way back, because a section that vanished on a control the user set two
	// days ago reads as "no agents anywhere", which is the opposite of the truth.
	emptyFilter := len(agents) == 0 && agentsTotal > 0

	nS := len(sessions)
	nT := len(terminals)
	nA := len(agents)
	if emptyFilter {
		nA = 1
	}
	// A peeked session with no panes says so, or the section would read as "the
	// attached session has no panes".
	emptyPeek := peeking && nT == 0
	if emptyPeek {
		nT = 1
	}
	showTerminals := config.SidebarShowWindows && nT > 0
	if !showTerminals {
		nT = 0
	}
	// The current rule, kept: a rail this short cannot carry an alarm block and
	// a working list both, and the working list is the one with no other home.
	if height < 8 {
		nA = 0
	}

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

	chrome := 1 // the sessions header
	if nT > 0 {
		chrome++
	}
	if nA > 0 {
		chrome += 2 // the gap that floats the agents block, plus its header
	}
	sH, tH, aH := sidebarBudget(height-footerH-chrome, nS, nT, nA)
	slack := max(height-footerH-chrome-sH-tH-aH, 0)

	// Where each section's lines land, in the rail's own coordinates. Computed
	// before any row is rendered so hover resolves against the draw's arithmetic
	// rather than a second copy of it.
	var place [sidebarSectionCount]struct{ header, top, lines, y0, y1 int }
	line := 0
	place[sidebarSectionSessions] = struct{ header, top, lines, y0, y1 int }{line, line + 1, sH, line, line + 1 + sH}
	line += 1 + sH
	place[sidebarSectionTerminals] = struct{ header, top, lines, y0, y1 int }{-1, line, 0, line, line}
	if nT > 0 {
		place[sidebarSectionTerminals] = struct{ header, top, lines, y0, y1 int }{line, line + 1, tH, line, line + 1 + tH + slack}
		line += 1 + tH
	}
	line += slack
	place[sidebarSectionAgents] = struct{ header, top, lines, y0, y1 int }{-1, line, 0, line, line}
	if nA > 0 {
		place[sidebarSectionAgents] = struct{ header, top, lines, y0, y1 int }{line + 1, line + 2, aH, line, line + 2 + aH}
		line += 2 + aH
	}
	for s := range m.sidebarSectionY {
		m.sidebarSectionY[s] = [2]int{topMargin + place[s].y0, topMargin + place[s].y1}
	}

	// Keyboard cursor auto-scroll, per section: a cursor past a section's fold
	// scrolls that section the way a wheel would, and never disturbs the others.
	scroll := [sidebarSectionCount]*int{&m.SidebarScrollS, &m.SidebarScrollT, &m.SidebarScrollA}
	rowsIn := [sidebarSectionCount]int{nS, nT, nA}
	if m.SidebarFocused && haveCursorTarget {
		if sec, idx, ok := m.sidebarCursorIndex(cursorTarget, sessions, terminals, agents); ok {
			if lines := place[sec].lines; lines > 0 {
				if idx < *scroll[sec] {
					*scroll[sec] = idx
				} else if idx >= *scroll[sec]+lines {
					*scroll[sec] = idx - lines + 1
				}
			}
		}
	}
	var start, count, hidden [sidebarSectionCount]int
	for s := range rowsIn {
		start[s], count[s], hidden[s] = sidebarWindowSection(*scroll[s], rowsIn[s], place[s].lines)
		*scroll[s] = start[s]
	}

	// Hover, derived from the last motion seen inside the band, resolved against
	// the placement above. Hover yields entirely to a drag.
	var hoverRow [sidebarSectionCount]int
	for s := range hoverRow {
		hoverRow[s] = -1
	}
	footerHoverLine, footerHoverX := -1, -1
	// The agents header carries two click targets of its own, so the pointer's
	// column on that one line matters as well as which line it is on.
	agentsHeaderHoverX := -1
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		delta := m.SidebarHoverY - topMargin
		footerTop := height - footerH
		switch {
		case footerH > 0 && delta >= footerTop && delta < height:
			footerHoverLine, footerHoverX = delta-footerTop, m.SidebarHoverX-contentX0
		case nA > 0 && delta == place[sidebarSectionAgents].header:
			agentsHeaderHoverX = m.SidebarHoverX - contentX0
		default:
			for s := range place {
				if d := delta - place[s].top; d >= 0 && d < count[s] {
					hoverRow[s] = start[s] + d
				}
			}
		}
	}
	// Re-rendered now the pointer is resolved; the first pass only measured how
	// many lines the footer takes so the sections could be sized.
	if footerH > 0 {
		footerLines, footerZones = m.sidebarFooter(variant, cw, pal, canCreate, footerHoverLine, footerHoverX, footerCursor)
	}

	nav := make([]sidebarNavRow, 0, nS+nT+nA+2)
	lines := make([]string, 0, height)
	// recordHit publishes a drawn row's rectangle and its nav row together, in
	// drawn order, so the mouse and the keyboard address one target set. Hits
	// only ever come from the renderer as it draws; nothing recomputes them.
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
		nav = append(nav, sidebarNavRow{Kind: kind, SessionID: sessionID, WindowID: windowID, WindowIndex: windowIndex})
	}
	overflowRow := func(n int) string {
		// Stands in for the rows it hides, so it starts on the name spine.
		more := overlay.Ellipsis() + " +" + strconv.Itoa(n)
		return compose(sidebarFit(strings.Repeat(" ", sidebarNameCol)+
			sidebarStyle(nil, pal.FgMute).Render(more), cw, nil))
	}

	// sessions
	lines = append(lines, compose(sidebarHeaderRow("sessions", "", cw, pal)))
	for i := range count[sidebarSectionSessions] {
		idx := start[sidebarSectionSessions] + i
		s := sessions[idx]
		dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
		hovered := idx == hoverRow[sidebarSectionSessions] || isCursor(sidebarRowSession, s.ID, "")
		recordHit(sidebarRowSession, s.ID, "", -1)
		lines = append(lines, compose(m.sidebarSessionRow(s, variant, cw, pal, hovered, dragged)))
	}
	if h := hidden[sidebarSectionSessions]; h > 0 {
		lines = append(lines, overflowRow(h))
	}

	// terminals
	if nT > 0 {
		right := ""
		if peeking {
			// Whose panes these are, since they are not the attached session's.
			right = sidebarStyle(nil, pal.Fg).Render(overlay.Truncate(printableTitle(shown), max(cw/2, 1)))
		}
		lines = append(lines, compose(sidebarHeaderRow("terminals", right, cw, pal)))
		if emptyPeek {
			hint := "no terminals"
			lines = append(lines, compose(sidebarFit(
				sidebarStyle(nil, nil).Render(" ")+sidebarQuietDot(nil, pal)+
					sidebarStyle(nil, nil).Render(" ")+
					sidebarStyle(nil, pal.FgMute).Render(overlay.Truncate(hint, sidebarNameAvail(cw, 0))), cw, nil)))
		} else {
			for i := range count[sidebarSectionTerminals] {
				idx := start[sidebarSectionTerminals] + i
				e := terminals[idx]
				hovered := idx == hoverRow[sidebarSectionTerminals] || isCursor(sidebarRowWindow, e.SessionID, e.WindowID)
				recordHit(sidebarRowWindow, e.SessionID, e.WindowID, e.WindowIndex)
				lines = append(lines, compose(m.sidebarTerminalRow(e, cw, pal, hovered, peeking)))
			}
			if h := hidden[sidebarSectionTerminals]; h > 0 {
				lines = append(lines, overflowRow(h))
			}
		}
	}

	for range slack {
		lines = append(lines, blank)
	}

	// agents, pinned to the bottom by the slack above them
	if nA > 0 {
		lines = append(lines, blank)
		controls, tokens := m.sidebarAgentsControls(cw, sidebarAgentsHeaderW, pal, agentsHeaderHoverX)
		for _, tk := range tokens {
			y := topMargin + len(lines)
			m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
				X0: contentX0 + tk.X0, X1: contentX0 + tk.X1,
				Y0: y, Y1: y + 1,
				Kind:        tk.Kind,
				WindowIndex: -1,
			})
			nav = append(nav, sidebarNavRow{Kind: tk.Kind, WindowIndex: -1})
		}
		lines = append(lines, compose(sidebarHeaderRow("agents", controls, cw, pal)))
		switch {
		case emptyFilter:
			// The hint is about the attached session ("here"), so it carries that
			// identity: it is a second filter control, and without something to tell
			// it apart from the header's token the cursor could not address it.
			recordHit(sidebarRowAgentFilter, m.sidebarCurrentSessionID(), "", -1)
			lines = append(lines, compose(m.sidebarAgentsEmptyRow(agentsTotal, cw, pal,
				hoverRow[sidebarSectionAgents] == 0 || isCursor(sidebarRowAgentFilter, m.sidebarCurrentSessionID(), ""))))
		default:
			for i := range count[sidebarSectionAgents] {
				idx := start[sidebarSectionAgents] + i
				e := agents[idx]
				hovered := idx == hoverRow[sidebarSectionAgents] || isCursor(sidebarRowAgent, e.SessionID, e.WindowID)
				recordHit(sidebarRowAgent, e.SessionID, e.WindowID, e.WindowIndex)
				lines = append(lines, compose(m.sidebarAgentRow(e, variant, cw, pal, hovered)))
			}
			if h := hidden[sidebarSectionAgents]; h > 0 {
				lines = append(lines, overflowRow(h))
			}
		}
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

	m.sidebarPublishNav(nav, cursorTarget, haveCursorTarget)
	return lines, w
}

// sidebarPublishNav hands the frame's navigable rows to the keyboard, then
// re-anchors the cursor onto the row it was tracking so its index stays valid
// across a relayout (reorder, switch, filter, peek). A follow request is
// consumed here, once the row it named exists in the new layout.
func (m *OS) sidebarPublishNav(nav []sidebarNavRow, target sidebarNavRow, haveTarget bool) {
	m.SidebarNav = nav
	m.sidebarFollowSession = ""
	if haveTarget {
		m.SidebarCursor = 0
		for i, r := range nav {
			if sidebarNavRowsEqual(r, target) {
				m.SidebarCursor = i
				break
			}
		}
	}
	if m.SidebarCursor >= len(nav) {
		m.SidebarCursor = max(len(nav)-1, 0)
	}
}

// sidebarShownSession is the session whose panes the terminals section is
// showing, and whether that is a peek rather than the attached session. A peek
// naming a session that no longer exists (or the attached one) is simply not a
// peek: the render never has to trust stale runtime state.
func (m *OS) sidebarShownSession(sessions []sessiontree.Node) (string, bool) {
	attached := m.sidebarCurrentSessionID()
	if m.SidebarPeek == "" || m.SidebarPeek == attached {
		return attached, false
	}
	for _, s := range sessions {
		if s.ID == m.SidebarPeek {
			return s.ID, true
		}
	}
	return attached, false
}

// sidebarTerminals flattens the panes of one session for the terminals section,
// ordered by workspace and then by the session's own pane order so a row never
// moves under the pointer for a reason the user cannot see.
func (m *OS) sidebarTerminals(sessions []sessiontree.Node, sessionID string) []sidebarTerminalEntry {
	var node *sessiontree.Node
	for i := range sessions {
		if sessions[i].ID == sessionID {
			node = &sessions[i]
			break
		}
	}
	if node == nil {
		return nil
	}
	out := make([]sidebarTerminalEntry, 0, len(node.Children))
	for _, win := range node.Children {
		e := sidebarTerminalEntry{
			SessionID:   node.ID,
			WindowID:    win.ID,
			Title:       win.Title,
			State:       win.AgentState,
			DoneSeen:    win.DoneSeen,
			Focused:     win.IsCurrent,
			WindowIndex: -1,
		}
		if node.IsCurrent {
			e.WindowIndex = m.windowIndexByID(win.ID)
		}
		// A pane elsewhere is here for orientation and says where it went; a pane
		// on the session's own workspace says nothing, because "here" is not
		// information. A session whose workspace this client cannot know (an
		// older daemon sends neither field) tags nothing at all rather than
		// tagging everything.
		e.workspace = win.Workspace
		if node.Workspace > 0 && win.Workspace > 0 && win.Workspace != node.Workspace {
			if node.IsCurrent {
				e.Tag = m.workspaceTag(win.Workspace)
			} else {
				// Another session's workspace names are not on the wire, so its
				// panes get the numbered form.
				e.Tag = "w" + strconv.Itoa(win.Workspace)
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].workspace < out[b].workspace })
	return out
}

// sidebarAgents flattens every pane running an agent, across every session.
// Sessions with known windows contribute: the attached one from live state,
// others from the cached listing, so agents elsewhere surface here marked
// Foreign.
func (m *OS) sidebarAgents(sessions []sessiontree.Node, variant int) []sidebarAgentEntry {
	if variant == sidebarVariantGlyph || !config.SidebarShowAgents {
		return nil
	}
	var agents []sidebarAgentEntry
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
				SessionID:    s.ID,
				SessionLabel: s.Title,
				WindowID:     win.ID,
				Title:        win.Title,
				State:        win.AgentState,
				DoneSeen:     win.DoneSeen,
				StateAt:      win.StateAt,
				WindowIndex:  idx,
				Foreign:      !s.IsCurrent,
			})
		}
	}
	// Left in tree order; the section's own filter and sort run over it, which is
	// what makes the cap safe: what it hides is the calm end of whichever order
	// the user asked for, never the pane waiting on an answer.
	return agents
}

// sidebarCursorIndex locates the cursor's target inside its section, so the
// auto-scroll knows which offset to move and by how much.
func (m *OS) sidebarCursorIndex(target sidebarNavRow, sessions []sessiontree.Node,
	terminals []sidebarTerminalEntry, agents []sidebarAgentEntry,
) (sidebarSection, int, bool) {
	switch target.Kind {
	case sidebarRowSession:
		for i, s := range sessions {
			if s.ID == target.SessionID {
				return sidebarSectionSessions, i, true
			}
		}
	case sidebarRowWindow:
		for i, e := range terminals {
			if e.WindowID == target.WindowID {
				return sidebarSectionTerminals, i, true
			}
		}
	case sidebarRowAgent:
		for i, e := range agents {
			if e.WindowID == target.WindowID && e.SessionID == target.SessionID {
				return sidebarSectionAgents, i, true
			}
		}
	}
	return 0, 0, false
}

// sidebarFooterZone is one control in the rail's footer: its kind and the
// content-relative columns it was drawn on. Two zones can share a line, so the
// footer hit-tests per zone rather than claiming the whole row.
type sidebarFooterZone struct {
	Kind   sidebarRowKind
	Line   int // index into the footer's own lines
	X0, X1 int
}

// sidebarCollapseGlyph is the footer control's mark, or ok false when the rail
// cannot move at this render width. Two states, not three: the arrow points
// where the rail is about to go, so a collapsed rail offers to reopen and an
// open one offers to get out of the way. The old three-stop ladder made the
// middle width a place the user could get stranded in with no name for it; it
// survives only as the responsive clamp on a 60-89 column screen, which no
// control targets.
//
// The arrow flips with the rail's side, because where the rail is about to go
// does: a left rail collapses leftward and reopens rightward, a right rail the
// other way round. Nothing else about the row mirrors.
func (m *OS) sidebarCollapseGlyph(variant int) (glyph string, ok bool) {
	left, right := "«", "»"
	if overlay.UseASCII() {
		left, right = "<<", ">>"
	}
	collapse, expand := left, right
	if config.SidebarPosition == "right" {
		collapse, expand = right, left
	}
	if variant == sidebarVariantGlyph {
		// Only offer to reopen when the screen has room to honour it; a control
		// that provably cannot move is noise.
		return expand, sidebarVariant(m.sidebarWidthFor(m.sidebarStoredWidth())) > variant
	}
	return collapse, true
}

// sidebarFooter renders the expanded rail's pinned bottom rows: the
// new-session control on the outer end and the collapse toggle on the
// pane-facing one, both meta voice on the bare canvas. Controls live down here
// rather than among the rows because they are not things the rail is listing,
// and a control dressed as a session row read as one. They share a line
// wherever both fit. The collapsed strip draws its own single control; see
// sidebar_strip.go.
func (m *OS) sidebarFooter(variant, cw int, pal overlay.Palette, canCreate bool,
	hoverLine, hoverX int, isCursor func(sidebarRowKind) bool,
) ([]string, []sidebarFooterZone) {
	stepGlyph, canStep := m.sidebarCollapseGlyph(variant)
	if !canCreate && !canStep {
		return nil, nil
	}

	const newLabel = "+ new"
	newW, stepW := lipgloss.Width(newLabel), lipgloss.Width(stepGlyph)

	// One line when both fit with a cell of air between them, otherwise one
	// line each.
	oneLine := !canCreate || !canStep || 1+newW+1+stepW+1 <= cw

	type placed struct {
		zone  sidebarFooterZone
		label string
	}
	// The control hugs the pane-facing corner and "+ new" holds the outer end,
	// which swaps with the rail's side: the toggle is always the thing nearest
	// the panes, where the pointer arrives from.
	outer, facing := 1, max(cw-1-stepW, 1)
	if config.SidebarPosition == "right" {
		outer, facing = max(cw-1-newW, 1), 1
	}

	var items []placed
	line := 0
	if canCreate {
		items = append(items, placed{sidebarFooterZone{Kind: sidebarRowNewSession, Line: line, X0: outer, X1: outer + newW}, newLabel})
		if !oneLine {
			line++
		}
	}
	if canStep {
		x0 := facing
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

// sidebarSessionRow renders one session row.
//
//	▎● name              3
//	^ ^ ^                ^ window count, right-aligned, muted, inset one cell
//	| | name: full strength on the attached session, dim on the rest
//	| rolled-up agent glyph, state-colored, a quiet dot when there is none
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
func (m *OS) sidebarSessionRow(node sessiontree.Node, variant, cw int, pal overlay.Palette, hovered, dragged bool) string {
	var rowBg color.Color
	if hovered || dragged {
		rowBg = pal.Surface
	}

	glyph := sidebarQuietDot(rowBg, pal)
	if agentStateIndicator(node.AgentState) != "" {
		glyph = sidebarGlyph(node.AgentState, node.DoneSeen, rowBg, pal)
	}

	right, rightW := "", 0
	if config.SidebarShowCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		countStr := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(countStr)
		rightW = lipgloss.Width(countStr)
	}

	// The attached session's name reads at full strength; the rest are dim. A
	// state wanting a human takes the rail's one bold voice, so it still leads
	// on a monochrome capture where the gutter colour is gone.
	fg := pal.FgDim
	if node.IsCurrent || hovered || dragged {
		fg = pal.Fg
	}
	name := sidebarStyle(rowBg, fg).Bold(sidebarAttention(node.AgentState)).
		Render(m.sidebarMarquee("s:"+node.ID, printableTitle(node.Title), sidebarNameAvail(cw, rightW), hovered))
	return sidebarComposeRow(sidebarGutter(node.IsCurrent, node.AgentState, rowBg, pal),
		glyph, name, right, cw, rowBg)
}

// sidebarTerminalRow renders one pane of the session the terminals section is
// showing. The focused pane wears exactly one identity bar: the gutter mark,
// burning its own accent when the user gave it one, which is why the glyph
// column carries agent state and nothing else on that row. An unfocused pane
// with an accent wears the chip in the gutter instead, so the rail keeps one
// column of identity top to bottom.
//
// A peeked row is a photograph: uniformly dim, no focus mark, no unread
// emphasis. Severity gutters and state glyph colours stay, because they are
// what the user peeked to see.
func (m *OS) sidebarTerminalRow(e sidebarTerminalEntry, cw int, pal overlay.Palette, hovered, peeked bool) string {
	var rowBg color.Color
	if hovered {
		rowBg = pal.Surface
	}

	title := printableTitle(e.Title)
	if title == "" {
		title = "shell"
	}

	gutter := sidebarGutter(false, e.State, rowBg, pal)
	if !peeked {
		var tint color.Color
		accent, accented := m.WindowAccent(e.WindowID)
		if m.ShowAccentPicker && e.WindowID == m.AccentPickerWindowID {
			// The open picker previews the colour under its cursor on the row it
			// targets, so the choice reads on the thing being accented.
			accent, accented = m.accentPreview(e.WindowID)
		}
		if accented {
			tint = accent.Color()
		}
		switch {
		case e.Focused:
			gutter = sidebarGutterTinted(true, e.State, tint, rowBg, pal)
		case accented && !sidebarAttention(e.State):
			gutter = sidebarStyle(rowBg, tint).Render(accentMark())
		}
	}

	// A pane on another workspace is here for orientation: it names the
	// workspace it is on, so the row answers "where did it go" without a switch
	// to find out. A pane on this workspace says nothing, because "here" is not
	// information.
	right, rightW := "", 0
	if e.Tag != "" {
		right = sidebarStyle(rowBg, pal.FgMute).Render(e.Tag)
		rightW = lipgloss.Width(e.Tag)
	}

	fg := pal.FgDim
	switch {
	case peeked:
		// Nothing in a peek is yours to act on, so nothing in it is emphasised.
	case e.Focused:
		fg = pal.Fg
	case e.State == "done" && !e.DoneSeen:
		// Unseen work reads at full strength; seeing it is what dims it.
		fg = pal.Fg
	}
	if hovered {
		fg = pal.Fg
	}

	name := sidebarStyle(rowBg, fg).Bold(sidebarAttention(e.State)).
		Render(m.sidebarMarquee("t:"+e.WindowID, title, sidebarNameAvail(cw, rightW), hovered))
	return sidebarComposeRow(gutter, sidebarGlyph(e.State, e.DoneSeen, rowBg, pal), name, right, cw, rowBg)
}

// workspaceTag is the quiet right-hand mark saying which workspace a pane sits
// on. A named workspace says its name, because that is the thing the user gave
// it to be recognised by; an unnamed one keeps the "w4" form, where the bare
// digit would read as a session row's window count on the line above.
func (m *OS) workspaceTag(ws int) string {
	if label := printableTitle(m.WorkspaceLabel(ws)); label != strconv.Itoa(ws) && label != "" {
		return overlay.Truncate(label, sidebarWorkspaceTagMax)
	}
	return "w" + strconv.Itoa(ws)
}

// sidebarWorkspaceTagMax caps a named workspace's tag so the name it fronts can
// never crowd out the pane name the row is actually about.
const sidebarWorkspaceTagMax = 8

// sidebarAgentsEmptyRow is what the agents section shows when its filter hides
// every pane it has: the state it is in, the count it is hiding, and the way
// back, all on the name spine so it reads as the section's one row rather than
// as a message about it. Clicking anywhere on it flips the filter.
func (m *OS) sidebarAgentsEmptyRow(total, cw int, pal overlay.Palette, hovered bool) string {
	var rowBg color.Color
	fg := pal.FgMute
	if hovered {
		rowBg, fg = pal.Surface, pal.Fg
	}
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}
	text := "none here" + sep + strconv.Itoa(total) + " all"
	return sidebarFit(sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", sidebarNameCol))+
		sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, sidebarNameAvail(cw, 0))), cw, rowBg)
}

// sidebarAgentRow renders one row of the agents section: state glyph, pane name
// (session-qualified when the pane lives in another session), and, in the full
// variant, how long it has been in its state, right-aligned.
func (m *OS) sidebarAgentRow(e sidebarAgentEntry, variant, cw int, pal overlay.Palette, hovered bool) string {
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
		if s := printableTitle(e.SessionLabel); s != "" {
			prefix = s + "/"
		}
	}

	// How long the pane has been in this state, in place of a state word: the
	// glyph, colour and sort position already say which state it is, while the
	// duration is the part nothing else carries. A pane waiting twenty minutes
	// on input reads very differently from one that just asked.
	label, labelW := "", 0
	if variant == sidebarVariantFull {
		label = agentElapsed(e.State, e.StateAt, time.Now())
		labelW = lipgloss.Width(label)
	}

	avail := sidebarNameAvail(cw, labelW)
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
	right := ""
	if label != "" {
		right = sidebarStyle(rowBg, timeFg).Render(label)
	}
	// An agent row is only ever "current" through the pane it points at, which
	// the terminals section already marks, so its gutter carries severity only.
	body := sidebarStyle(rowBg, pal.FgMute).Render(shown) +
		nameStyle.Render(m.sidebarMarquee("a:"+e.SessionID+"/"+e.WindowID, name,
			max(avail-lipgloss.Width(shown), 1), hovered))
	return sidebarComposeRow(sidebarGutter(false, e.State, rowBg, pal),
		sidebarGlyph(e.State, e.DoneSeen, rowBg, pal), body, right, cw, rowBg)
}
