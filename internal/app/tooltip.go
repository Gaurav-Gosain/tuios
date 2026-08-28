package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// A tooltip is what a control says when it has had to give up its words. Three
// surfaces need one: the collapsed rail, which says a whole session in two
// cells; the dock's session controls, which are a glyph each; and a dock
// workspace pill, which cuts a name off at twelve.
//
// None is the only way to the meaning, which is the condition for taking the
// words off at all. The expanded rail spells the sessions out, the help menu and
// the which-key sheet name both session actions for the people who never hover
// because they never reach for the mouse, and a workspace's full name is on its
// rename dialog and its context menu.
//
// It costs no standing tick. Hover-enter records the target and the instant;
// while the label is pending, TooltipPending joins tickNeedsWork exactly as the
// marquee does, so the maintenance tick runs for at most the delay window of a
// live gesture and then idles. A shown tooltip is static and persists in the
// last frame with nothing driving it.

const (
	// tooltipDelay is how long the pointer has to rest on a control before the
	// label appears. Long enough that crossing one on the way somewhere else
	// pops nothing.
	tooltipDelay = 350 * time.Millisecond
	// tooltipPad is the cell of air on each side of the text. A border would
	// make the label three rows tall and turn a glance into furniture.
	tooltipPad = 1
)

// tooltipSource is the surface a hover was recorded on. The words are resolved
// from that surface at draw time rather than copied in on the way past: a
// session's label carries how long it has been blocked, and copying it would
// freeze that at the moment the pointer stopped moving.
type tooltipSource int

const (
	// tooltipNone is no hover, and the zero value, so an unset state means the
	// pointer is on nothing that talks.
	tooltipNone tooltipSource = iota
	// tooltipRailStrip is a row of the collapsed rail.
	tooltipRailStrip
	// tooltipRailAdd is a section header's add control on the expanded rail. The
	// rest of that rail spells itself out in words and so says nothing here; a
	// one-cell "+" is the exception, since a glyph that has given up its words is
	// exactly what a label is for.
	tooltipRailAdd
	// tooltipDockSession is one of the dock's session controls.
	tooltipDockSession
	// tooltipDockWorkspace is a workspace pill on the dock strip whose name did
	// not fit the twelve cells the pill has.
	tooltipDockWorkspace
)

// tooltipState is the live hover. Runtime only, gesture-scoped like the marquee
// and the peek.
type tooltipState struct {
	// Source is the surface under the pointer. Key identifies the target within
	// it: the screen row for a rail strip row, the action for a dock control.
	Source tooltipSource
	Key    int
	// At is when the pointer arrived on this target.
	At time.Time
	// Shown latches on the frame that draws the label, so moving to the next
	// control swaps it instantly instead of waiting the delay out again: the
	// warm-state behaviour a browser's tab titles have. It is also what closes
	// the tick gate, so it latches on the drawing frame whether or not the
	// target turned out to have anything to say.
	Shown bool
}

// tooltipsEnabled reports whether a surface pops labels at all.
func (m *OS) tooltipsEnabled(src tooltipSource) bool {
	if !m.Settings.Tooltips || src == tooltipNone {
		return false
	}
	if src == tooltipRailStrip {
		// The expanded rail says all of it in words already, so a label over it
		// would only repeat what is on the screen.
		return sidebarVariant(m.GetSidebarWidth()) == sidebarVariantGlyph
	}
	if src == tooltipRailAdd {
		// The mirror of the rule above: these controls only exist on the expanded
		// rail, and they are the one thing on it drawn as a bare glyph.
		return sidebarVariant(m.GetSidebarWidth()) != sidebarVariantGlyph
	}
	if src == tooltipDockWorkspace {
		return m.Settings.DockWorkspaceTooltip
	}
	return true
}

// tooltipTrack records the pointer landing on a target. Called from the motion
// handlers, which are the only things that know the pointer moved.
func (m *OS) tooltipTrack(src tooltipSource, key int) {
	if !m.tooltipsEnabled(src) {
		m.tooltipClear()
		return
	}
	if m.Tooltip.Source == src && m.Tooltip.Key == key {
		return // already on this target; the clock keeps running
	}
	m.Tooltip = tooltipState{Source: src, Key: key, At: time.Now(), Shown: m.Tooltip.Shown}
}

// tooltipClear drops the hover and the latch. Called when the pointer leaves a
// surface, when anything is pressed, and whenever a surface stops being the kind
// that talks.
func (m *OS) tooltipClear() { m.Tooltip = tooltipState{} }

// tooltipVisible reports whether src's label should be drawn this frame: the
// pointer is on one of its targets and has been there long enough, or a label is
// already up and has only moved to a neighbour.
func (m *OS) tooltipVisible(src tooltipSource) bool {
	if m.Tooltip.Source != src || !m.tooltipsEnabled(src) {
		return false
	}
	return m.Tooltip.Shown || time.Since(m.Tooltip.At) >= tooltipDelay
}

// TooltipPending reports whether a label is waiting to be drawn, which is the
// only state that needs the maintenance tick: nothing else will bring the frame
// that draws it. It goes false on that frame, so a shown tooltip is free and the
// pointer at rest anywhere else costs nothing. Bounded by the delay: at worst it
// holds the tick for one gesture's 350 ms.
func (m *OS) TooltipPending() bool {
	return m.tooltipsEnabled(m.Tooltip.Source) && !m.Tooltip.Shown
}

// tooltipLabel styles the words and truncates them to the room they have, so a
// long session name ends in an ellipsis rather than off the side of the screen.
func tooltipLabel(text string, room int, pal overlay.Palette) string {
	pad := strings.Repeat(" ", tooltipPad)
	body := pad + overlay.Truncate(text, max(room-2*tooltipPad, 1)) + pad
	return lipgloss.NewStyle().Background(pal.Surface).Foreground(pal.Fg).Render(body)
}

// tooltipLayer floats a composed label above the panes and the dock. x is where
// the label would like to start; it is clamped so the label cannot leave the
// screen either way, which for a control at the bar's right-hand end means the
// label opens leftward from it.
func tooltipLayer(label string, x, y, renderW int, id string) *lipgloss.Layer {
	x = max(min(x, renderW-lipgloss.Width(label)), 0)
	return lipgloss.NewLayer(label).X(x).Y(max(y, 0)).Z(config.ZIndexDock + 1).ID(id)
}

// renderTooltip composes whichever surface's label is live. Nil when there is
// nothing to show.
func (m *OS) renderTooltip() *lipgloss.Layer {
	switch m.Tooltip.Source {
	case tooltipRailStrip:
		return m.renderRailTooltip()
	case tooltipRailAdd:
		return m.renderRailAddTooltip()
	case tooltipDockSession:
		return m.renderDockSessionTooltip()
	case tooltipDockWorkspace:
		return m.renderDockWorkspaceTooltip()
	}
	return nil
}
