package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// The rail consumes motion over its band, so the pointer-shape pass never runs
// there: a shape set on the last pane cell before the rail (grab on a title
// bar, resize on a divider) rode across the whole rail and stuck, worst on a
// slow pass where every cell the pointer crossed was consumed. The rail owns
// no shape of its own, so the motion it consumes must hand the pointer back to
// default.
func TestRailMotionResetsPointerShape(t *testing.T) {
	o := hoverOS(t)
	o.SetPointerShape(app.PointerGrab)
	if o.CurrentPointer() != app.PointerGrab {
		t.Fatal("preset did not take; the test asserts nothing")
	}

	handleMouseMotion(tea.MouseMotionMsg{X: 5, Y: 10}, o)

	if got := o.CurrentPointer(); got != app.PointerDefault {
		t.Fatalf("pointer over the rail is %q, want default: a stale shape sticks while the rail consumes motion", got)
	}
}

// A motion over a pane still runs the geometry pass, so the shapes the pane
// chrome offers are unchanged by the rail fix.
func TestPaneMotionStillShapesThePointer(t *testing.T) {
	o := hoverOS(t)

	// The editor pane's top border row: the geometry pass offers grab there.
	handleMouseMotion(tea.MouseMotionMsg{X: 40, Y: 1}, o)

	if got := o.CurrentPointer(); got != app.PointerGrab {
		t.Fatalf("pointer over a pane title bar is %q, want grab: the geometry pass must keep owning pane chrome", got)
	}
}
