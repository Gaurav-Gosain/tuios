package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func countSizes(win *terminal.Window) *[][2]int {
	win.Resize(win.Width, win.Height)
	var got [][2]int
	win.DaemonResizeFunc = func(w, h int) error {
		got = append(got, [2]int{w, h})
		return nil
	}
	return &got
}

// TestDraggingTheDividerTellsTheGuestOnce is the positive half, in the same
// fixture. Moving the shared border really does change how many columns each
// pane has, so the guest has to be told once, for the width it settled at,
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
