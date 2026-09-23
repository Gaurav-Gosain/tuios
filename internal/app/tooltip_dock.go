package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The dock's session controls are a glyph each, so the words that used to sit
// beside them live here. This is the dock's half of the shared tooltip in
// tooltip.go: which control the pointer is on, and where its label goes.

// dockSessionTooltipTrack arms the label for whichever control the pointer
// landed on, taking the control the hover pass already resolved rather than
// hit-testing the row a second time.
//
// It clears only its own hover. The rail band consumes motion over itself before
// this runs, so a pointer that reaches the dock is a pointer that has left the
// rail, and the rail's own handler has already dropped its label.
func (m *OS) dockSessionTooltipTrack(a DockSessionAction) {
	if a == DockSessionNone {
		if m.Tooltip.Source == tooltipDockSession {
			m.tooltipClear()
		}
		return
	}
	m.tooltipTrack(tooltipDockSession, int(a))
}

// DockHoverActive reports whether the dock is showing something the pointer
// put there: a brightened session control, or a label pending or up on a
// control or a pill. The motion filter passes one more event while it is, so
// the pointer leaving the band is the event that clears it.
func (m *OS) DockHoverActive() bool {
	return m.dockSessionHover != DockSessionNone ||
		m.Tooltip.Source == tooltipDockSession || m.Tooltip.Source == tooltipDockWorkspace
}

// renderDockSessionTooltip composes the hovered control's label.
//
// It sits one row off the bar, on the hairline the dock already owns. The bar is
// a single row, so a label on it would be drawn over the very glyph the pointer
// is asking about; going up (or down, for a dock at the top) is the only
// placement that leaves the control visible while its name is up.
//
// The anchor is the control's recorded first column, and tooltipLayer clamps it
// to the screen. These two controls hold the bar's right-hand end, so in
// practice the label always opens leftward from them.
func (m *OS) renderDockSessionTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipDockSession) {
		return nil
	}
	// Latched here for the reason the rail's is: a control that has since left
	// the frame still ends the pending state, or the tick gate stays open on a
	// hover that will never draw anything.
	m.Tooltip.Shown = true

	for _, h := range m.dockSessionHits {
		if int(h.Action) != m.Tooltip.Key {
			continue
		}
		renderW := m.GetRenderWidth()
		label := tooltipLabel(dockSessionLabel(h.Action), renderW, theme.UI())
		y := h.Y - 1
		if m.Settings.DockbarPosition == "top" {
			y = h.Y + 1
		}
		return tooltipLayer(label, h.X0, y, renderW, "dock-session-tooltip")
	}
	return nil
}

// dockWorkspaceTooltipTrack arms the label for the workspace pill under the
// pointer. A pill whose name fits arms nothing: it is already saying all of it,
// so a label would repeat the screen and hold the maintenance tick open across
// the delay for no reason. The "+" tab resolves to no workspace and is skipped
// with it.
func (m *OS) dockWorkspaceTooltipTrack(ws int) {
	if ws <= 0 || !m.workspacePillClipped(ws) {
		if m.Tooltip.Source == tooltipDockWorkspace {
			m.tooltipClear()
		}
		return
	}
	m.tooltipTrack(tooltipDockWorkspace, ws)
}

// renderDockWorkspaceTooltip says the hovered pill's name in full.
//
// It is placed exactly as the session controls' label is, one row off the bar,
// anchored to the pill's recorded first column so it opens under the name it is
// finishing. tooltipLayer clamps it, so a pill near the right-hand end opens
// leftward instead of running off the screen.
//
// The pill itself is untouched: its columns are the columns the strip measured
// and the renderer recorded, and the label floats on the row above them. Nothing
// about the strip reflows while the label is up, so the rectangle under the
// pointer is still the rectangle a click lands on.
func (m *OS) renderDockWorkspaceTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipDockWorkspace) {
		return nil
	}
	// Latched for the reason the other two are: a pill scrolled out of the strip
	// still ends the pending state, or the tick gate stays open forever.
	m.Tooltip.Shown = true

	for _, h := range m.dockWorkspaceHits {
		if h.Workspace != m.Tooltip.Key {
			continue
		}
		renderW := m.GetRenderWidth()
		label := tooltipLabel(m.workspacePillName(h.Workspace), renderW, theme.UI())
		y := h.Y - 1
		if m.Settings.DockbarPosition == "top" {
			y = h.Y + 1
		}
		return tooltipLayer(label, h.X0, y, renderW, "dock-workspace-tooltip")
	}
	return nil
}

// DockWorkspaceHoverAt arms the pill label for whatever workspace pill covers
// (x, y), and drops it when the pointer is on none. It reports whether the
// pointer is on a pill.
//
// Like the session controls' hover it does not consume the motion: the strip has
// no other reaction to a pointer crossing it, and the arriving motion is the
// only clock the label has.
func (m *OS) DockWorkspaceHoverAt(x, y int) bool {
	ws := m.DockWorkspacePillAt(x, y)
	m.dockWorkspaceTooltipTrack(ws)
	return ws > 0
}

// dockHoverChangesAt reports whether a motion to (x, y) would change what the
// dock shows: which session control is lit, or which dock label is pending or
// up. It resolves the targets the motion handler resolves, DockSessionHoverAt
// and then DockWorkspaceHoverAt, from the same hit lists, so a motion it turns
// down is one the handler would have answered by writing the state it already
// had. The motion filter asks this so a pointer crossing bare band, or moving
// inside one control, does not compose a frame per cell identical to the last.
// TestDockHoverChangesAtAgreesWithTheHandler holds the two together.
func (m *OS) dockHoverChangesAt(x, y int) bool {
	a := m.DockSessionActionAt(x, y)
	if a != m.dockSessionHover {
		return true
	}
	src, key := tooltipNone, 0
	if a != DockSessionNone {
		src, key = tooltipDockSession, int(a)
	} else if ws := m.DockWorkspacePillAt(x, y); ws > 0 && m.workspacePillClipped(ws) {
		src, key = tooltipDockWorkspace, ws
	}
	switch {
	case src == tooltipNone:
		// Nothing to arm: the handler drops a dock label and leaves any other.
		return m.Tooltip.Source == tooltipDockSession || m.Tooltip.Source == tooltipDockWorkspace
	case !m.tooltipsEnabled(src):
		// tooltipTrack clears whatever label is pending or up.
		return m.Tooltip.Source != tooltipNone
	default:
		return m.Tooltip.Source != src || m.Tooltip.Key != key
	}
}
