package app

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func dockTooltipAt(t *testing.T, m *OS, a DockSessionAction) (*lipgloss.Layer, dockSessionHit) {
	t.Helper()
	m.renderDockString()
	for _, h := range m.dockSessionHits {
		if h.Action != a {
			continue
		}
		if !m.DockSessionHoverAt(h.X0, h.Y) {
			t.Fatalf("the pointer on column %d of %v was not on a control", h.X0, a)
		}
		if !m.TooltipPending() {
			t.Fatalf("hovering %v armed no label", a)
		}
		m.Tooltip.At = time.Now().Add(-2 * m.Settings.TooltipDelay())
		return m.renderTooltip(), h
	}
	t.Fatalf("the dock drew no control for %v", a)
	return nil, dockSessionHit{}
}

// TestDockSessionTooltipCostsNoIdleTick: the pending flag is the only thing that
// holds the maintenance tick open, and it closes on the frame that draws the
// label. A tooltip left up must not keep the app awake.
func TestDockSessionTooltipCostsNoIdleTick(t *testing.T) {
	m := dockSessionOS(t, 160, true)
	if m.TooltipPending() {
		t.Fatal("a fresh dock is already pending a label")
	}
	if _, _ = dockTooltipAt(t, m, DockSessionClose); m.TooltipPending() {
		t.Error("the label has been drawn and is still holding the tick open")
	}
}
