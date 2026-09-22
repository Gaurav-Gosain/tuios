package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The keyboard height paths decide whether an edge is the content region's
// boundary or a divider. They measured the top boundary as row 0 and the bottom
// one as the usable height, which is right only while nothing is reserved above
// the panes. With a top reserve, the top pane's edge was taken for a divider and
// moved off the boundary, and a divider near the bottom was taken for the
// boundary, so the key moved the pane's top edge instead of the divider.

// reservedStack is two panes stacked in one column, laid out under a session
// reserve of six rows at the top. ratio is the top pane's share.
func reservedStack(t *testing.T, ratio float64) (m *app.OS, top, bottom func() (y, h int), topMargin int) {
	t.Helper()
	m = isoOS(t, 2)
	m.SessionReserve = session.LayoutReserve{Top: 6}
	buildTree(m, func(leaf func(int) *layout.TileNode) *layout.TileNode {
		return h(ratio, leaf(1), leaf(2))
	})
	if m.GetTopMargin() != 6 {
		t.Fatalf("top margin is %d, want the reserved 6", m.GetTopMargin())
	}
	top = func() (int, int) { return m.Windows[0].Y, m.Windows[0].Height }
	bottom = func() (int, int) { return m.Windows[1].Y, m.Windows[1].Height }
	m.FocusedWindow = 0
	return m, top, bottom, m.GetTopMargin()
}

// TestHeightTopKeyLeavesAPaneOnTheReservedTopEdge checks that shrinking a pane
// from the top does nothing when its top edge is the region's top boundary.
func TestHeightTopKeyLeavesAPaneOnTheReservedTopEdge(t *testing.T) {
	m, top, _, margin := reservedStack(t, 0.5)
	y0, h0 := top()
	if y0 != margin {
		t.Fatalf("the top pane starts on row %d, want the top margin %d", y0, margin)
	}

	m.ResizeFocusedWindowHeightTop(1)

	if y, hgt := top(); y != y0 || hgt != h0 {
		t.Errorf("the top pane moved from row %d height %d to row %d height %d; its top edge is the boundary",
			y0, h0, y, hgt)
	}
}

// TestHeightKeyMovesADividerNearTheBottom checks that the height key moves the
// divider under a pane whose bottom edge is a divider close to the screen
// bottom, rather than treating that edge as the boundary.
func TestHeightKeyMovesADividerNearTheBottom(t *testing.T) {
	m, top, bottom, margin := reservedStack(t, 0.8)
	y0, h0 := top()
	by0, _ := bottom()
	if y0 != margin {
		t.Fatalf("the top pane starts on row %d, want the top margin %d", y0, margin)
	}

	m.ResizeFocusedWindowHeight(-1)

	y, hgt := top()
	if y != y0 {
		t.Errorf("the top pane's top edge moved from row %d to %d; the key should move the divider", y0, y)
	}
	if hgt != h0-1 {
		t.Errorf("the top pane is %d rows, want %d", hgt, h0-1)
	}
	if by, _ := bottom(); by != by0-1 {
		t.Errorf("the bottom pane starts on row %d, want %d", by, by0-1)
	}
}
