package terminal

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The anchor rule, pinned per case. The e2e suite drives the same rule through
// a real pane and a real wheel (e2e/tui/scroll_anchor_test.go); these are the
// fast half, and they are where the clamp's own controls live because no frame
// on screen can tell one end of the clamp from the other.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named tests fail:
//
//   - RecordScrollAnchor returning at the top, so nothing is ever anchored:
//     TestAnchorHoldsUnderNewOutput fails saying the scroll was recorded as
//     line 0 with anchored=false, TestAnchorHoldsWhenHistoryIsMergedNotPrinted
//     fails saying merged history moved the view from line 59 to line 359,
//     TestAClearedHistoryPutsThePaneBackOnLiveOutput fails with an offset of 20
//     against an empty history, and
//     TestOutputBetweenTheTwoHalvesIsNotMistakenForAScroll fails saying the
//     view drifted from line 59 to line 109.
//   - ApplyScrollAnchor returning at the top, so nothing derives an offset:
//     the same four fail. The first one's message is the report itself, "the
//     view slid from line 59 to line 99 under 40 lines of new output".
//   - The clamp's lower end (max(..., 0)) removed:
//     TestAClearedHistoryPutsThePaneBackOnLiveOutput fails with an offset of
//     -59, which the render path would read as a row before the start of the
//     ring.
//   - RecordScrollAnchor recording on every call rather than on a changed
//     offset: TestOutputBetweenTheTwoHalvesIsNotMistakenForAScroll fails,
//     because the anchor then follows the end of the history by exactly the
//     amount this whole file exists to cancel.
//   - The clamp's upper end (min(..., sbLen)) removed: NOTHING FAILS, and the
//     control is reported as invalid rather than left looking pinned. An anchor
//     is recorded as scrollbackLen-offset from an offset every caller already
//     clamps to the length, so it is never negative and the upper end is never
//     the binding one. It is kept as defense against a future caller that does
//     not clamp.
//
//   - TestALivePaneIsNotAnchored and TestScrollingBackDownToLiveDropsTheAnchor
//     pass under every control above, which is right: they are about a pane
//     with no anchor to lose, and they are the guard on a fix that held a live
//     pane back from its own output.
//   - TestASaturatedRingKeepsTheOffsetInRange passes under every control above
//     too. It pins a limit rather than the rule, so it is not the row to read
//     for whether the anchor works.

// anchorWindow is a daemon-backed pane with an emulator and no PTY, which is
// all the anchor rule reads.
func anchorWindow(t *testing.T, id string) *Window {
	t.Helper()
	w := NewDaemonWindow(id, "pane", 0, 0, 80, 24, 0, "pty-"+id, nil, config.DefaultScrollbackLines)
	if w == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(w.Close)
	return w
}

// printLines writes n newline-terminated lines straight into the emulator, the
// way a guest printing output does, so they scroll off the top and land in the
// scrollback ring.
func printLines(t *testing.T, w *Window, prefix string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		w.WriteOutput([]byte(fmt.Sprintf("%s-%d\r\n", prefix, i)))
	}
}

// scrollBackTo puts the pane where a wheel gesture would leave it: a scroll
// offset, mirrored to copy mode, and then recorded as an anchor.
func scrollBackTo(w *Window, offset int) {
	w.EnterCopyModeImplicit()
	w.CopyMode.ScrollOffset = offset
	w.ScrollbackOffset = offset
	w.RecordScrollAnchor()
}

// TestAnchorHoldsUnderNewOutput is the report: a pane scrolled back must stay
// on the line the user stopped at while the guest keeps printing.
func TestAnchorHoldsUnderNewOutput(t *testing.T) {
	w := anchorWindow(t, "anchor-output")
	printLines(t, w, "OLD", 100)

	before := w.ScrollbackLen()
	scrollBackTo(w, 20)
	line, ok := w.ScrollAnchorLine()
	if !ok || line != before-20 {
		t.Fatalf("the scroll was recorded as line %d (anchored=%v), want %d", line, ok, before-20)
	}

	printLines(t, w, "NEW", 40)
	w.ApplyScrollAnchor()

	if got := w.ScrollbackLen(); got != before+40 {
		t.Fatalf("the history is %d lines long, want %d; this test is not exercising a "+
			"growing history", got, before+40)
	}
	if got := w.ScrollbackLen() - w.ScrollbackOffset; got != before-20 {
		t.Fatalf("the view slid from line %d to line %d under 40 lines of new output; a "+
			"scrolled pane must stay on the line the user stopped at", before-20, got)
	}
	if w.CopyMode.ScrollOffset != w.ScrollbackOffset {
		t.Fatalf("copy mode is at offset %d and the render path at %d; the two must agree",
			w.CopyMode.ScrollOffset, w.ScrollbackOffset)
	}
}

// TestAnchorHoldsWhenHistoryIsMergedNotPrinted is the control on the shape of
// the fix. A workspace switch primes the pane from the daemon's copy, and those
// rows are pushed straight into the ring rather than scrolled off the screen.
// Counting the lines the emulator scrolls would miss every one of them.
func TestAnchorHoldsWhenHistoryIsMergedNotPrinted(t *testing.T) {
	w := anchorWindow(t, "anchor-merge")
	printLines(t, w, "OLD", 100)

	before := w.ScrollbackLen()
	scrollBackTo(w, 20)

	// The rehydration path's own move: decoded lines appended to the ring, with
	// nothing passing through the emulator's scroll path.
	for i := range 300 {
		w.Terminal.PushScrollbackLine(w.Terminal.ScrollbackLine(i % before))
	}
	w.ApplyScrollAnchor()

	if got := w.ScrollbackLen() - w.ScrollbackOffset; got != before-20 {
		t.Fatalf("merged history moved the view from line %d to line %d; the anchor must be "+
			"a place in the history, not a count of lines the emulator scrolled",
			before-20, got)
	}
}

