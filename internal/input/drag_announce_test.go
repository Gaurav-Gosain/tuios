package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// A drag is a gesture, and the sizes it passes through on the way are not sizes
// the guest was ever meant to live at. A borderless pane gives its shared-border
// allowance up on the first motion so it can draw its own frame, and the retile
// on the drop gives it back. In a tiled layout the pane usually lands in a slot
// the same size as the one it left, so both of those were avoidable, and each of
// them reaches the shell as a SIGWINCH it answers by repainting its prompt.
//
// These count the sizes the guest was handed, not the size it ends up with. The
// drop puts the size back, so a check made after the gesture reads the right
// number for the wrong reason.
//
// They drive OS.Update rather than the handlers directly, because the hold is
// armed by the press in Update: a test that called handleMouseClick would enter
// the code below the arming and prove nothing about whether anything arms it.

// countSizes records every size a pane is handed from now on. The layout is
// announced once first, which is the state a session is in before a gesture.
func countSizes(win *terminal.Window) *[][2]int {
	win.Resize(win.Width, win.Height)
	var got [][2]int
	win.DaemonResizeFunc = func(w, h int) error {
		got = append(got, [2]int{w, h})
		return nil
	}
	return &got
}

// TestDragBackIntoASameSizeSlotTellsTheGuestNothing is the regression. The
// dragged pane is dropped on its neighbour, which the split gave the same
// rectangle, so the two swap and the pane ends the gesture exactly the size it
// started.
func TestDragBackIntoASameSizeSlotTellsTheGuestNothing(t *testing.T) {
	app.SetInputHandler(HandleInput)
	withSharedBorders(t)
	withClickToType(t, config.ClickToTypeDouble)
	o, wa, wb := twoPaneBSP(t)
	left, right := leftPaneOf(wa, wb)
	left.Resize(left.Width, left.Height)
	told := countSizes(right)

	cx, cy := contentCell(right)
	o.Update(clickMsg(cx, cy))
	for step := 1; step <= 20; step++ {
		o.Update(motionMsg(cx-step, cy))
	}
	o.Update(releaseMsg(cx-20, cy))

	if len(*told) != 0 {
		t.Errorf("a drag that put the pane back at the same size told the guest %v, "+
			"want nothing: every one of those is a SIGWINCH the shell repaints for", *told)
	}
	if !right.Tiled {
		t.Error("the pane never got its borderless allowance back after the drop")
	}
}

// TestDraggingTheDividerTellsTheGuestOnce is the positive half, in the same
// fixture. Moving the shared border really does change how many columns each
// pane has, so the guest has to be told - once, for the width it settled at,
// not once per column the pointer crossed.
//
// Without this the test above could pass because the fixture never resizes
// anything at all.
func TestDraggingTheDividerTellsTheGuestOnce(t *testing.T) {
	app.SetInputHandler(HandleInput)
	withSharedBorders(t)
	withClickToType(t, config.ClickToTypeDouble)
	o, wa, wb := twoPaneBSP(t)
	left, right := leftPaneOf(wa, wb)
	left.Resize(left.Width, left.Height)
	told := countSizes(right)

	// The separator cell between the two tiles, dragged eight columns left.
	dividerX := left.X + left.Width
	rowY := right.Y + right.Height/2
	o.Update(clickMsg(dividerX, rowY))
	for step := 1; step <= 8; step++ {
		o.Update(motionMsg(dividerX-step, rowY))
	}
	o.Update(releaseMsg(dividerX-8, rowY))

	if len(*told) != 1 {
		t.Fatalf("dragging the divider across eight columns told the guest %v, want one size: "+
			"either the fixture cannot resize anything, in which case the test above proves "+
			"nothing, or the columns the pointer crossed are reaching the guest", *told)
	}
	if gotW := (*told)[0][0]; gotW != right.ContentWidth() {
		t.Errorf("the guest was told %d columns and the pane is %d wide", gotW, right.ContentWidth())
	}
}
