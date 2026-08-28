package app

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The collapsed strip says everything in two cells, which is enough to steer by
// and not enough to read. The shared tooltip in tooltip.go fills that gap for
// the pointer only; this is the rail's half of it, the words and where they go.

// sidebarTooltipTrack records the pointer landing on something in the rail that
// talks: a strip row, or one of the expanded rail's one-cell add controls.
// Called from the motion handler, which is the only thing that knows the
// pointer moved.
func (m *OS) sidebarTooltipTrack(x, y int) {
	for _, r := range m.sidebarStripRows {
		if r.contains(y) {
			m.tooltipTrack(tooltipRailStrip, y)
			return
		}
	}
	// Keyed by the control's kind rather than by its row, since there is one of
	// each and their headers move with the section budget.
	if h, ok := m.sidebarRowAt(x, y); ok && sidebarAddKind(h.Kind) {
		m.tooltipTrack(tooltipRailAdd, int(h.Kind))
		return
	}
	m.tooltipClear()
}

// sidebarAddKind reports whether a row kind is one of the header add controls.
func sidebarAddKind(k sidebarRowKind) bool {
	return k == sidebarRowNewSession || k == sidebarRowNewWindow
}

// sidebarAddWords is what an add control says when it is asked. The words match
// the actions elsewhere in the app, so the label and the palette never invent
// two names for one thing.
func sidebarAddWords(k sidebarRowKind) string {
	if k == sidebarRowNewWindow {
		return "new terminal"
	}
	return "new session"
}

// renderRailAddTooltip composes the label for the add control under the
// pointer. It anchors on the control's own line and opens away from the rail,
// exactly as the strip's label does, so the two read as one behaviour at two
// widths.
func (m *OS) renderRailAddTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipRailAdd) {
		return nil
	}
	// Latched here rather than at the end, so a control whose row has since gone
	// still closes the tick gate instead of holding it open.
	m.Tooltip.Shown = true

	kind := sidebarRowKind(m.Tooltip.Key)
	row := -1
	for _, h := range m.SidebarHits {
		if h.Kind == kind && sidebarAddKind(h.Kind) {
			row = h.Y0
			break
		}
	}
	if row < 0 {
		return nil
	}

	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	label := tooltipLabel(sidebarAddWords(kind), max(renderW-railW-1, 1), theme.UI())

	x := railW
	if m.Settings.SidebarPosition == "right" {
		x = renderW - railW - lipgloss.Width(label)
	}
	return tooltipLayer(label, x, row, renderW, "sidebar-tooltip")
}

// sidebarTooltipBadgeLabel is what the alarm badge says in words. Empty when
// nothing is blocked, which is also when the badge is not drawn.
func sidebarTooltipBadgeLabel(info sidebarStripBadgeInfo) string {
	if info.Count == 0 {
		return ""
	}
	return strconv.Itoa(info.Count) + " " + plural("agent", info.Count) + " " + sidebarStateWords(info.State)
}

// sidebarTooltipSessionLabel is what a session cell says in words: the two
// things its two cells stand for, plus what is loud about it and for how long,
// which is the whole reason to hover a two-cell rail.
func sidebarTooltipSessionLabel(s sessiontree.Node) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " - "
	}
	label := printableTitle(s.Title) + sep + strconv.Itoa(s.WindowCount) + " " + plural("terminal", s.WindowCount)
	if sidebarAttention(s.AgentState) {
		loud := agentStateIndicator(s.AgentState) + " " + sidebarStateWords(s.AgentState)
		if age := agentElapsed(s.AgentState, s.StateAt, time.Now()); age != "" {
			loud += " " + age
		}
		label += "  " + loud
	}
	return label
}

// sidebarTooltipTerminalLabel is what one row of the strip's terminals group
// says in words: which pane it is, the workspace it went to when that is not
// this one, and what it is doing if it is doing anything. The two cells carry
// the focus mark and the state, so the name is the part only words can give.
func sidebarTooltipTerminalLabel(e sidebarTerminalEntry) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " - "
	}
	label := printableTitle(e.Title)
	if label == "" {
		label = "shell"
	}
	if e.Tag != "" {
		label += sep + e.Tag
	}
	if e.State != "" {
		label += sep + sidebarStateWords(e.State)
	}
	return label
}

// sidebarTooltipAgentLabel is what one row of the strip's agents group says in
// words: which pane it is, whose session it is in when that is not this one,
// which agent is running in it, what it is doing and for how long. The group's
// two cells carry the state and nothing else, so this is where everything the
// strip cannot draw lives, and the harness is the newest thing on that list.
func sidebarTooltipAgentLabel(e sidebarAgentEntry) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " - "
	}
	bare := printableTitle(e.Title)
	if bare == "" {
		bare = "shell"
	}
	name := bare
	if e.Foreign {
		if s := printableTitle(e.SessionLabel); s != "" {
			name = s + "/" + name
		}
	}
	label := name
	// Compared against the bare pane name, not the session-qualified one: the
	// duplicate the suppression is about is "claude · claude".
	if h := sidebarHarnessLabel(e.Harness); h != "" && !strings.EqualFold(h, bare) {
		label += sep + h
	}
	label += sep + sidebarStateWords(e.State)
	if age := agentElapsed(e.State, e.StateAt, time.Now()); age != "" {
		label += " " + age
	}
	return label
}

// sidebarStateWords is the human phrasing of an agent state, for the one place
// the rail spells a state out instead of drawing it.
func sidebarStateWords(state string) string {
	switch state {
	case "needs_input":
		return "need input"
	case "errored":
		return "errored"
	case "working":
		return "working"
	case "done":
		return "done"
	default:
		return "idle"
	}
}

// plural appends an s past one, so the label reads as a sentence.
func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// renderRailTooltip composes the hovered strip row's label as its own layer.
//
// The label is a single row on Surface: it anchors on the hovered line and opens
// away from the rail, so it never covers the cell it is describing, and it
// clamps to the pane area so a long session name truncates instead of running
// off the screen.
func (m *OS) renderRailTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipRailStrip) {
		return nil
	}
	// Latched here rather than at the end: a row with nothing to say still ends
	// the pending state, or the tick gate would be held open by a hover that is
	// never going to draw anything.
	m.Tooltip.Shown = true

	// The label anchors on the slot's first line rather than on the line the
	// pointer happens to be on, so it lands level with the mark it is naming and
	// with the top edge of the band under it. Both are drawn on Surface, so
	// aligned they read as one object opening out of the rail; a label floating
	// one row down read as a second thing that happened to be nearby.
	text, row := "", m.Tooltip.Key
	for _, r := range m.sidebarStripRows {
		if r.contains(m.Tooltip.Key) {
			text, row = r.Label, r.Y0
			break
		}
	}
	if text == "" {
		return nil
	}

	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	label := tooltipLabel(text, max(renderW-railW-1, 1), theme.UI())

	x := railW
	if m.Settings.SidebarPosition == "right" {
		// The rail is against the right edge, so the label opens leftward and
		// its right edge lands flush against the rail's first column.
		x = renderW - railW - lipgloss.Width(label)
	}
	return tooltipLayer(label, x, row, renderW, "sidebar-tooltip")
}
