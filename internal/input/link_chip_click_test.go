package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// chipCell is a cell on a pane's chip row, which the chip owns across its
// whole width. The row is the pane's bottom content row: one border offset in
// from the bottom edge, or the last row itself for a tiled pane.
func chipCell(w *terminal.Window) (int, int) {
	return w.X + 2, w.Y + w.BorderOffset() + w.ContentHeight() - 1
}

// TestLinkChipClickJumpsToTheTargetAndClears: a plain click on the chip is
// consumed by the jump — the focus lands on the chip's target pane and the
// chip is taken down, so a second click on the same spot is a click on the
// pane again, not another jump.
func TestLinkChipClickJumpsToTheTargetAndClears(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	left, right := leftPaneOf(wa, wb)
	o.SetLinkChipForTest(left, &session.LinkChip{Label: "build logs", Target: right.ID})
	cx, cy := chipCell(left)

	clickPane(o, cx, cy)

	if got, want := o.FocusedWindow, indexOf(o, right); got != want {
		t.Fatalf("focus after the chip click is window %d, want the chip's target pane %d", got, want)
	}
	if o.LinkChipForTest(left.ID) != nil {
		t.Fatalf("the chip survived its own click")
	}
}

// TestClickOffTheChipLeavesItAlone: the chip answers for its own row only, so
// a plain click on pane content next to it is an ordinary click — the pane
// takes it and the chip stays up.
func TestClickOffTheChipLeavesItAlone(t *testing.T) {
	o, wa, wb := twoPaneBSP(t)
	left, right := leftPaneOf(wa, wb)
	chip := &session.LinkChip{Label: "build logs", Target: right.ID}
	o.SetLinkChipForTest(left, chip)
	cx, cy := contentCell(right)

	clickPane(o, cx, cy)

	if o.LinkChipForTest(left.ID) != chip {
		t.Fatalf("a click away from the chip took it down")
	}
	if got, want := o.FocusedWindow, indexOf(o, right); got != want {
		t.Fatalf("focus after the plain click is window %d, want the pane that was clicked", got)
	}
}
