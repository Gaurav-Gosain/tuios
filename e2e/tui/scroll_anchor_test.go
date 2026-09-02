package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The report this file exists for: "the scrolling still breaks randomly".
//
// A pane scrolled back is a place in the history, not a distance from the
// bottom. The bottom moves whenever the pane's history gets longer, and it gets
// longer on two paths a user never asked about: the guest printing a line, and
// a workspace switch priming the pane from the daemon's copy. So the trigger is
// output, and output arrives when it arrives, which is what "randomly" means
// from the chair.
//
// Both rows drive one real tuios on one real daemon, real SGR wheel reports for
// the scroll and the real leader chord for the workspace switch. The assertion
// is the frame: the newest line on screen is where the viewport is.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching both
// rows fail:
//
//   - Window.RecordScrollAnchor returning at the top, so no scroll is ever
//     recorded as a place in the history: the first row fails saying the
//     newest line on screen went from ANCHOR-330-END to nothing at all while
//     the guest was printing, and the second says the same of the round trip.
//   - Window.ApplyScrollAnchor returning at the top, so nothing derives an
//     offset from the anchor: the same two failures with the same numbers,
//     because recording an anchor nothing reads is the shipped behaviour
//     before this fix.
//
// The clamp at each end of the derive is pinned in internal/terminal, where
// window_scroll_anchor_test.go also records which of its controls turned out
// to be invalid.
//
// The strip, which is the other thing "scrolling" means in tuios, is pinned in
// scroll_strip_workspace_test.go. This file is only about a pane's own
// scrollback.

// anchorProducer starts a background job in the focused pane's shell that
// prints one tagged line every 50ms, and returns once the shell confirms the
// job is running.
//
// It is started before the scroll rather than after, because typing into a
// scrolled pane is itself a documented way back to live output
// (TestTypingWhileScrolledSnapsBackToLiveOutput). The delay in front of the
// loop gives the wheel gesture time to land first.
func anchorProducer(t *testing.T, term *tuitest.Terminal, prefix string, n int, delay string) {
	t.Helper()
	cmd := fmt.Sprintf(
		"{ sleep %s; for i in $(seq 1 %d); do echo \"%s-$i-END\"; sleep 0.05; done; } & echo PRODUCER-UP",
		delay, n, prefix)
	runInShell(t, term, cmd, "PRODUCER-UP", shellTimeout)
}

// TestScrolledPaneHoldsItsPlaceUnderNewOutput is the defect the previous scroll
// agent found and left: the pane is anchored N lines from the bottom, so every
// line the guest prints slides the view one line forward under the user.
//
// Nothing here touches a workspace or a second client. One pane, scrolled back,
// with its own shell printing.
func TestScrolledPaneHoldsItsPlaceUnderNewOutput(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	const filled = 400
	fillScrollback(t, term, "ANCHOR", filled)

	// Start the producer, then scroll back before its first line lands.
	anchorProducer(t, term, "LATE", 120, "2")

	col, row := paneCell(t, term)
	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 25)
	waitScrolledTo(t, term, "ANCHOR", "the wheel did not scroll back through the history",
		func(n int) bool { return n > 0 && n <= filled-40 })

	before := newestVisible(term.Screen(), "ANCHOR")
	t.Logf("the newest ANCHOR line after scrolling back is %d", before)

	// Let the producer run. Every one of its lines pushes the pane's history
	// forward by one.
	if err := term.WaitForText("LATE-1-END", uiTimeout); err != nil {
		// The producer may print entirely off screen, which is the point. Fall
		// back to waiting out its delay rather than failing on an absence.
		time.Sleep(3 * time.Second)
	}
	time.Sleep(4 * time.Second)

	after := newestVisible(term.Screen(), "ANCHOR")
	t.Logf("the newest ANCHOR line after 4s of output is %d", after)

	if after != before {
		t.Fatalf("the view slid under the user: the newest line on screen went from "+
			"ANCHOR-%d-END to ANCHOR-%d-END while the pane was scrolled back and the guest "+
			"was printing. A scrolled pane must stay on the line the user stopped at.\n%s",
			before, after, term.Snapshot())
	}
	alive(t, term, "after output under a scrolled pane")
}

// TestAPeersWorkspaceSwitchLeavesAScrolledPaneWhereItIs is the same defect on
// the path that makes it look random rather than steady, and on a client whose
// user touches nothing at all.
//
// CurrentWorkspace is session state, so a peer switching workspace switches
// this client too. Coming back primes every pane on the workspace from the
// daemon's copy of its history, and that merges rows the local emulator never
// scrolled: the history gets hundreds of lines longer in one call. Held at a
// distance from the end, the view does not creep, it jumps.
//
// It is also the control on the shape of the fix. Counting the lines the
// emulator pushes would pass the row above and fail this one, because no line
// here passes through the emulator's scroll path.
func TestAPeersWorkspaceSwitchLeavesAScrolledPaneWhereItIs(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "anchorws", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}

	a := attachIn(t, base, "anchorws", startOpts{cols: 120, rows: 40})
	waitWindowCount(t, a, 1, "the pane the session opened with")
	enterTerminalMode(t, a)

	const filled = 400
	fillScrollback(t, a, "WSANCHOR", filled)

	// A producer that keeps printing while the workspace is away, so the daemon
	// holds rows this client has never seen when it comes back.
	anchorProducer(t, a, "WSLATE", 200, "1")

	col, row := paneCell(t, a)
	wheelAt(t, a, col, row, tuitest.MouseWheelUp, 25)
	waitScrolledTo(t, a, "WSANCHOR", "the wheel did not scroll back through the history",
		func(n int) bool { return n > 0 && n <= filled-40 })

	before := newestVisible(a.Screen(), "WSANCHOR")
	t.Logf("the newest WSANCHOR line on the scrolled client is %d", before)

	// The second client makes the round trip. Nothing is typed on the first
	// one, which is the point: any key of its own would end the scrolled view
	// on purpose (keyboard_terminal.go, keyboard_wm.go).
	b := attachIn(t, base, "anchorws", startOpts{cols: 120, rows: 40})
	switchWorkspace(t, b, "2", 0)
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 0
	}, uiTimeout); err != nil {
		t.Fatalf("the peer's workspace switch never reached the scrolled client: %v\n%s",
			err, a.Snapshot())
	}
	// Long enough for the producer to finish printing into a pane nobody is
	// watching, so the daemon holds rows the scrolled client has never seen.
	time.Sleep(12 * time.Second)
	switchWorkspace(t, b, "1", 1)
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("the scrolled client never came back to the workspace: %v\n%s",
			err, a.Snapshot())
	}
	time.Sleep(3 * time.Second)

	after := newestVisible(a.Screen(), "WSANCHOR")
	t.Logf("the newest WSANCHOR line after the peer's round trip is %d", after)

	if after != before {
		t.Fatalf("a peer's workspace round trip moved a scrolled view on this client: the "+
			"newest line on screen went from WSANCHOR-%d-END to WSANCHOR-%d-END. The pane's "+
			"history grew while the workspace was away, and the scroll position followed the "+
			"end of it instead of staying on the line the user stopped at.\n%s",
			before, after, a.Snapshot())
	}
	if !strings.Contains(a.Screen().Text(), fmt.Sprintf("WSANCHOR-%d-END", before)) {
		t.Fatalf("the anchored line WSANCHOR-%d-END is not on screen after the round trip\n%s",
			before, a.Snapshot())
	}
	alive(t, a, "after a peer's workspace round trip under a scrolled pane")
}
