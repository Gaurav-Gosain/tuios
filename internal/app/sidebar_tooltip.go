package app

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The collapsed strip says everything in two cells, which is enough to steer by
// and not enough to read. A tooltip fills that gap for the pointer only: the
// expanded rail says all of it in words, so nothing here is exclusive.
//
// It costs no standing tick. Hover-enter records the row and the instant; while
// the label is pending, sidebarTooltipPending joins tickNeedsWork exactly as the
// marquee does, so the maintenance tick runs for at most the delay window of a
// live gesture and then idles. A shown tooltip is static and persists in the
// last frame with nothing driving it.

const (
	// sidebarTooltipDelay is how long the pointer has to rest on a strip row
	// before the label appears. Long enough that crossing the strip on the way
	// somewhere else pops nothing.
	sidebarTooltipDelay = 350 * time.Millisecond
	// sidebarTooltipPad is the cell of air on each side of the text. A border
	// would make the label three rows tall and turn a glance into furniture.
	sidebarTooltipPad = 1
)

// sidebarTooltipState is the live hover: which strip line the pointer is on and
// when it arrived. Runtime only, gesture-scoped like the marquee and the peek.
type sidebarTooltipState struct {
	// Active is set while the pointer rests on a strip row. Y is that row's
	// absolute screen line and At is when the pointer arrived on it.
	Active bool
	Y      int
	At     time.Time
	// Shown latches on the frame that draws the label, so moving to another
	// strip row swaps it instantly instead of waiting the delay out again: the
	// warm-state behaviour a browser's tab titles have. It is also what closes
	// the tick gate, so it latches on the drawing frame whether or not the row
	// turned out to have anything to say.
	Shown bool
}

// sidebarTooltipsEnabled reports whether the strip pops labels at all.
func (m *OS) sidebarTooltipsEnabled() bool {
	return config.SidebarTooltips && sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph
}

// sidebarTooltipTrack records the pointer landing on a strip row. Called from
// the motion handler, which is the only thing that knows the pointer moved.
func (m *OS) sidebarTooltipTrack(y int) {
	if !m.sidebarTooltipsEnabled() {
		m.sidebarTooltipClear()
		return
	}
	for _, r := range m.sidebarStripRows {
		if r.Y != y {
			continue
		}
		if m.SidebarTooltip.Active && m.SidebarTooltip.Y == y {
			return // already on this row; the clock keeps running
		}
		// Warm state: with a label already up, moving along the strip swaps it
		// with no second wait, which is what makes browsing the strip work.
		m.SidebarTooltip = sidebarTooltipState{Active: true, Y: y, At: time.Now(), Shown: m.SidebarTooltip.Shown}
		return
	}
	m.sidebarTooltipClear()
}

// sidebarTooltipClear drops the hover and the latch. Called when the pointer
// leaves the band, when anything is pressed, and whenever the rail stops being
// a strip.
func (m *OS) sidebarTooltipClear() { m.SidebarTooltip = sidebarTooltipState{} }

// sidebarTooltipVisible reports whether the label should be drawn this frame:
// the pointer is on a strip row and has been there long enough, or a label is
// already up and has only moved along the strip.
func (m *OS) sidebarTooltipVisible() bool {
	if !m.sidebarTooltipsEnabled() || !m.SidebarTooltip.Active {
		return false
	}
	return m.SidebarTooltip.Shown || time.Since(m.SidebarTooltip.At) >= sidebarTooltipDelay
}

// SidebarTooltipPending reports whether a label is waiting to be drawn, which is
// the only state that needs the maintenance tick: nothing else will bring the
// frame that draws it. It goes false on that frame, so a shown tooltip is free
// and the pointer at rest anywhere else costs nothing. Bounded by the delay: at
// worst it holds the tick for one gesture's 350 ms.
func (m *OS) SidebarTooltipPending() bool {
	return m.sidebarTooltipsEnabled() && m.SidebarTooltip.Active && !m.SidebarTooltip.Shown
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

// renderSidebarTooltip composes the hovered strip row's label as its own layer,
// above the panes and the dock. Nil when there is nothing to show.
//
// The label is a single row on Surface: it anchors on the hovered line and
// opens away from the rail, so it never covers the cell it is describing, and
// it clamps to the pane area so a long session name truncates instead of
// running off the screen.
func (m *OS) renderSidebarTooltip() *lipgloss.Layer {
	if !m.sidebarTooltipVisible() {
		return nil
	}
	// Latched here rather than at the end: a row with nothing to say still ends
	// the pending state, or the tick gate would be held open by a hover that is
	// never going to draw anything.
	m.SidebarTooltip.Shown = true

	railW := m.GetSidebarWidth()
	text := ""
	for _, r := range m.sidebarStripRows {
		if r.Y == m.SidebarTooltip.Y {
			text = r.Label
			break
		}
	}
	if text == "" {
		return nil
	}

	avail := max(m.GetRenderWidth()-railW-1, 1)
	pad := strings.Repeat(" ", sidebarTooltipPad)
	body := pad + overlay.Truncate(text, max(avail-2*sidebarTooltipPad, 1)) + pad

	pal := theme.UI()
	label := lipgloss.NewStyle().Background(pal.Surface).Foreground(pal.Fg).Render(body)

	x := railW
	if config.SidebarPosition == "right" {
		// The rail is against the right edge, so the label opens leftward and
		// its right edge lands flush against the rail's first column.
		x = m.GetRenderWidth() - railW - lipgloss.Width(label)
	}
	return lipgloss.NewLayer(label).X(max(x, 0)).Y(m.SidebarTooltip.Y).Z(config.ZIndexDock + 1).ID("sidebar-tooltip")
}
