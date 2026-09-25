package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A click that only picks a pane must not resize the guest inside it.
//
// With shared borders the panes are borderless, so the whole rectangle is the
// guest's. The drag setup used to take that allowance away on the press, which
// costs the guest the two rows and two columns the border now occupies, and the
// retile on release gives them back. Both are real size changes, both reach the
// shell as a SIGWINCH, and a shell repaints its prompt on each one, so picking a
// pane with the mouse left a new line in it every time.
//
// What these assert on is how many sizes the guest was handed, not the size it
// ends up with. The retile on release puts the size back, so a check made after
// the click reads the right number for the wrong reason.

// withSharedBorders turns shared borders on for a test and restores the
// previous value after.
func withSharedBorders(t *testing.T) {
	t.Helper()
	prev := config.Global.SharedBorders
	config.Global.SharedBorders = true
	t.Cleanup(func() { config.Global.SharedBorders = prev })
}

// TestDragStillUntilesTheGrabbedPane is the other half: the pane does give the
// allowance up, on the motion that makes the gesture a move rather than a
// click, or a dragged pane draws no border of its own.
func TestDragStillUntilesTheGrabbedPane(t *testing.T) {
	withSharedBorders(t)
	withClickToType(t, config.ClickToTypeDouble)
	o, wa, wb := twoPaneBSP(t)
	_, right := leftPaneOf(wa, wb)
	cx, cy := contentCell(right)

	handleMouseClick(clickMsg(cx, cy), o)
	if !right.Tiled {
		t.Fatalf("the press alone untiled the pane")
	}
	handleMouseMotion(motionMsg(cx+12, cy+4), o)
	if right.Tiled {
		t.Errorf("the pane kept its borderless allowance through a drag, so it draws no border")
	}
	handleMouseRelease(releaseMsg(cx+12, cy+4), o)
}