// TestASaturatedRingKeepsTheOffsetInRange is the honest limit of the anchor.
//
// A ring at its maximum evicts a line for every line it takes, so the history
// stops getting longer and the anchored line is destroyed without anything
// observable saying so. The pane cannot be held on content that no longer
// exists. What is still promised is that the offset stays a row the ring
// actually holds, and that the pane is not thrown back to live output.
//
// See the note on ApplyScrollAnchor. The drift that resumes past this point is
// filed rather than fixed, because neither emulator backend reports the count
// of lines pushed.
func TestASaturatedRingKeepsTheOffsetInRange(t *testing.T) {
	w := anchorWindow(t, "anchor-saturated")
	w.SetScrollbackMaxLines(40)
	printLines(t, w, "OLD", 60)

	scrollBackTo(w, 20)
	printLines(t, w, "NEW", 200)
	w.ApplyScrollAnchor()

	sbLen := w.ScrollbackLen()
	if w.ScrollbackOffset < 0 || w.ScrollbackOffset > sbLen {
		t.Fatalf("the offset is %d against a history %d lines long; the render path would "+
			"read a row the ring does not hold", w.ScrollbackOffset, sbLen)
	}
	if w.ScrollbackOffset == 0 {
		t.Fatal("a saturated ring threw the pane back to live output; the user is still " +
			"reading history, however old")
	}
}

// TestAClearedHistoryPutsThePaneBackOnLiveOutput is the lower end of the clamp.
// ED 3 and the alternate screen throw the history away, so there is no line left
// to hold and the offset must not go negative.
func TestAClearedHistoryPutsThePaneBackOnLiveOutput(t *testing.T) {
	w := anchorWindow(t, "anchor-cleared")
	printLines(t, w, "OLD", 100)
	scrollBackTo(w, 20)

	w.ClearScrollback()
	w.ApplyScrollAnchor()

	if w.ScrollbackOffset != 0 {
		t.Fatalf("the offset is %d against an empty history, want 0", w.ScrollbackOffset)
	}
	if _, ok := w.ScrollAnchorLine(); ok {
		t.Fatal("the pane is still anchored with nothing to anchor to")
	}
	if w.InCopyMode() {
		t.Fatal("an implicit copy mode session outlived the scrollback it existed to render")
	}
}

// TestOutputBetweenTheTwoHalvesIsNotMistakenForAScroll is why the record half
// acts on a changed offset rather than on every call. Output arrives on a
// background goroutine, so it can land between the derive and the record, and a
// record that took the offset at face value would move the anchor by exactly
// the drift being cancelled.
func TestOutputBetweenTheTwoHalvesIsNotMistakenForAScroll(t *testing.T) {
	w := anchorWindow(t, "anchor-between")
	printLines(t, w, "OLD", 100)

	before := w.ScrollbackLen()
	scrollBackTo(w, 20)

	for range 5 {
		w.ApplyScrollAnchor()
		printLines(t, w, "MID", 10)
		w.RecordScrollAnchor()
	}
	w.ApplyScrollAnchor()

	if got := w.ScrollbackLen() - w.ScrollbackOffset; got != before-20 {
		t.Fatalf("the view drifted from line %d to line %d over five rounds of output "+
			"arriving between the derive and the record", before-20, got)
	}
}

// TestALivePaneIsNotAnchored is the case the idle path depends on: a pane the
// user has not scrolled has no anchor, so neither half does any work and
// nothing holds a live pane back from its own output.
func TestALivePaneIsNotAnchored(t *testing.T) {
	w := anchorWindow(t, "anchor-live")
	printLines(t, w, "OLD", 100)

	w.ApplyScrollAnchor()
	w.RecordScrollAnchor()
	if _, ok := w.ScrollAnchorLine(); ok {
		t.Fatal("a pane nobody scrolled is anchored")
	}

	printLines(t, w, "NEW", 40)
	w.ApplyScrollAnchor()
	if w.ScrollbackOffset != 0 {
		t.Fatalf("a live pane was scrolled back to offset %d by its own output",
			w.ScrollbackOffset)
	}
}

// TestScrollingBackDownToLiveDropsTheAnchor closes the round trip: the user
// scrolling to the bottom is a decision to follow the output again, and the
// anchor must not put them back where they were.
func TestScrollingBackDownToLiveDropsTheAnchor(t *testing.T) {
	w := anchorWindow(t, "anchor-round-trip")
	printLines(t, w, "OLD", 100)
	scrollBackTo(w, 20)

	scrollBackTo(w, 0)
	if _, ok := w.ScrollAnchorLine(); ok {
		t.Fatal("scrolling back down to live output left the pane anchored")
	}

	printLines(t, w, "NEW", 40)
	w.ApplyScrollAnchor()
	if w.ScrollbackOffset != 0 {
		t.Fatalf("the pane was dragged back to offset %d after returning to live output",
			w.ScrollbackOffset)
	}
}
