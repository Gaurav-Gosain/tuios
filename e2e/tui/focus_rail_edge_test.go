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
// rail and the test fails waiting for the pill to drop SIDEBAR.
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
	if strings.Contains(term.Screen().Text(), "SIDEBAR") {
		t.Fatalf("alt+left on the leftmost pane entered the rail on the right edge\n%s", term.Snapshot())
	}
	send(t, term, tuitest.Alt(tuitest.Right))
	if cur = waitFocusChange(t, base, session, left); cur != rightmost.ID {
		t.Fatalf("alt+right from the left pane went to %s, want the rightmost pane %s\n%s", cur[:8], rightmost.ID[:8], term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "SIDEBAR") {
		t.Fatalf("alt+right into a pane also entered the rail\n%s", term.Snapshot())
	}

	// Into the rail: the rightmost pane runs into the rail's edge.
	send(t, term, tuitest.Alt(tuitest.Right))
	waitText(t, term, "the mode pill after alt+right on the rightmost pane", "SIDEBAR")
	if _, f := focusedLayout(t, base, session); f != cur {
		t.Fatalf("entering the rail moved pane focus from %s to %s", cur[:8], f[:8])
	}
	saveFrame(t, term, "rail-entered")

	// And back out, onto the pane it was entered from.
	send(t, term, tuitest.Alt(tuitest.Left))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "SIDEBAR")
	}, uiTimeout); err != nil {
		t.Fatalf("alt+left did not leave the rail: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("echo BACK-$((6*7))", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the pane's echo after leaving the rail", "BACK-42")
	if _, f := focusedLayout(t, base, session); f != cur {
		t.Fatalf("leaving the rail focused %s, want the pane it was entered from %s", f[:8], cur[:8])
	}
	saveFrame(t, term, "rail-left")
	alive(t, term, "after walking into and out of the rail")
}
