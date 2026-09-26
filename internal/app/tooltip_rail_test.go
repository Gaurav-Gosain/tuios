package app

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// tooltipOS renders a collapsed strip once so the strip rows exist, then hands
// back the y of the row of the given kind.
func tooltipOS(t *testing.T, pos string, kind sidebarStripRowKind) (*OS, int) {
	t.Helper()
	m, tree := stripOS(t, 120, 20)
	withSidebar(t, true, pos, config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarCollapsed = true
	m.sidebarPanelLinesForTree(tree)
	for _, r := range m.sidebarStripRows {
		if r.Kind == kind {
			return m, r.Y0
		}
	}
	t.Fatalf("the strip drew no row of kind %v", kind)
	return nil, 0
}

// TestTooltipPendsOnlyDuringALiveHover is the tick-idle guarantee. A leaked
// pending flag ticks forever, which is the one thing this feature is not
// allowed to cost.
func TestTooltipPendsOnlyDuringALiveHover(t *testing.T) {
	m, y := tooltipOS(t, "left", sidebarStripSession)

	if m.TooltipPending() {
		t.Fatal("a rail nobody is pointing at is pending a tooltip")
	}
	m.SidebarMotion(1, y)
	if !m.TooltipPending() {
		t.Fatal("landing on a strip row did not arm the tooltip")
	}
	if m.tooltipVisible(tooltipRailStrip) {
		t.Error("the tooltip appeared before its delay elapsed")
	}
	if !m.tickNeedsWork() {
		t.Error("a pending tooltip does not hold the maintenance tick, so nothing will draw it")
	}

	// The delay elapses and the frame that draws it closes the gate.
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	if !m.tooltipVisible(tooltipRailStrip) {
		t.Fatal("the tooltip never became visible")
	}
	if m.renderRailTooltip() == nil {
		t.Fatal("a visible tooltip composed no layer")
	}
	if m.TooltipPending() {
		t.Error("a shown tooltip is still pending; the tick would never idle")
	}

	// And leaving the band takes it down entirely.
	m.SidebarMotion(m.GetRenderWidth()-2, y)
	if m.TooltipPending() || m.tooltipVisible(tooltipRailStrip) {
		t.Error("leaving the band left the tooltip behind")
	}
}

// TestTooltipPendingClosesOnASilentRow: the gate is closed by
// the drawing frame, not by the label, or a silent row would hold the tick open
// for as long as the pointer sat on it.
func TestTooltipPendingClosesOnASilentRow(t *testing.T) {
	m, y := tooltipOS(t, "left", sidebarStripBadge)
	m.SidebarMotion(1, y)
	// A row the renderer had nothing to say about.
	for i := range m.sidebarStripRows {
		m.sidebarStripRows[i].Label = ""
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)

	if m.renderRailTooltip() != nil {
		t.Fatal("a row with no label drew one anyway")
	}
	if m.TooltipPending() {
		t.Error("a row with nothing to say left the tooltip pending forever")
	}
}
