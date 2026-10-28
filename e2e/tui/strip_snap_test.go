package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// onBravo reports whether the strip is lined up on BRAVO: BRAVO starts at the
// screen's left edge, where its top left corner is drawn, and none of ALPHA
// is on screen.
func onBravo(band string) bool {
	return strings.Contains(band, "BRAVO") && !strings.Contains(band, "ALPHA") && strings.HasPrefix(band, "╭")
}

// waitBand waits for the strip's band to satisfy ok.
func waitBand(t *testing.T, term *tuitest.Terminal, ok func(string) bool, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var band string
	for time.Now().Before(deadline) {
		if band = stripBand(term); ok(band) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s:\n%s", what, band)
}

// TestStripSnapsToTheNextColumn is appearance.niri_scroll_snap with the
// wheel: once the steps stop, the strip lines up on the next column in the
// way they went. niri_scroll_cells is 3 here, so a step never lands on a
// column by itself. The columns are 55 cells of a 100-cell screen.
//
//  1. One step to the right, 3 cells, goes on to BRAVO. Snapping to the
//     nearest column would send it back to ALPHA, and the wheel could never
//     move the strip one notch at a time.
//  2. One step to the left goes back to ALPHA, to the frame the test started
//     on.
//
// Negative controls: with SnapStrip doing nothing, or snapping to the nearest
// column, step 1 does not reach BRAVO. The runs are in NEGATIVE_CONTROLS.md.
func TestStripSnapsToTheNextColumn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nniri_scroll_cells = 3\nniri_scroll_snap = true\n")
	a := stripClient(t, base)
	before := stripBand(a)

	wheelStrip(t, a, 1)
	waitBand(t, a, onBravo, "one step to the right did not line the strip up on BRAVO, at the left edge")

	mousePress(t, a, 50, 15, tuitest.MouseWheelUp, tuitest.ModAlt)
	waitBand(t, a, func(band string) bool { return band == before }, "one step to the left did not bring the strip back to ALPHA")
}

