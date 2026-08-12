package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
		if config.DockbarPosition == "top" {
			y = h.Y + 1
		}
		return tooltipLayer(label, h.X0, y, renderW, "dock-session-tooltip")
	}
	return nil
}
