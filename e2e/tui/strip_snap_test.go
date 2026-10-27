package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestStripSnapsToTheNearestColumn is appearance.niri_scroll_snap: once the
// wheel stops, the strip lines up on the nearest column. niri_scroll_cells is
// 3 here, so a step never lands on a column by itself. The columns are 55
// cells of a 100-cell screen.
//
//  1. One step, 3 cells, is nearer the first column than the second: the strip
//     goes back to where it was, and the frame is the frame before the step.
//  2. Ten steps, 30 cells, is past half of the first column: the strip lines
//     up on the second. BRAVO starts at the screen's left edge, where its top
//     left corner is drawn, and none of ALPHA is on screen.
//
// Negative control: with SnapStrip doing nothing, step 1 leaves the strip 3
// cells along and its frame differs. The run is in NEGATIVE_CONTROLS.md.
func TestStripSnapsToTheNearestColumn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nniri_scroll_cells = 3\nniri_scroll_snap = true\n")
	a := stripClient(t, base)
	before := stripBand(a)

	// 1. Back to the first column.
	wheelStrip(t, a, 1)
	if after := stripBand(a); after != before {
		t.Fatalf("ASSERTION: one step did not snap back to the first column.\nBEFORE:\n%s\nAFTER:\n%s", before, after)
	}

	// 2. On to the second.
	wheelStrip(t, a, 10)
	deadline := time.Now().Add(uiTimeout)
	var band string
	for time.Now().Before(deadline) {
		band = stripBand(a)
		if strings.Contains(band, "BRAVO") && !strings.Contains(band, "ALPHA") && strings.HasPrefix(band, "╭") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: ten steps did not line the strip up on BRAVO, at the left edge:\n%s", band)
}

// touchStep and touchLift are the reports tuios-web's touch layer writes for
// a finger's sideways drag: Ctrl and Alt with the wheel to the right (67 + 8 +
// 16), and the lift, Ctrl, Alt and Shift with the wheel to the left (66 + 4 +
// 8 + 16). See cmd/tuios-web/strip_touch.js.
const (
	touchStep = "\x1b[<91;50;15M"
	touchLift = "\x1b[<94;50;15M"
)

// TestStripTouchSnapsWhenTheFingerLifts: a finger's drag on the strip snaps
// when the finger lifts, not when it rests on the way. Ten steps (30 cells)
// and a rest longer than the wheel's pause leave the strip where the finger
// put it; the lift lines it up on BRAVO.
//
// Negative control: with a finger's step arming the wheel's pause snap
// (CancelStripSnap cut), the strip snaps during the rest. The run is in
// NEGATIVE_CONTROLS.md.
func TestStripTouchSnapsWhenTheFingerLifts(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nniri_scroll_cells = 3\nniri_scroll_snap = true\n")
	a := stripClient(t, base)
	snapped := func(band string) bool {
		return strings.Contains(band, "BRAVO") && !strings.Contains(band, "ALPHA") && strings.HasPrefix(band, "╭")
	}
	for range 10 {
		if err := a.SendKeys(touchStep); err != nil {
			t.Fatalf("finger step: %v", err)
		}
	}
	time.Sleep(600 * time.Millisecond)
	if band := stripBand(a); snapped(band) || strings.HasPrefix(band, "╭") {
		t.Fatalf("ASSERTION: the strip snapped while the finger rested:\n%s", band)
	}
	if err := a.SendKeys(touchLift); err != nil {
		t.Fatalf("finger lift: %v", err)
	}
	deadline := time.Now().Add(uiTimeout)
	var band string
	for time.Now().Before(deadline) {
		if band = stripBand(a); snapped(band) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the lift did not line the strip up on BRAVO:\n%s", band)
}

// TestStripTouchStepReachesNoPaneOffTheStrip: outside the scrolling layout a
// finger's step reaches nothing, also a pane that takes the mouse. The same
// pane gets a plain Alt sideways wheel, which is the positive half.
//
// Negative control: with the touch branch not checking for the scrolling
// layout, the step falls through to the pane and cat -v prints it. The run is
// in NEGATIVE_CONTROLS.md.
func TestStripTouchStepReachesNoPaneOffTheStrip(t *testing.T) {
	const sess = "touchbsp"
	term, base := lockedSession(t, sess, "")
	waitForSettledGeometryIn(t, base, sess, 1)
	if err := term.SendKeys(`printf '\033[?1000h\033[?1006h'; echo mouse-on; cat -v`, tuitest.Enter); err != nil {
		t.Fatalf("start cat -v with the mouse on: %v", err)
	}
	if err := term.WaitForText("mouse-on", uiTimeout); err != nil {
		t.Fatalf("setup: the pane never turned the mouse on: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(touchStep, touchLift); err != nil {
		t.Fatalf("finger step: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if strings.Contains(term.Snapshot(), "^[[<") {
		t.Fatalf("ASSERTION: a finger's step reached the pane off the strip\n%s", term.Snapshot())
	}
	if err := term.SendKeys("\x1b[<75;50;15M"); err != nil {
		t.Fatalf("alt wheel: %v", err)
	}
	if err := term.WaitForText("^[[<", uiTimeout); err != nil {
		t.Fatalf("setup: a plain Alt wheel did not reach the pane, so the step proved nothing: %v\n%s", err, term.Snapshot())
	}
}
