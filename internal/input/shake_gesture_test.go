package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func shakeReady(t *testing.T) *OS2 {
	t.Helper()
	app.SetInputHandler(HandleInput)
	withSharedBorders(t)
	withClickToType(t, config.ClickToTypeDouble)
	o, _, _ := twoPaneBSP(t)
	o.UserConfig = config.DefaultConfig()
	o.UserConfig.Spotlight.Shake = true
	o.ConfigReadOnly = true
	return o
}

func hoverMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{Button: tea.MouseNone, X: x, Y: y}
}

const shakeTurns = 12

func shakeAt(o *OS2, x, y, turns int) {
	o.Update(hoverMsg(x, y))
	at, dir := x, 1
	for range turns + 1 {
		at += dir * 20
		dir = -dir
		o.Update(hoverMsg(at, y))
	}
}

// TestShakingTheRealPointerTogglesTheBeam is the positive half. Without it the
// three below could all pass in a fixture where the gesture can never fire at
// all, and the whole file would be proving nothing.
func TestShakingTheRealPointerTogglesTheBeam(t *testing.T) {
	o := shakeReady(t)

	shakeAt(o, 60, 20, shakeTurns)

	if !o.SpotlightOn() {
		t.Fatal("shaking the pointer through Update did not turn the beam on; " +
			"the detector is not on the path a real pointer takes")
	}
}

// TestADividerDragDoesNotToggleTheBeam. Sizing a pane by the border it shares
// with its neighbour is a fast wide back-and-forth with the button down, and it
// is the movement a person makes most often that looks most like a shake.
func TestADividerDragDoesNotToggleTheBeam(t *testing.T) {
	o := shakeReady(t)
	left, right := leftPaneOf(o.Windows[0], o.Windows[1])
	dividerX := left.X + left.Width
	rowY := right.Y + right.Height/2

	o.Update(clickMsg(dividerX, rowY))
	if !o.Resizing {
		t.Fatal("the press on the divider started no resize; " +
			"this fixture is not dragging anything and the test below proves nothing")
	}
	at, dir := dividerX, 1
	for range shakeTurns {
		at += dir * 12
		dir = -dir
		o.Update(motionMsg(at, rowY))
	}
	o.Update(releaseMsg(at, rowY))

	if o.SpotlightOn() {
		t.Error("sizing a pane by its divider toggled the beam; " +
			"the gesture fires in the middle of somebody working")
	}
}

// TestAWindowDragDoesNotToggleTheBeam. Moving a pane across the screen is the
// other gesture that reverses direction with the button down: a person carrying
// a pane to a slot passes over it and comes back.
func TestAWindowDragDoesNotToggleTheBeam(t *testing.T) {
	o := shakeReady(t)
	win := o.Windows[1]
	cx, cy := contentCell(win)

	o.Update(clickMsg(cx, cy))
	at, dir := cx, 1
	o.Update(motionMsg(cx+15, cy))
	if !o.Dragging {
		t.Fatal("the press on the pane started no drag; " +
			"this fixture is not dragging anything and the test below proves nothing")
	}
	for range shakeTurns {
		at += dir * 15
		dir = -dir
		o.Update(motionMsg(at, cy))
	}
	o.Update(releaseMsg(at, cy))

	if o.SpotlightOn() {
		t.Error("dragging a pane about toggled the beam")
	}
}

func withSharedBorders(t *testing.T) {
	t.Helper()
	prev := config.Global.SharedBorders
	config.Global.SharedBorders = true
	t.Cleanup(func() { config.Global.SharedBorders = prev })
}
