package tuie2e

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestARailMoveSurvivesAQuitRightAfterIt: the rail draws a move at once and
// sends it to the daemon after (internal/app/session_order.go). A client that
// quit while the move was still on its way lost it, and the next client drew
// the old order. The client now sends what is still out before it exits.
//
// The knob session_order_hold_ms holds each order on its way out for three
// seconds, so the client always quits while the move is out. The send on the
// way out does not wait for the knob.
//
// How this could pass wrongly, written down first:
//   - The drag could fail to move the row, so the order the daemon lists is
//     the order it always had. The test waits for the rail to draw bravo
//     above alpha before it quits, and the daemon listed alpha first.
//   - The held send could land after all. The client is gone before the
//     hold ends, and the test reads the daemon's order before the hold
//     would end.
//
// Negative control: without the send on the way out, the daemon still lists
// alpha above bravo.
func TestARailMoveSurvivesAQuitRightAfterIt(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	away := t.TempDir()
	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLIIn(t, base, away, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	if got := sessionNames(t, base); !slices.Equal(got, []string{"alpha", "bravo"}) {
		t.Fatalf("the daemon lists %v before the move, want [alpha bravo]", got)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "alpha"}, env: []string{knob("session_order_hold_ms=3000")}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "alpha", "bravo")

	s := term.Screen()
	col, _ := findOnScreen(t, term, "bravo")
	mouseDrag(t, term, col, screenRowOf(s, "bravo"), col, screenRowOf(s, "alpha"), tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, b := screenRowOf(s, "alpha"), screenRowOf(s, "bravo")
		return a >= 0 && b >= 0 && b < a
	}, uiTimeout); err != nil {
		t.Fatalf("the drag did not draw bravo above alpha: %v\n%s", err, term.Snapshot())
	}
	if err := term.Close(); err != nil {
		t.Logf("close: %v", err)
	}

	got := sessionNames(t, base)
	if !slices.Equal(got, []string{"bravo", "alpha"}) {
		t.Fatalf("ASSERTION: the daemon lists %v after the client quit, want [bravo alpha]: the move was lost", got)
	}
	t.Logf("the daemon lists %v", got)
}
