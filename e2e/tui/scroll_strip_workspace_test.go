package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The report this file exists for: scroll the strip in the scrolling (niri)
// layout, switch workspace, come back, and the scroll is gone.
//
// One real tuios on one real daemon, three named panes, the real leader chord
// for the workspace switch and real SGR wheel reports for the scroll. The
// assertion is the frame: where the columns are drawn is where the strip is.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named row fail:
//
//   - ScrollingOnFocusChange calling ScrollToFocusedColumn again, which is the
//     shipped code before the fix: TestWorkspaceRoundTripKeepsTheScrolledStrip
//     fails with ALPHA drawn back at the left edge after the round trip.
//   - EnsureFocusedVisible never revealing anything:
//     TestWorkspaceRoundTripRevealsAHiddenColumn fails with the focused column
//     still off screen. That is the positive control on the row above: a fix
//     that only stopped the strip moving would leave the user typing into a
//     pane they cannot see, and this suite would say so.
//
// The strip's own rule and its limit are pinned faster in
// internal/app.TestWorkspaceRoundTripKeepsTheParkedStrip, whose controls
// include the one that matters most here: giving EnsureFocusedVisible the peek
// margin, so it moves an already-visible column. That fails the "kept" row too,
// which is how these tests are known to be bound to the threshold rather than
// to the name of the function called.

// stripClient brings up one client on one daemon session with three named
// panes in the scrolling layout, focused on the leftmost column.
func stripClient(t *testing.T, base string) *tuitest.Terminal {
	t.Helper()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "wsstrip", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	a := attachIn(t, base, "wsstrip", startOpts{cols: 100, rows: 30})
	renameWindow(t, a, "ALPHA")
	newWindow(t, a)
	renameWindow(t, a, "BRAVO")
	newWindow(t, a)
	renameWindow(t, a, "CHARLIE")
	waitWindowCount(t, a, 3, "three panes")
	enableTiling(t, a)

	if err := a.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	waitPaletteOpen(t, a, "to pick the scrolling layout")
	if err := a.SendKeys("scrolling", tuitest.Enter); err != nil {
		t.Fatalf("pick the scrolling layout: %v", err)
	}
	waitPaletteClosed(t, a, "after picking the scrolling layout")

	// Columns are 55% of the screen each, so at most two are ever on screen.
	// Step left to the first column, which is where every row below starts.
	for range 2 {
		if err := a.SendKeys(tuitest.Alt(tuitest.Left)); err != nil {
			t.Fatalf("step left: %v", err)
		}
		time.Sleep(insertGuard)
	}
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ALPHA") && !strings.Contains(s.Text(), "CHARLIE")
	}, uiTimeout); err != nil {
		t.Fatalf("the strip never settled on the first column: %v\n%s", err, a.Snapshot())
	}
	if err := a.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled: %v", err)
	}
	return a
}

// stripBand is the screen without the dock's last two rows, so a notification
// expiring does not read as the strip having moved.
func stripBand(term *tuitest.Terminal) string {
	s := term.Screen()
	_, rows := s.Size()
	var b strings.Builder
	for r := range rows - 2 {
		b.WriteString(s.Line(r))
		b.WriteString("\n")
	}
	return b.String()
}

// wheelStrip turns the wheel with alt held, which is how the strip is scrolled.
func wheelStrip(t *testing.T, term *tuitest.Terminal, notches int) {
	t.Helper()
	for range notches {
		mousePress(t, term, 50, 15, tuitest.MouseWheelDown, tuitest.ModAlt)
	}
	time.Sleep(time.Second)
}

// roundTrip switches to workspace 2 and back to workspace 1.
func roundTrip(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	switchWorkspace(t, term, "2", 0)
	time.Sleep(500 * time.Millisecond)
	switchWorkspace(t, term, "1", 3)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the frame never settled after the round trip: %v", err)
	}
	time.Sleep(time.Second)
}

// TestWorkspaceRoundTripKeepsTheScrolledStrip is the report. One wheel notch
// moves the strip a fifth of the screen and leaves the focused column on
// screen, so nothing about the round trip has any reason to move it.
func TestWorkspaceRoundTripKeepsTheScrolledStrip(t *testing.T) {
	a := stripClient(t, t.TempDir())

	wheelStrip(t, a, 1)
	before := stripBand(a)
	t.Logf("the strip after one notch:\n%s", before)
	if !strings.Contains(before, "ALPHA") {
		t.Fatalf("one notch took the focused column off screen, so this row proves "+
			"nothing about a column the user can see:\n%s", before)
	}

	roundTrip(t, a)

	if after := stripBand(a); after != before {
		t.Errorf("a workspace round trip moved the strip. The focused column was on "+
			"screen the whole time, so nothing asked for it.\nBEFORE:\n%s\nAFTER:\n%s",
			before, after)
	}
}

// TestWorkspaceRoundTripRevealsAHiddenColumn is the limit on the row above, and
// the positive control for it: with none of the focused column on screen, the
// round trip must bring it back. A fix that simply stopped the strip moving
// would leave the user typing into a pane they cannot see.
func TestWorkspaceRoundTripRevealsAHiddenColumn(t *testing.T) {
	a := stripClient(t, t.TempDir())

	// Five notches is the whole strip: the focused column is off the left end.
	wheelStrip(t, a, 5)
	before := stripBand(a)
	t.Logf("the strip parked off the focused column:\n%s", before)
	if strings.Contains(before, "ALPHA") {
		t.Fatalf("the focused column is still on screen, so this proves nothing:\n%s", before)
	}

	roundTrip(t, a)

	after := stripBand(a)
	if !strings.Contains(after, "ALPHA") {
		t.Errorf("the round trip left the focused column off screen\nAFTER:\n%s", after)
	}
}