// TestStripSnapReachesTheOtherClient: the snap is the strip's offset, which
// is the session's, so it is sent to the daemon when it happens and a second
// client on the session lines up with the first. B is attached at A's size,
// so the two draw the strip alike. A slides there and B jumps, so B is
// normally first, about 150 ms ahead of A. B may be up to peerSnapLag behind.
//
// Negative control: with the snap not sent to the daemon, it reaches B only
// with a later push of A's, 510 to 530 ms after A lined up in three runs. The
// run is in NEGATIVE_CONTROLS.md.
func TestStripSnapReachesTheOtherClient(t *testing.T) {
	const peerSnapLag = 150 * time.Millisecond
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nniri_scroll_cells = 3\nniri_scroll_snap = true\n")
	a := stripClient(t, base)
	b := attachIn(t, base, "wsstrip", startOpts{cols: 100, rows: 30})
	waitBand(t, b, func(band string) bool { return band == stripBand(a) }, "the second client does not draw the strip as the first does")

	start := time.Now()
	mousePress(t, a, 50, 15, tuitest.MouseWheelDown, tuitest.ModAlt)
	var onA, onB time.Duration
	for time.Since(start) < uiTimeout && (onA == 0 || onB == 0) {
		if onA == 0 && onBravo(stripBand(a)) {
			onA = time.Since(start)
		}
		if onB == 0 && onBravo(stripBand(b)) {
			onB = time.Since(start)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if onA == 0 || onB == 0 {
		t.Fatalf("ASSERTION: one step did not line both strips up on BRAVO (A after %v, B after %v).\nA:\n%s\nB:\n%s", onA, onB, stripBand(a), stripBand(b))
	}
	if onB > onA+peerSnapLag {
		t.Fatalf("ASSERTION: A's snap reached B %v after A lined up, more than %v: it was not sent when it happened", onB-onA, peerSnapLag)
	}
}

// touch is the report tuios-web's touch layer writes for a finger's sideways
// drag that started on cell (col, row): kind 2 brings the columns to the
// right into view by cells, 1 those to the left, and 3 is the lift. See
// cmd/tuios-web/strip_touch.js and internal/app/strip_touch.go.
func touch(kind, col, row, cells int) string {
	if kind == 3 {
		return fmt.Sprintf("\x1b[7503;3;%d;%d~", col, row)
	}
	return fmt.Sprintf("\x1b[7503;%d;%d;%d;%d~", kind, col, row, cells)
}

// TestStripTouchSnapsWhenTheFingerLifts: a finger's drag on the strip snaps
// when the finger lifts, not when it rests on the way, and to the nearest
// column, since the strip has followed the finger.
//
//  1. Ten reports of 3 cells (30 cells) and a rest longer than the wheel's
//     pause leave the strip where the finger put it. The lift lines it up on
//     BRAVO, past half of ALPHA.
//  2. Five cells on and the lift: the strip goes back to BRAVO, the nearest
//     column, and not on to CHARLIE as the wheel would.
//
// Negative control: with a finger's step arming the wheel's pause snap
// (CancelStripSnap cut), the strip snaps during the rest. The run is in
// NEGATIVE_CONTROLS.md.
func TestStripTouchSnapsWhenTheFingerLifts(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nniri_scroll_cells = 3\nniri_scroll_snap = true\n")
	a := stripClient(t, base)
	before := stripBand(a)
	for range 10 {
		if err := a.SendKeys(touch(2, 50, 15, 3)); err != nil {
			t.Fatalf("finger step: %v", err)
		}
	}
	waitBand(t, a, func(band string) bool { return band != before }, "ten finger steps did not move the strip")
	time.Sleep(600 * time.Millisecond)
	if band := stripBand(a); strings.HasPrefix(band, "╭") {
		t.Fatalf("ASSERTION: the strip snapped while the finger rested:\n%s", band)
	}
	if err := a.SendKeys(touch(3, 50, 15, 0)); err != nil {
		t.Fatalf("finger lift: %v", err)
	}
	waitBand(t, a, onBravo, "the lift did not line the strip up on BRAVO")
	settled := stripBand(a)

	if err := a.SendKeys(touch(2, 50, 15, 5)); err != nil {
		t.Fatalf("finger step: %v", err)
	}
	waitBand(t, a, func(band string) bool { return band != settled }, "five cells of finger did not move the strip")
	if err := a.SendKeys(touch(3, 50, 15, 0)); err != nil {
		t.Fatalf("finger lift: %v", err)
	}
	waitBand(t, a, func(band string) bool { return band == settled }, "a short drag did not go back to BRAVO, the nearest column")
}

// TestStripTouchReachesOnlyTheStrip: a finger's drag that started on the
// rail moves nothing, as a sideways wheel over the rail does not. The same
// drag started on the strip moves it, which is the positive half.
//
// Negative control: with handleStripTouch not checking the rail, the drag on
// the rail moves the strip. The run is in NEGATIVE_CONTROLS.md.
func TestStripTouchReachesOnlyTheStrip(t *testing.T) {
	base := t.TempDir()
	a := stripClient(t, base)
	toggleRail(t, a)
	waitRailUp(t, a)
	if err := a.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled with the rail up: %v", err)
	}
	before := stripBand(a)

	if err := a.SendKeys(touch(2, 3, 10, 30), touch(3, 3, 10, 0)); err != nil {
		t.Fatalf("finger on the rail: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if band := stripBand(a); band != before {
		t.Fatalf("ASSERTION: a finger's drag on the rail moved the strip.\nBEFORE:\n%s\nAFTER:\n%s", before, band)
	}

	if err := a.SendKeys(touch(2, 70, 10, 30), touch(3, 70, 10, 0)); err != nil {
		t.Fatalf("finger on the strip: %v", err)
	}
	waitBand(t, a, func(band string) bool { return band != before }, "a finger's drag on the strip did not move it, so the rail half proved nothing")
}

// TestStripTouchLeavesTheWheelToThePane: off the strip a finger's report
// reaches no pane, also one that takes the mouse, while a real sideways wheel
// with Ctrl and Alt held, which a trackpad's drift sends with those keys down
// and Windows sends for AltGr, still reaches it. The page's report is a
// sequence of its own, so no input a person makes is taken for it.
//
// Negative control: the PR before this fix, which wrote the finger as Ctrl
// and Alt with a sideways wheel and dropped every such wheel off the strip.
// The run is in NEGATIVE_CONTROLS.md.
func TestStripTouchLeavesTheWheelToThePane(t *testing.T) {
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
	if err := term.SendKeys(touch(2, 50, 15, 3), touch(3, 50, 15, 0)); err != nil {
		t.Fatalf("finger step: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if snap := term.Snapshot(); strings.Contains(snap, "^[[") || strings.Contains(snap, "7503") {
		t.Fatalf("ASSERTION: a finger's report reached the pane off the strip\n%s", snap)
	}
	// Ctrl and Alt with the wheel to the right: 67 + 8 + 16.
	if err := term.SendKeys("\x1b[<91;50;15M"); err != nil {
		t.Fatalf("ctrl+alt wheel: %v", err)
	}
	if err := term.WaitForText("^[[<", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a sideways wheel with Ctrl and Alt did not reach the pane: %v\n%s", err, term.Snapshot())
	}
}
