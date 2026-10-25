package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The terminal focus keys walk from the outermost pane into the rail on that
// edge, and the opposite key walks back out to the panes. Before, the key did
// nothing at the edge and the rail took a leader chord or window mode's s.
//
// The dock's mode pill says SIDEBAR while the rail owns the keyboard, which is
// what this reads. A command typed after leaving proves the keyboard is back on
// a pane rather than swallowed by the rail.
//
// NEGATIVE CONTROL: with the enterRailAtEdge call cut from
// FocusTerminalDirection, alt+right on the rightmost pane leaves the pill on
// Terminal mode and the test fails waiting for SIDEBAR. With the
// LeaveRailToward check cut from handleSidebarKey, alt+left is swallowed by the
// rail and the test fails waiting for the pill to drop SIDEBAR. With it cut
// from FocusTerminalDirection, ctrl+b left moves pane focus while the rail
// keeps the keyboard, and the test fails the same way.
func TestFocusKeysWalkIntoAndOutOfRail(t *testing.T) {
	const session = "focus-rail-edge"
	term, base := railClient(t, session, "[appearance.sidebar]\nenabled = true\nposition = \"right\"\nwidth = 28\n", startOpts{cols: 140, rows: 40})
	newWindow(t, term)
	enableTiling(t, term)
	enterTerminalMode(t, term)

	rects := waitForSettledGeometryIn(t, base, session, 2)
	rightmost := rects[0]
	for _, r := range rects[1:] {
		if r.X > rightmost.X {
			rightmost = r
		}
	}
	_, cur := focusedLayout(t, base, session)
	if cur != rightmost.ID {
		send(t, term, tuitest.Alt(tuitest.Right))
		if cur = waitFocusChange(t, base, session, cur); cur != rightmost.ID {
			t.Fatalf("alt+right did not reach the rightmost pane %s, focus is on %s\n%s", rightmost.ID[:8], cur[:8], term.Snapshot())
		}
	}

	// The positive half of the left edge below: with a pane to the left, the
	// key moves between panes and the rail stays out of it.
	send(t, term, tuitest.Alt(tuitest.Left))
	left := waitFocusChange(t, base, session, cur)
	if left == cur {
		t.Fatalf("alt+left from the rightmost pane did not move focus\n%s", term.Snapshot())
	}
	// The leftmost pane runs into the screen edge, and no rail is there.
	send(t, term, tuitest.Alt(tuitest.Left))
	railStaysOut(t, term, "alt+left on the leftmost pane entered the rail on the right edge")
	send(t, term, tuitest.Alt(tuitest.Right))
	if cur = waitFocusChange(t, base, session, left); cur != rightmost.ID {
		t.Fatalf("alt+right from the left pane went to %s, want the rightmost pane %s\n%s", cur[:8], rightmost.ID[:8], term.Snapshot())
	}
	railStaysOut(t, term, "alt+right into a pane also entered the rail")

	// Into the rail: the rightmost pane runs into the rail's edge.
	send(t, term, tuitest.Alt(tuitest.Right))
	waitText(t, term, "the mode pill after alt+right on the rightmost pane", "SIDEBAR")
	if _, f := focusedLayout(t, base, session); f != cur {
		t.Fatalf("entering the rail moved pane focus from %s to %s", cur[:8], f[:8])
	}
	saveFrame(t, term, "rail-entered")

	// And back out, onto the pane it was entered from.
	send(t, term, tuitest.Alt(tuitest.Left))
	leaveRail(t, term, base, session, cur, "alt+left", "BACK")
	saveFrame(t, term, "rail-left")

	// The prefix arrows take the same path in and out. The rail swallows no
	// prefix key, so leaving rests on FocusTerminalDirection alone.
	send(t, term, tuitest.Ctrl('b'), tuitest.Right)
	waitText(t, term, "the mode pill after ctrl+b right on the rightmost pane", "SIDEBAR")
	send(t, term, tuitest.Ctrl('b'), tuitest.Left)
	leaveRail(t, term, base, session, cur, "ctrl+b left", "PREFIX")
	saveFrame(t, term, "rail-left-prefix")
	alive(t, term, "after walking into and out of the rail")
}

// railStaysOut fails when the mode pill turns to SIDEBAR within a few frames,
// so a key that wrongly entered the rail is caught after its frame lands.
func railStaysOut(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "SIDEBAR")
	}, 500*time.Millisecond) == nil {
		t.Fatalf("%s\n%s", what, term.Snapshot())
	}
}

// leaveRail waits for the rail to give the keyboard back, then proves a pane
// has it by echoing word-42, and that the pane is the one the rail was entered
// from.
func leaveRail(t *testing.T, term *tuitest.Terminal, base, session, from, key, word string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "SIDEBAR")
	}, uiTimeout); err != nil {
		t.Fatalf("%s did not leave the rail: %v\n%s", key, err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("echo "+word+"-$((6*7))", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the pane's echo after "+key+" left the rail", word+"-42")
	if _, f := focusedLayout(t, base, session); f != from {
		t.Fatalf("%s left the rail onto %s, want the pane it was entered from %s", key, f[:8], from[:8])
	}
}
