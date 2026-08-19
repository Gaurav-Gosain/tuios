package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// scrolledBackPane returns a tiled pane looking some way up its own history,
// with a frame already composed: the grab rect input reads is recorded by the
// renderer as it draws, so nothing is grabbable until something is drawn.
func scrolledBackPane(t *testing.T) (*OS2, *terminal.Window, app.ScrollbarRect) {
	t.Helper()
	o, wa, wb := twoPaneBSP(t)
	win, _ := leftPaneOf(wa, wb)

	win.LockIO()
	for i := range 400 {
		_, _ = win.Terminal.Write(fmt.Appendf(nil, "history line %d\r\n", i))
	}
	win.UnlockIO()
	win.MarkContentDirty()
	if win.ScrollbackLenSync() <= 0 {
		t.Fatal("the pane has no scrollback to scroll back into")
	}

	win.EnterCopyModeImplicit()
	if win.CopyMode == nil {
		t.Fatal("copy mode did not start")
	}
	win.CopyMode.ScrollOffset = win.ScrollbackLenSync() / 2
	win.ScrollbackOffset = win.CopyMode.ScrollOffset

	o.GetCanvas(true)
	rect, ok := o.ScrollbarHit(win)
	if !ok {
		t.Fatal("the composed frame recorded no scrollbar to grab")
	}
	return o, win, rect
}

// A press on the bare track jumps the thumb under the pointer and becomes a
// drag in the same gesture. The grab offset has to be taken after the jump:
// taken before it, the first motion event moves the thumb a second time, which
// reads as the bar leaping out from under the pointer.
func TestScrollbarTrackClickJumpsThenDragsWithoutASecondLeap(t *testing.T) {
	o, win, rect := scrolledBackPane(t)

	// A row on the track well clear of the thumb, on whichever side has room.
	target := rect.TrackY + 1
	if rect.OnThumb(target) {
		target = rect.TrackY + rect.TrackH - 2
	}
	if rect.OnThumb(target) {
		t.Fatal("the thumb covers the whole track; there is no track left to click")
	}

	handleMouseClick(clickMsg(rect.X, target), o)
	if !o.ScrollbarDragging {
		t.Fatal("a press on the track did not start a drag")
	}
	jumped := app.ScrollbarThumbRow(win)
	if jumped < target-rect.ThumbH || jumped > target {
		t.Errorf("the click on row %d put the thumb at row %d; it did not jump under the pointer", target, jumped)
	}
	if want := target - jumped; o.ScrollbarGrabOffset != want {
		t.Errorf("grab offset %d, want %d: it was taken before the jump, not after it",
			o.ScrollbarGrabOffset, want)
	}

	// The first motion event of the gesture, on the row the press landed on.
	// Nothing moved between them, so nothing may move now.
	handleMouseMotion(motionMsg(rect.X, target), o)
	if got := app.ScrollbarThumbRow(win); got != jumped {
		t.Errorf("the first motion event moved the thumb from row %d to %d: the bar leapt a second time",
			jumped, got)
	}

	// And the drag proper tracks the pointer, holding the offset it grabbed.
	handleMouseMotion(motionMsg(rect.X, target+2), o)
	if got := app.ScrollbarThumbRow(win); got != jumped+2 {
		t.Errorf("dragging two rows down moved the thumb to row %d, want %d", got, jumped+2)
	}

	handleMouseRelease(releaseMsg(rect.X, target+2), o)
	if o.ScrollbarDragging || o.ScrollbarGrabOffset != 0 {
		t.Errorf("the release left the drag armed (dragging=%v offset=%d)",
			o.ScrollbarDragging, o.ScrollbarGrabOffset)
	}
}

// A press on the thumb itself is a plain grab: it holds where it was taken, so
// the thumb does not jump to centre itself on the pointer first.
func TestScrollbarThumbGrabDoesNotJump(t *testing.T) {
	o, win, rect := scrolledBackPane(t)
	before := app.ScrollbarThumbRow(win)
	row := rect.ThumbY + rect.ThumbH - 1

	handleMouseClick(clickMsg(rect.X, row), o)
	if got := app.ScrollbarThumbRow(win); got != before {
		t.Errorf("grabbing the thumb at row %d moved it from %d to %d", row, before, got)
	}
	if want := row - before; o.ScrollbarGrabOffset != want {
		t.Errorf("grab offset %d, want %d", o.ScrollbarGrabOffset, want)
	}
	handleMouseMotion(motionMsg(rect.X, row), o)
	if got := app.ScrollbarThumbRow(win); got != before {
		t.Errorf("the first motion event moved a stationary grab from row %d to %d", before, got)
	}
}

// The bar owns only the cells it painted. A press one column in from it, or on
// the same column of a pane at the live tail, is the guest's.
func TestScrollbarPressOutsideTheDrawnBarReachesTheGuest(t *testing.T) {
	o, win, rect := scrolledBackPane(t)

	handleMouseClick(clickMsg(rect.X-1, rect.ThumbY), o)
	if o.ScrollbarDragging {
		t.Error("a press one column in from the bar started a scrollbar drag")
	}

	win.CopyMode.ScrollOffset = 0
	win.ScrollbackOffset = 0
	o.GetCanvas(true)
	if _, ok := o.ScrollbarHit(win); ok {
		t.Error("the frame that returned the pane to the live tail left a grab behind")
	}
	handleMouseClick(clickMsg(rect.X, rect.ThumbY), o)
	if o.ScrollbarDragging {
		t.Error("a press on the last content column of a pane at the live tail started a drag")
	}
}
