package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A snap zone belongs on the edge of the region panes occupy, which a sidebar
// shortens exactly as the dock shortens the height. Reading the raw screen put
// the right-hand zone under the rail: releasing at the visible right edge
// snapped nothing, and reaching the zone meant dragging into the sidebar.
func TestSnapZonesFollowTheContentRegion(t *testing.T) {
	const cols, rows, rail = 120, 40, 24

	for _, side := range []string{"right", "left"} {
		t.Run(side, func(t *testing.T) {
			swapSidebar(t, side, rail)
			m := &OS{Settings: config.Global, Width: cols, Height: rows}

			left := m.GetLeftMargin()
			right := m.GetRenderWidth() - m.GetRightMargin()
			midY := m.GetTopMargin() + m.GetUsableHeight()/2

			if got := m.SnapZoneAt(right-1, midY); got != SnapRight {
				t.Errorf("released on the content's right edge (x=%d): got %v, want SnapRight", right-1, got)
			}
			if got := m.SnapZoneAt(left, midY); got != SnapLeft {
				t.Errorf("released on the content's left edge (x=%d): got %v, want SnapLeft", left, got)
			}

			// Releasing over the rail still snaps toward it. The zone is open-ended
			// past the content edge on purpose: a pointer dragged that way is heading
			// right, and the rail is not a drop target of its own, so refusing here
			// would make an obvious gesture do nothing.
			var overRail int
			want := SnapRight
			if side == "right" {
				overRail = right + rail/2
			} else {
				overRail = rail / 2
				want = SnapLeft
			}
			if got := m.SnapZoneAt(overRail, midY); got != want {
				t.Errorf("released over the %s rail (x=%d): got %v, want %v", side, overRail, got, want)
			}

			// The middle of the pane region is not a zone at either edge.
			if got := m.SnapZoneAt(left+(right-left)/2, midY); got != NoSnap {
				t.Errorf("mid-content: got %v, want NoSnap", got)
			}
		})
	}
}

// With no sidebar the zones sit on the screen edges, which is the behaviour the
// content-region rule has to reduce to.
func TestSnapZonesWithoutASidebarSitOnTheScreen(t *testing.T) {
	swapSidebar(t, "right", 0)
	config.Global.SidebarEnabled = false

	m := &OS{Settings: config.Global, Width: 120, Height: 40}
	midY := m.GetTopMargin() + m.GetUsableHeight()/2

	if got := m.SnapZoneAt(m.GetRenderWidth()-1, midY); got != SnapRight {
		t.Errorf("no sidebar, right screen edge: got %v, want SnapRight", got)
	}
	if got := m.SnapZoneAt(0, midY); got != SnapLeft {
		t.Errorf("no sidebar, left screen edge: got %v, want SnapLeft", got)
	}
	if got := m.SnapZoneAt(60, midY); got != NoSnap {
		t.Errorf("mid-screen: got %v, want NoSnap", got)
	}
}

func swapSidebar(t *testing.T, pos string, width int) {
	t.Helper()
	oe, op, ow := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
	config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = true, pos, width
	t.Cleanup(func() {
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = oe, op, ow
	})
}
