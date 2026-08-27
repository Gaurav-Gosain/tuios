package app

import (
	"image/color"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// sidebarRestoredTag is the rail's marker for a session rebuilt from saved
// state, shared with every other surface that shows it.
const sidebarRestoredTag = session.RestoredTag

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
	// sidebarRowNewSession is the "+" in the sessions header, and the same
	// control on the collapsed strip. It targets nothing that exists yet.
	sidebarRowNewSession
	// sidebarRowNewWindow is the "+" in the terminals header: a new pane in the
	// session that section is listing.
	sidebarRowNewWindow
	// sidebarRowCollapse is the footer's collapse toggle. Like the header's
	// controls it is narrower than its line, so it carries its own columns.
	sidebarRowCollapse
	// sidebarRowFiles is the terminals header's control that puts the rail into
	// its file view. It sits beside the "+" and is the only way in from the
	// pointer.
	sidebarRowFiles
	// The rows of the file view itself. They only exist while the rail is in
	// that mode, which is also when none of the rows above do: the mode replaces
	// the three sections rather than joining them. See sidebar_files.go.
	//
	// sidebarRowFileBack is the header control that returns to the sections.
	sidebarRowFileBack
	// sidebarRowFileCd is the control that sends a cd to the pane the view was
	// opened from. It is drawn only when there is such a pane.
	sidebarRowFileCd
	// sidebarRowFileUp is the ".." row, drawn everywhere but at the root.
	sidebarRowFileUp
	// sidebarRowFileEntry is one name in the listing. It carries the entry's
	// index in the WindowIndex field, which is the only integer a row hit has;
	// nothing in this mode points at a window, so the field is free.
	sidebarRowFileEntry
)

// sidebarAddGlyph is the mark both add controls wear. One cell, so it costs a
// header no rows and no name: a "+ new" wide enough to read would have pushed
// the label out of a narrow rail.
func sidebarAddGlyph() string { return config.GetRailAddGlyph() }

// sidebarHeaderAdd places a section header's add control: right-aligned on the
// same spine every other trailing figure lands on, one cell in from the rail's
// edge. It returns the styled token and the content-relative columns it took,
// or ok false when the header has no room for it beside its own label, since
// half a control is half a click target.
//
// The control lives in the header rather than in the footer because that is
// what binds it to a section. One "+ new" pinned to the rail's bottom edge sat
// directly under the agents block and read as "new agent", which is not a thing
// the rail can do; the same glyph on the sessions header cannot be read as
// anything but "another one of these".
func sidebarHeaderAdd(kind sidebarRowKind, cw, labelW int, pal overlay.Palette, hoverX int, cursor bool) (string, sidebarTokenSpan, bool) {
	gw := lipgloss.Width(sidebarAddGlyph())
	x0 := cw - 1 - gw
	if x0 < labelW+1 {
		return "", sidebarTokenSpan{}, false
	}
	span := sidebarTokenSpan{Kind: kind, X0: x0, X1: x0 + gw}
	ink := pal.FgMute
	if cursor || (hoverX >= span.X0 && hoverX < span.X1) {
		ink = pal.Fg
	}
	return sidebarStyle(nil, ink).Render(sidebarAddGlyph()), span, true
}

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
// the only ones allowed a severity gutter mark, the rail's one bold, or a
// severity mark on the collapsed strip's spine: reserving those for the two
// states is what keeps them legible as an alarm.
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

// railFocusTint is the colour a focus mark burns: the identity the caller
// resolved, and the rail accent when there is none, which is every mark on the
// rail before session colours existed and every mark again with them off.
func railFocusTint(tint color.Color, pal overlay.Palette) color.Color {
	if tint != nil {
		return tint
	}
	return pal.Accent
}

