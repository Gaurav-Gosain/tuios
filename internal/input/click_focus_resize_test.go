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

// TestClickToFocusDoesNotResizeABorderlessPane is the regression. The policy is
// "double" because that is the one that leaves the user in window-management
// mode on a single click, which is the mode the drag setup runs in; the same
// press does the same thing under every policy.
func TestClickToFocusDoesNotResizeABorderlessPane(t *testing.T) {
	withSharedBorders(t)
	withClickToType(t, config.ClickToTypeDouble)
	o, wa, wb := twoPaneBSP(t)
	left, right := leftPaneOf(wa, wb)

	// twoPaneBSP writes the rectangles straight onto the windows, so nothing has
	// been announced yet. Announce the settled layout once, which is the state a
	// real session is in before the first click, and only then start counting.
	left.Resize(left.Width, left.Height)
	right.Resize(right.Width, right.Height)

	var told [][2]int
	right.DaemonResizeFunc = func(w, h int) error {
		told = append(told, [2]int{w, h})
		return nil
	}

	for i := range 3 {
		agePane(left)
		agePane(right)
		lx, ly := contentCell(left)
		clickPane(o, lx, ly)
		rx, ry := contentCell(right)
		clickPane(o, rx, ry)

		if len(told) != 0 {
			t.Fatalf("round trip %d: the clicked pane was told %v, want nothing: "+
				"a click that only focuses must not SIGWINCH the shell", i+1, told)
		}
		if !right.Tiled {
			t.Fatalf("round trip %d: the clicked pane lost its borderless allowance", i+1)
		}
	}
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
