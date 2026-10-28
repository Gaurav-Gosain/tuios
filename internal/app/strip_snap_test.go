package app

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// TestStripSnapStaysOnItsWorkspace: a wheel's pause snap is for the strip
// the wheel moved. A switch to another workspace inside the pause leaves the
// timer running, and the strip on screen when it fires is not the one it was
// armed for, so it must not move. The same timer on the workspace it was
// armed on snaps, which is the positive half.
//
// Negative control: with SnapStrip not checking the workspace, the first
// half moves the strip. The run is in e2e/tui/NEGATIVE_CONTROLS.md.
func TestStripSnapStaysOnItsWorkspace(t *testing.T) {
	f := newScrollingFleet(t, 3, 100, 30)
	m := f.r.m
	m.Settings.NiriScrollSnap = true
	m.Settings.NiriScrollCells = 3
	sl := m.GetOrCreateScrollingLayout()

	before := sl.ViewportX
	m.ScrollingScrollViewport(-1)
	m.ScheduleStripSnap(-1)
	at := sl.ViewportX
	if at != before-3 {
		t.Fatalf("setup: one step to the left took the strip from %d to %d\n%s", before, at, strip(m, "A"))
	}
	other := StripSnapMsg{Gen: m.stripSnapGen, Workspace: m.CurrentWorkspace + 1}
	if m.SnapStrip(other) || sl.ViewportX != at {
		t.Fatalf("ASSERTION: a snap armed on workspace %d moved the strip of workspace %d from %d to %d",
			other.Workspace, m.CurrentWorkspace, at, sl.ViewportX)
	}
	own := StripSnapMsg{Gen: m.stripSnapGen, Workspace: m.CurrentWorkspace}
	if !m.SnapStrip(own) || sl.ViewportX == at {
		t.Fatalf("the snap armed on this workspace did not move the strip: still at %d\n%s", sl.ViewportX, strip(m, "A"))
	}
}

// TestStripTouchReport is the decoding of tuios-web's finger report, and the
// sequences that only look like it.
func TestStripTouchReport(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want StripTouchMsg
		ok   bool
	}{
		{"\x1b[7503;2;50;15;3~", StripTouchMsg{Kind: StripTouchRight, X: 49, Y: 14, Cells: 3}, true},
		{"\x1b[7503;1;1;1~", StripTouchMsg{Kind: StripTouchLeft, X: 0, Y: 0, Cells: 1}, true},
		{"\x1b[7503;3;10;4~", StripTouchMsg{Kind: StripTouchLift, X: 9, Y: 3, Cells: 1}, true},
		{"\x1b[7503;2;5;5;99999~", StripTouchMsg{Kind: StripTouchRight, X: 4, Y: 4, Cells: stripTouchMaxCells}, true},
		{"\x1b[7503;4;5;5~", StripTouchMsg{}, false},
		{"\x1b[7503;2;5~", StripTouchMsg{}, false},
		{"\x1b[7503;2;5;5;1;1~", StripTouchMsg{}, false},
		{"\x1b[7503;2;-5;5~", StripTouchMsg{}, false},
		{"\x1b[7504;2;5;5~", StripTouchMsg{}, false},
	} {
		got := stripTouchReport(uv.UnknownCsiEvent(tc.in))
		msg, ok := got.(StripTouchMsg)
		if ok != tc.ok || (ok && msg != tc.want) {
			t.Errorf("%q: got %#v, want %#v (a report: %v)", tc.in, got, tc.want, tc.ok)
		}
	}
}
