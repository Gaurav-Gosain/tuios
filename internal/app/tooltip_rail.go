package app

import (
	"strconv"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The collapsed strip says everything in two cells, which is enough to steer by
// and not enough to read. The shared tooltip in tooltip.go fills that gap for
// the pointer only; this is the rail's half of it, the words and where they go.

// sidebarTooltipTrack records the pointer landing on a strip row. Called from
// the motion handler, which is the only thing that knows the pointer moved.
func (m *OS) sidebarTooltipTrack(y int) {
	for _, r := range m.sidebarStripRows {
		if r.contains(y) {
			m.tooltipTrack(tooltipRailStrip, y)
			return
		}
	}
	m.tooltipClear()
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

	text := ""
	for _, r := range m.sidebarStripRows {
		if r.contains(m.Tooltip.Key) {
			text = r.Label
			break
		}
	}
	if text == "" {
		return nil
	}

	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	label := tooltipLabel(text, max(renderW-railW-1, 1), theme.UI())

	x := railW
	if config.SidebarPosition == "right" {
		// The rail is against the right edge, so the label opens leftward and
		// its right edge lands flush against the rail's first column.
		x = renderW - railW - lipgloss.Width(label)
	}
	return tooltipLayer(label, x, m.Tooltip.Key, renderW, "sidebar-tooltip")
}