// sidebarGutterTinted is sidebarGutter with the current-mark drawn in a colour
// of the caller's choosing: the focused pane's gutter burns the accent the user
// gave that pane, so the row wears exactly one identity bar instead of an
// accent chip beside a focus mark. tint nil falls back to the rail accent.
func sidebarGutterTinted(current bool, state string, tint, bg color.Color, pal overlay.Palette) string {
	switch {
	case current:
		return sidebarStyle(bg, railFocusTint(tint, pal)).Render(config.GetRailFocusMark())
	case sidebarAttention(state):
		return sidebarStyle(bg, sidebarSeverityColor(state, pal)).Render(config.GetRailAttentionMark())
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
	// Harness is which agent is running in the pane, as the detecting manifest
	// or the reporting source named it, empty when nothing named one.
	Harness string
	// Message is the note the pane reported with its state ("editing files"),
	// empty when it reported none.
	Message string
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
	return strings.TrimSpace(printableRunes(s))
}

// printableRunes is printableTitle without the trim, for the rename field: a
// space the user has just typed is the last thing in the buffer, and trimming it
// off the display makes the key look like it did nothing.
func printableRunes(s string) string {
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if printableRune(r, ascii) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// printableRune is the per-codepoint half of the rule printableTitle applies,
// exposed on its own so the rename editor can refuse a keypress the chrome would
// only strip again the moment the name was drawn.
func printableRune(r rune, ascii bool) bool {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		// C0/C1 controls.
		return false
	case r >= 0xe000 && r <= 0xf8ff:
		// BMP private use area.
		return false
	case r >= 0xf0000:
		// Plane 15/16 private use.
		return false
	case r >= 0x25a0 && r <= 0x2bff && !chromeGlyphs[r]:
		// Geometric Shapes through Miscellaneous Symbols and Arrows. Agents
		// park spinners and status ornaments in here (Claude Code alone uses
		// U+2733 idle and a U+2802/U+2810 Braille spinner) and they tofu in
		// any font that stops at Latin. Box Drawing and Block Elements end at
		// U+259F, so a title may still draw with them.
		return false
	case r >= 0xfe00 && r <= 0xfe0f:
		// Variation Selectors (VS1-16), including the emoji VS16.
		return false
	case r >= 0x1f000 && r <= 0x1faff:
		// Emoji and pictographic planes (Regional Indicator flag halves
		// U+1F1E6-1F1FF sit inside this span).
		return false
	case ascii && r > 0x7e:
		return false
	}
	return true
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
	return sidebarQuietDotTinted(pal.FgMute, bg, pal)
}

// dotTint is the colour the quiet dot burns: the session's, or the muted ink it
// has always used when there is no colour to show or the cell is about to be
// taken by an agent state.
func dotTint(tint color.Color, pal overlay.Palette, stated bool) color.Color {
	if tint == nil || stated {
		return pal.FgMute
	}
	return tint
}

// sidebarQuietDotTinted is sidebarQuietDot in a colour of the caller's
// choosing: a session row with no agent running burns its session's colour in
// the dot it was already drawing, so the colour costs the rail no cell and a
// terminal without colour sees the row it saw before.
func sidebarQuietDotTinted(tint, bg color.Color, pal overlay.Palette) string {
	if !config.SidebarShowGlyphs {
		return sidebarStyle(bg, nil).Render(" ")
	}
	// No ASCII branch: GetRailBullet already gives up a glyph the terminal
	// cannot draw and keeps one it can, per glyph rather than per set, so a
	// branch here would throw away an ASCII-safe set under --ascii-only.
	return sidebarStyle(bg, tint).Render(config.GetRailBullet())
}

// sidebarEdgeRule is the one-cell vertical rule separating the rail from the
// panes, drawn in the window-border character at the dock separator's color:
// the rail's edge is the vertical sibling of the dock's hairline.
func sidebarEdgeRule() string {
	return lipgloss.NewStyle().Foreground(theme.RailRule()).Render(config.GetWindowBorderLeft())
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

// sidebarHeaderLabelW is the columns a section's label occupies, its leading
// inset included. A header's controls refuse to draw over it.
func sidebarHeaderLabelW(label string) int { return 1 + lipgloss.Width(label) }

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
//
// The figure costs two cells beyond itself: the inset that holds it off the
// rail's edge rule, and one blank in front of it. Without that blank a name cut
// to the last cell butts against its own window count, and "documentation-site"
// beside a count of 2 reads as "documentation-site2". The name is what gives way
// there, never the gap.
func sidebarNameAvail(cw, rightW int) int {
	if rightW > 0 {
		rightW += 2 // the inset cell, and the gap in front of the figure
	}
	return max(cw-sidebarNameCol-rightW, 1)
}

// renderSidebar composes the vertical session sidebar as a single layer, the way
// renderDock composes the dock. It returns nil when the sidebar reserves no
// columns (off, hidden, or the screen too narrow). It also records the on-screen
// hit geometry of every row into m.SidebarHits for the mouse handlers.
func (m *OS) renderSidebar() *lipgloss.Layer {
	panel, w := m.sidebarPanel()
	if panel == "" {
		return nil
	}
	sidebarX := 0
	if config.SidebarPosition == "right" {
		sidebarX = m.GetRenderWidth() - w
	}
	return lipgloss.NewLayer(panel).X(sidebarX).Y(m.GetTopMargin()).Z(config.ZIndexDock).ID("sidebar")
}

// sidebarBudget divides avail lines between the three sections, per the design's
// table: sessions are content-sized up to about a quarter of the rail, agents up
// to about a third, and the terminals section takes the slack because it is the
// list the user actually works in. A rail too short for the terminals floor
// shrinks agents first, then sessions, and never below their own floors.
//
// # Why there is no fourth section for workspaces
//
// It has been asked for and the answer is no, for four reasons, the first of
// which is this function.
//
// The floors below already claim 2+3+2 lines before a single row of content,
// and every section draws its header whether or not the budget could afford it,
// so three sections cost ten lines of chrome at the minimum. A workspaces
// section with the same two-row floor takes that to thirteen, against a rail
// that TestRailFitsAShortRegion walks from zero because rail growth has already
// once overrun the region and painted over the dock. It would also have to join
// the give-up ladder at the bottom of this function, and there is no obvious
// answer to whether workspaces should yield before agents or after them.
//
// It would restate what is already on screen. The terminals section is ordered
// by workspace and tags each pane with its workspace's name, so a workspaces
// section would be one row per workspace carrying nothing the tags do not.
//
// It does not fit the rail's grammar. Sessions and agents are lists across
// every session; terminals is the list inside the selected one. A workspace
// only exists inside a session, so a workspaces section would be the second
// per-session list, competing with terminals for the same lines while saying
// less than it does.
//
// And a workspace already has three surfaces that cost no rail lines at all:
// the dock's pill strip, which is always on screen and can be rearranged by
// dragging; the digit keys; and the workspace switcher. What a fourth section
// would have been for is seeing and steering the workspace arrangement, and
// that is what those three do.
//
// What the rail owed workspaces was agreement, not a section. Its terminals
// grouping now orders by the same display order the dock's pills use, so
// dragging a pill rearranges the panes under it here too.
//
// aRowH is how many lines one agent row takes, so the agents section is
// budgeted in lines like every other section while still being counted in rows.
// Its share of the rail stays a third of the rows either way: a tall row buys
// its second line out of the same third, never out of the terminals list.
func sidebarBudget(avail, nS, nT, nA, aRowH int) (sH, tH, aH int) {
	avail = max(avail, 0)
	aRowH = max(aRowH, 1)
	aLines := nA * aRowH
	sFloor, tFloor, aFloor := min(nS, 2), min(nT, 3), min(aLines, 2*aRowH)

	sH = min(nS, max(avail/4, sFloor))
	aH = min(aLines, max(aRowH*(avail/3), aFloor))
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
	// Colours are arbitrated over this machine's sessions only. A remote row
	// draws in its host group's muted ink, and letting one into the arbitration
	// would move a local session's colour because a machine somewhere else
	// gained a session.
	m.refreshSessionColorsFor(localSessionNodes(tree.Sessions))

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
		if isRemoteNode(s) {
			// A remote row is not addressable, so it is not in the id list the
			// mouse resolves a click against.
			continue
		}
		m.SidebarSessionIDs = append(m.SidebarSessionIDs, s.ID)
	}

	if variant == sidebarVariantGlyph {
		// The strip lays its own ground, so it composes its lines itself rather
		// than borrowing the expanded rail's bare-canvas edge. Host groups stay
		// out of it: three columns cannot say which machine a row is on, and a
		// row that cannot say that must not sit beside the local ones.
		return m.sidebarStripLines(localSessionNodes(sessions), w, cw, height, topMargin, sidebarX, pal, edgeLeft)
	}

	// The file view takes the whole rail, so it leaves before any of the section
	// machinery below runs: no budget, no place table, no per-section scroll.
	// That is the entire cost of the mode being a mode, and it is why the three
	// sections lay out identically whether or not the mode exists.
	//
	// It is checked after the collapsed strip and not before, so a rail folded to
	// three columns keeps drawing the strip. A listing cannot be drawn in three
	// columns, and OpenFileView refuses to enter the mode there for that reason.
	if m.filesView.Open {
		return m.sidebarFilesLines(w, cw, height, topMargin, sidebarX, contentX0, pal, compose, blank), w
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
	agents, agentsTotal := m.sidebarFilterAgents(m.sidebarAgents(sessions))
	m.sidebarSortAgents(agents)
	// The one section whose order moves on its own, so the one whose viewport is
	// anchored to a row rather than to an index. Before the cursor's auto-scroll
	// below, which gets the last word on what is on screen.
	m.sidebarReanchorAgents(agents)
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
	footerLines, footerZones := m.sidebarFooter(variant, cw, pal, -1, -1, footerCursor)
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
	// A second line per agent row carries the harness and the note the pane
	// reported, which is the one thing on the rail no other row can say. It is
	// taken only when it costs nothing: the section goes tall when the budget it
	// lands on still holds every agent it has, so "lines = 2 x rows" is exact,
	// the section never hides a row in order to spell one out, and the height is
	// the same for every row in it rather than per entry.
	agentRowH := 1
	sH, tH, aH := sidebarBudget(height-footerH-chrome, nS, nT, nA, agentRowH)
	if nA > 0 && !emptyFilter && sidebarAgentsHaveNotes(agents) {
		if s2, t2, a2 := sidebarBudget(height-footerH-chrome, nS, nT, nA, sidebarAgentRowTall); a2 >= nA*sidebarAgentRowTall {
			sH, tH, aH, agentRowH = s2, t2, a2, sidebarAgentRowTall
		}
	}
	// Row heights per section, which is what turns a section's line budget into
	// the rows it can show and a hovered line back into the row under it.
	rowH := [sidebarSectionCount]int{1, 1, agentRowH}
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
	// A section's fold is in rows and its budget is in lines. Everything below
	// scrolls and windows against this rather than against the line count, which
	// were the same number until an agent row took two of them.
	var capRows [sidebarSectionCount]int
	for s := range capRows {
		capRows[s] = place[s].lines / rowH[s]
	}
	if m.SidebarFocused && haveCursorTarget {
		if sec, idx, ok := m.sidebarCursorIndex(cursorTarget, sessions, terminals, agents); ok {
			if rows := capRows[sec]; rows > 0 {
				if idx < *scroll[sec] {
					*scroll[sec] = idx
				} else if idx >= *scroll[sec]+rows {
					*scroll[sec] = idx - rows + 1
				}
			}
		}
	}
	var start, count, hidden [sidebarSectionCount]int
	for s := range rowsIn {
		start[s], count[s], hidden[s] = sidebarWindowSection(*scroll[s], rowsIn[s], capRows[s])
		*scroll[s] = start[s]
	}
	m.sidebarRecordAgentAnchor(agents, start[sidebarSectionAgents], count[sidebarSectionAgents])

	// Hover, derived from the last motion seen inside the band, resolved against
	// the placement above. Hover yields entirely to a drag.
	var hoverRow [sidebarSectionCount]int
	for s := range hoverRow {
		hoverRow[s] = -1
	}
	footerHoverLine, footerHoverX := -1, -1
	// Every header now carries click targets of its own (the add controls, and
	// the agents section's filter and sort), so the pointer's column on a header
	// line matters as well as which line it is on.
	var headerHoverX [sidebarSectionCount]int
	for s := range headerHoverX {
		headerHoverX[s] = -1
	}
	if !m.SidebarDrag.Dragging && m.SidebarHoverActive && m.SidebarBandContains(m.SidebarHoverX, m.SidebarHoverY) {
		delta := m.SidebarHoverY - topMargin
		footerTop := height - footerH
		onHeader := -1
		for s := range place {
			if place[s].header >= 0 && delta == place[s].header {
				onHeader = s
			}
		}
		switch {
		case footerH > 0 && delta >= footerTop && delta < height:
			footerHoverLine, footerHoverX = delta-footerTop, m.SidebarHoverX-contentX0
		case onHeader >= 0:
			headerHoverX[onHeader] = m.SidebarHoverX - contentX0
		default:
			for s := range place {
				if d := delta - place[s].top; d >= 0 && d < count[s]*rowH[s] {
					hoverRow[s] = start[s] + d/rowH[s]
				}
			}
		}
	}
	// Re-rendered now the pointer is resolved; the first pass only measured how
	// many lines the footer takes so the sections could be sized.
	if footerH > 0 {
		footerLines, footerZones = m.sidebarFooter(variant, cw, pal, footerHoverLine, footerHoverX, footerCursor)
	}

	nav := make([]sidebarNavRow, 0, nS+nT+nA+2)
	lines := make([]string, 0, height)
	// recordHit publishes a drawn row's rectangle and its nav row together, in
	// drawn order, so the mouse and the keyboard address one target set. Hits
	// only ever come from the renderer as it draws; nothing recomputes them.
	// h is the row's height in lines: one for every section but agents, whose
	// rows carry a second line when the budget allows. The rectangle grows and
	// the nav entry does not, so one agent stays one target for the keyboard and
	// the mouse both, and a click on either line resolves to the same row.
	recordHit := func(kind sidebarRowKind, sessionID, windowID string, windowIndex, h int) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: sidebarX, X1: sidebarX + w,
			Y0: y, Y1: y + max(h, 1),
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
	// recordToken publishes a header control's rectangle and its nav row, the
	// column-scoped sibling of recordHit. Called before the header line is
	// appended, so the y it computes is that line's.
	recordToken := func(tk sidebarTokenSpan, sessionID string) {
		y := topMargin + len(lines)
		m.SidebarHits = append(m.SidebarHits, sidebarRowHit{
			X0: contentX0 + tk.X0, X1: contentX0 + tk.X1,
			Y0: y, Y1: y + 1,
			Kind:        tk.Kind,
			SessionID:   sessionID,
			WindowIndex: -1,
		})
		nav = append(nav, sidebarNavRow{Kind: tk.Kind, SessionID: sessionID, WindowIndex: -1})
	}

	// sessions
	sessionsAdd := ""
	if canCreate {
		if tok, span, ok := sidebarHeaderAdd(sidebarRowNewSession, cw, sidebarHeaderLabelW("sessions"),
			pal, headerHoverX[sidebarSectionSessions], isCursor(sidebarRowNewSession, "", "")); ok {
			sessionsAdd = tok
			recordToken(span, "")
		}
	}
	lines = append(lines, compose(sidebarHeaderRow("sessions", sessionsAdd, cw, pal)))
	for i := range count[sidebarSectionSessions] {
		idx := start[sidebarSectionSessions] + i
		s := sessions[idx]
		if isRemoteNode(s) {
			// Federation stage 1 carries listings and nothing else, so a row for
			// another machine is drawn and is not a target: no hit rectangle, no
			// nav entry, no drag, no click. Nothing can reach a remote session
			// from the rail, which is the point rather than an omission.
			lines = append(lines, compose(m.sidebarHostRow(s, cw, pal)))
			continue
		}
		dragged := m.SidebarDrag.Dragging && s.ID == m.SidebarDrag.SessionID
		hovered := idx == hoverRow[sidebarSectionSessions] || isCursor(sidebarRowSession, s.ID, "")
		recordHit(sidebarRowSession, s.ID, "", -1, 1)
		lines = append(lines, compose(m.sidebarSessionRow(s, variant, cw, pal, hovered, dragged)))
	}
	if h := hidden[sidebarSectionSessions]; h > 0 {
		lines = append(lines, overflowRow(h))
	}

	// terminals
	if nT > 0 {
		// The add control takes the spine's last cell, and the peek label sits in
		// front of it. A peek is the pointer's own transient state and cannot
		// coexist with a pointer on this header, so the two never compete for the
		// same cells in practice; the arithmetic holds either way.
		termAdd, termSpan, hasTermAdd := sidebarHeaderAdd(sidebarRowNewWindow, cw,
			sidebarHeaderLabelW("terminals"), pal, headerHoverX[sidebarSectionTerminals],
			isCursor(sidebarRowNewWindow, shown, ""))
		right := termAdd
		if peeking {
			// Whose panes these are, since they are not the attached session's,
			// in that session's own colour: the row the pointer is on is marked
			// the same way three lines up, so the preview and its source are
			// visibly one thing rather than two lists that happen to be adjacent.
			ink := pal.Fg
			if tint := m.sessionTint(shown, theme.TerminalBg()); tint != nil {
				ink = tint
			}
			// The label gives way to the control, never the other way round: a
			// readout that pushes a click target off its own cells is worse than a
			// readout cut one word shorter. The control keeps the spine's last cell,
			// so its recorded columns hold whether or not a label precedes it.
			room := max(cw/2, 1)
			if hasTermAdd {
				room = max(room-lipgloss.Width(sidebarAddGlyph())-1, 1)
			}
			name := sidebarStyle(nil, ink).Render(overlay.Truncate(printableTitle(shown), room))
			right = name + sidebarStyle(nil, nil).Render(" ") + termAdd
			if !hasTermAdd {
				right = name
			}
		}
		if hasTermAdd {
			recordToken(termSpan, shown)
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
				recordHit(sidebarRowWindow, e.SessionID, e.WindowID, e.WindowIndex, 1)
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
		// No add control here, and the asymmetry is the honest answer: an agent is
		// a pane running an agent CLI, which is exactly what the terminals section
		// makes. A "+" on this header would be a second name for new-terminal
		// pointing at a list the rail only observes.
		controls, tokens := m.sidebarAgentsControls(cw, sidebarHeaderLabelW("agents"), pal,
			headerHoverX[sidebarSectionAgents])
		for _, tk := range tokens {
			recordToken(tk, "")
		}
		lines = append(lines, compose(sidebarHeaderRow("agents", controls, cw, pal)))
		switch {
		case emptyFilter:
			// The hint is about the attached session ("here"), so it carries that
			// identity: it is a second filter control, and without something to tell
			// it apart from the header's token the cursor could not address it.
			recordHit(sidebarRowAgentFilter, m.sidebarCurrentSessionID(), "", -1, 1)
			lines = append(lines, compose(m.sidebarAgentsEmptyRow(agentsTotal, cw, pal,
				hoverRow[sidebarSectionAgents] == 0 || isCursor(sidebarRowAgentFilter, m.sidebarCurrentSessionID(), ""))))
		default:
			for i := range count[sidebarSectionAgents] {
				idx := start[sidebarSectionAgents] + i
				e := agents[idx]
				hovered := idx == hoverRow[sidebarSectionAgents] || isCursor(sidebarRowAgent, e.SessionID, e.WindowID)
				tall := rowH[sidebarSectionAgents] > 1
				recordHit(sidebarRowAgent, e.SessionID, e.WindowID, e.WindowIndex, rowH[sidebarSectionAgents])
				lines = append(lines, compose(m.sidebarAgentRow(e, variant, cw, pal, hovered, tall)))
				if tall {
					lines = append(lines, compose(m.sidebarAgentNoteRow(e, cw, pal, hovered)))
				}
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

	// The rail is exactly the rows its region gave it. Each section's header is
	// drawn whether or not the budget could afford it, so a region short enough
	// that the chrome alone overruns it produced a rail taller than its own band:
	// the extra rows painted over the dock, and the hit rectangles recorded on
	// them made a row outside the band clickable.
	if len(lines) > height {
		lines = lines[:height]
		bottom := topMargin + height
		kept := m.SidebarHits[:0]
		for _, h := range m.SidebarHits {
			if h.Y1 <= bottom {
				kept = append(kept, h)
			}
		}
		m.SidebarHits = kept
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
	// Ordered by where the workspaces are shown rather than by their numbers, so
	// dragging a pill in the dock rearranges the panes under it here too. Two
	// surfaces grouping the same panes into a different sequence would be the one
	// disagreement a single display order exists to rule out.
	sort.SliceStable(out, func(a, b int) bool {
		return m.workspaceRank(out[a].workspace) < m.workspaceRank(out[b].workspace)
	})
	return out
}

// sidebarAgents flattens every pane running an agent, across every session.
// Sessions with known windows contribute: the attached one from live state,
// others from the cached listing, so agents elsewhere surface here marked
// Foreign.
func (m *OS) sidebarAgents(sessions []sessiontree.Node) []sidebarAgentEntry {
	if !config.SidebarShowAgents {
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
				Harness:      win.Harness,
				Message:      win.Message,
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
	left, right := config.GetRailCollapseGlyph(), config.GetRailExpandGlyph()
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

// sidebarFooter renders the expanded rail's pinned bottom row: the collapse
// toggle, hugging the pane-facing corner in meta voice on the bare canvas.
//
// It used to carry "+ new" as well, on the outer end. That was the rail's only
// add affordance, and pinning it to the bottom edge put it directly under the
// agents block, where it read as "new agent" rather than "new session". The add
// controls moved into the section headers, which is what binds each one to what
// it makes; leaving a duplicate down here would have been two affordances for
// one action, which is worse than one in the wrong place.
//
// It now carries a second control, "files", and the paragraph above is the test
// it had to pass. "+ new" was a section's action pinned outside its section,
// which is why it read as belonging to whatever happened to sit above it. This
// one is not a section's action at all: the file view replaces the whole rail,
// so the row that already holds the rail's own toggle is exactly where it
// belongs, and there is no section it could have been put inside instead.
//
// The collapsed strip draws its own controls; see sidebar_strip.go.
func (m *OS) sidebarFooter(variant, cw int, pal overlay.Palette,
	hoverLine, hoverX int, isCursor func(sidebarRowKind) bool,
) ([]string, []sidebarFooterZone) {
	stepGlyph, canStep := m.sidebarCollapseGlyph(variant)
	if !canStep {
		return nil, nil
	}

	stepW := lipgloss.Width(stepGlyph)

	type placed struct {
		zone  sidebarFooterZone
		label string
	}
	// The toggle is always the thing nearest the panes, where the pointer
	// arrives from, so its corner swaps with the rail's side.
	facing := max(cw-1-stepW, 1)
	if config.SidebarPosition == "right" {
		facing = 1
	}
	line := 0
	items := []placed{{sidebarFooterZone{Kind: sidebarRowCollapse, Line: line, X0: facing, X1: facing + stepW}, stepGlyph}}

	// "files" hugs the opposite corner from the toggle, so the two controls sit
	// at the ends of the row rather than next to each other, and neither can be
	// clicked by aiming at the other. It is dropped rather than crowded when the
	// rail is too narrow to hold both with a cell between them: a control the
	// pointer cannot separate from its neighbour is worse than one that is not
	// there, and the file view is also reachable by clicking a folder link.
	filesX := 1
	if config.SidebarPosition == "right" {
		filesX = max(cw-1-sidebarFilesLabelW, 1)
	}
	filesEnd := filesX + sidebarFilesLabelW
	if filesEnd < facing-1 || filesX > facing+stepW {
		items = append(items, placed{
			sidebarFooterZone{Kind: sidebarRowFiles, Line: line, X0: filesX, X1: filesEnd},
			sidebarFilesLabel,
		})
	}

	// Cell-addressed rather than spliced into a rendered string: a zone's escape
	// sequences would make byte offsets lie to any zone after it.
	cells := make([][]string, line+1)
	for i := range cells {
		cells[i] = make([]string, cw)
		for c := range cells[i] {
			cells[i][c] = " "
		}
	}
	// Left to right, because the rail publishes its rectangles in drawn order
	// and a row's targets are drawn left to right. The two controls hug opposite
	// corners, so which of them is built first depends on which side the rail is
	// on, and recording them in construction order put the outer one's rectangle
	// before the inner one's on a left-hand rail.
	slices.SortFunc(items, func(a, b placed) int { return a.zone.X0 - b.zone.X0 })

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
//	gutter: the session's own colour, severity when a pane wants a human
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

	// The session's colour takes whichever of the row's two marks the louder
	// signals have not claimed. The quiet dot first, which costs the rail
	// nothing: a terminal with no colour draws the row it drew before. When a
	// pane is running an agent the state owns that cell, so identity falls to
	// the gutter, and when a pane wants a human the severity owns that one too
	// and identity gives way entirely. An alarm outranks a label.
	tint := m.sessionTint(node.ID, railGround(rowBg))
	stated := agentStateIndicator(node.AgentState) != ""

	glyph := sidebarQuietDotTinted(dotTint(tint, pal, stated), rowBg, pal)
	if stated {
		glyph = sidebarGlyph(node.AgentState, node.DoneSeen, rowBg, pal)
	}

	right, rightW := "", 0
	if config.SidebarShowCounts && node.WindowCount > 0 && variant == sidebarVariantFull {
		countStr := strconv.Itoa(node.WindowCount)
		right = sidebarStyle(rowBg, pal.FgMute).Render(countStr)
		rightW = lipgloss.Width(countStr)
	}
	// The restored tag rides the right slot, dim, and only where there is room
	// for a word: it says the layout came back without its processes, which is
	// worth a column or two off the name until someone attaches and it goes.
	if node.Restored && variant == sidebarVariantFull {
		tag := sidebarRestoredTag
		if rightW > 0 {
			tag += " "
		}
		right = sidebarStyle(rowBg, pal.FgMute).Render(tag) + right
		rightW += lipgloss.Width(tag)
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

	gutter := sidebarGutterTinted(node.IsCurrent, node.AgentState, tint, rowBg, pal)
	if tint != nil && stated && !node.IsCurrent && !sidebarAttention(node.AgentState) {
		gutter = sidebarStyle(rowBg, tint).Render(accentMark())
	}
	return sidebarComposeRow(gutter, glyph, name, right, cw, rowBg)
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
		// The focus mark is the session's own colour. The rail is one object, and a
		// session marked magenta two rows above its focused pane marked blue reads
		// as a mismatch rather than as a distinction. It says nothing new, which is
		// why the section is otherwise still uncoloured: one session's panes are on
		// screen at a time, so a hue per row would separate them from nothing.
		tint := m.sessionTint(e.SessionID, railGround(rowBg))
		accent, accented := m.WindowAccent(e.WindowID)
		if preview, ok := m.accentPreview(AccentTargetWindow, e.WindowID); ok {
			// The open picker previews the colour under its cursor on the row it
			// targets, so the choice reads on the thing being accented.
			accent, accented = preview, true
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

// sidebarHarnessMax caps a harness label so the agent it names can never crowd
// out the pane the row is actually about.
const sidebarHarnessMax = 8

// sidebarHarnessLabel is the short form of a harness id, for a row that has one
// name's worth of room and two names to put in it. The bundled manifests spell
// the product out ("claude-code", "gemini-cli", "cursor-agent"); the first
// segment is the agent's identity and the rest is the shape it ships in, which
// is not a distinction anyone is drawing on a 28-column rail.
func sidebarHarnessLabel(harness string) string {
	id, _, _ := strings.Cut(printableTitle(harness), "-")
	return overlay.Truncate(strings.ToLower(id), sidebarHarnessMax)
}

// sidebarAgentPrefix is the muted context in front of an agent row's name: which
// session the pane is in when it is not this one, and which agent is running in
// it. It returns what fits, including the trailing separator, or "".
//
// Two facts compete for the same cells, so they yield in order. The session goes
// first because the row's gutter already carries a tint for a pane that is
// somewhere else, while nothing else on the row says which agent it is. Whatever
// survives that is still dropped whole before a single cell of the pane name
// goes, which is the rule the session prefix has always followed.
func sidebarAgentPrefix(session, harness, name string, avail int) string {
	var parts []string
	if session != "" {
		parts = append(parts, session)
	}
	// A pane running an agent is usually already labelled with its command, so a
	// row reading "claude/claude" would spend half its width saying one thing
	// twice. The prefix earns its cells only when it adds a name.
	if h := sidebarHarnessLabel(harness); h != "" && !strings.EqualFold(h, name) {
		parts = append(parts, h)
	}
	for len(parts) > 0 {
		if s := strings.Join(parts, "/") + "/"; lipgloss.Width(s)+2 <= avail {
			return s
		}
		parts = parts[1:]
	}
	return ""
}

// sidebarAgentRowTall is how many lines a tall agent row takes: the identity
// line, and the note under it.
const sidebarAgentRowTall = 2

// sidebarAgentsHaveNotes reports whether any of these agents has something to
// put on a second line. A section where none of them does would pay two lines a
// row for a column of blanks.
func sidebarAgentsHaveNotes(agents []sidebarAgentEntry) bool {
	// Wider than any rail, so what is asked here is whether the note exists at
	// all rather than whether it would fit at some particular width.
	const unbounded = 1 << 20
	for _, e := range agents {
		if sidebarAgentNote(e, unbounded) != "" {
			return true
		}
	}
	return false
}

// sidebarAgentNote is the second line's text: which agent this is, and the note
// it reported, joined by the separator the rail uses everywhere else.
//
// The note gives way before the harness name. Which agent a row is stays true
// at any width, where half a sentence is not a shorter sentence, so a rail
// squeezed narrow keeps "claude" and drops what it was doing.
func sidebarAgentNote(e sidebarAgentEntry, avail int) string {
	harness, note := sidebarHarnessLabel(e.Harness), printableTitle(e.Message)
	// The same rule the prefix followed on the line above: a pane running an
	// agent is usually already named after it, and "claude" over "claude" spends
	// a line saying one thing twice.
	if strings.EqualFold(harness, sidebarAgentName(e)) {
		harness = ""
	}
	switch {
	case harness == "" && note == "":
		return ""
	case note == "":
		return overlay.Truncate(harness, avail)
	case harness == "":
		return overlay.Truncate(note, avail)
	}
	sep := " · "
	if overlay.UseASCII() {
		sep = " . "
	}
	// Two cells is the least a truncated note can say that a bare ellipsis does
	// not; below that the row is better off spending everything on the name.
	if room := avail - lipgloss.Width(harness) - lipgloss.Width(sep); room >= 2 {
		return harness + sep + overlay.Truncate(note, room)
	}
	return overlay.Truncate(harness, avail)
}

// sidebarAgentName is what an agent row calls the pane it points at.
func sidebarAgentName(e sidebarAgentEntry) string {
	if name := printableTitle(e.Title); name != "" {
		return name
	}
	return "shell"
}

// sidebarAgentNoteRow is the second line of a tall agent row: which agent is in
// the pane, and the note it last reported, indented one cell past the name
// spine so the pair reads as one entry.
//
// This is the line that earns the row it costs. The harness name alone is
// static, and a rail that spent a line per agent on it would be spending it on
// something the row above could have carried; the note is live, is the answer
// to "what is it doing", and had nowhere else to be said.
//
// It is drawn in the quiet tier, which is the point: the loud thing on an agent
// row is the state, and a sentence in the same ink as the name would outrank the
// pane it is about.
func (m *OS) sidebarAgentNoteRow(e sidebarAgentEntry, cw int, pal overlay.Palette, hovered bool) string {
	var rowBg color.Color
	if hovered {
		rowBg = pal.Surface
	}
	indent := sidebarNameCol + 1
	text := sidebarAgentNote(e, sidebarNameAvail(cw, 0)-1)
	return sidebarFit(sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent))+
		sidebarStyle(rowBg, pal.FgMute).Render(text), cw, rowBg)
}

// sidebarAgentRow renders the identity line of one row of the agents section:
// state glyph, the agent and pane it names (session-qualified when the pane
// lives in another session), and, in the full variant, how long it has been in
// its state, right-aligned.
//
// tall says the row has a note line under it, which is where the harness name
// goes: carrying it here as well would print one thing twice, and the line has
// only ever had room for one name.
func (m *OS) sidebarAgentRow(e sidebarAgentEntry, variant, cw int, pal overlay.Palette, hovered, tall bool) string {
	var rowBg color.Color
	fg := pal.FgDim
	if e.State == "done" && !e.DoneSeen {
		fg = pal.Fg
	}
	if hovered {
		rowBg = pal.Surface
		fg = pal.Fg
	}

	name := sidebarAgentName(e)
	// A pane in another session carries that session as a prefix. It is context,
	// not the answer, so it renders muted against the full-strength pane name and
	// gives its cells up first when the row runs out of room.
	session := ""
	if e.Foreign {
		session = printableTitle(e.SessionLabel)
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
	harness := e.Harness
	if tall {
		harness = ""
	}
	shown := sidebarAgentPrefix(session, harness, name, avail)
	right := ""
	if label != "" {
		right = sidebarStyle(rowBg, timeFg).Render(label)
	}
	// An agent row is only ever "current" through the pane it points at, which
	// the terminals section already marks, so its gutter carries severity, and
	// below that the fact that the pane is somewhere else. The mark is drawn on
	// foreign rows only, so it says "not from here" on a terminal with no colour
	// and says which session on one with colour. It is the answer the prefix
	// gives in words and gives up first when the row runs out of room.
	gutter := sidebarGutter(false, e.State, rowBg, pal)
	if e.Foreign && !sidebarAttention(e.State) {
		if tint := m.agentIdentityTint(e, railGround(rowBg)); tint != nil {
			gutter = sidebarStyle(rowBg, tint).Render(accentMark())
		}
	}
	body := sidebarStyle(rowBg, pal.FgMute).Render(shown) +
		nameStyle.Render(m.sidebarMarquee("a:"+e.SessionID+"/"+e.WindowID, name,
			max(avail-lipgloss.Width(shown), 1), hovered))
	return sidebarComposeRow(gutter,
		sidebarGlyph(e.State, e.DoneSeen, rowBg, pal), body, right, cw, rowBg)
}
