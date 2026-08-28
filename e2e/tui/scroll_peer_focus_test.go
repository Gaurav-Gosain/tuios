package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The report this file exists for: two clients attached to one daemon session
// in the scrolling (niri) layout, and one of them moves focus. The other draws
// the focus border on the new window and leaves its viewport where it was, so
// the focused window can be entirely off its screen.
//
// Two real tuios processes on one daemon, three named panes, and the assertion
// is what the second client has on screen.
//
// NEGATIVE CONTROL: run against the tree before the fix, the second client's
// frame after the step still read "BRAVO ... CHARLIE" while the client that had
// moved the focus read "ALPHA ... BRAVO", and the assertion below failed. The
// same run at the internal level is TestPeerScrollsFocusIntoView, where the
// peer had the focused pane at x=-65 on a screen 100 wide.
func TestScrollingPeerFollowsFocusIntoView(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "strip", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}

	a := attachIn(t, base, "strip", startOpts{cols: 100, rows: 30})
	renameWindow(t, a, "ALPHA")
	newWindow(t, a)
	renameWindow(t, a, "BRAVO")
	newWindow(t, a)
	renameWindow(t, a, "CHARLIE")
	waitWindowCount(t, a, 3, "three panes on the first client")
	enableTiling(t, a)

	// The scrolling layout is chosen from the command palette, and the choice is
	// session state: it reaches the second client through the daemon.
	if err := a.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	waitPaletteOpen(t, a, "to pick the scrolling layout")
	if err := a.SendKeys("scrolling", tuitest.Enter); err != nil {
		t.Fatalf("pick the scrolling layout: %v", err)
	}
	waitPaletteClosed(t, a, "after picking the scrolling layout")
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("the first client never took the scrolling layout: %v\n%s", err, a.Snapshot())
	}
	t.Logf("the first client in the scrolling layout:\n%s", a.Snapshot())

	b := attachIn(t, base, "strip", startOpts{cols: 100, rows: 30})
	// The columns are 55% of the screen each, so at most two are ever on screen
	// and the far one never is. Wait until the second client is showing the
	// same end of the strip as the first.
	if err := b.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "CHARLIE")
	}, uiTimeout); err != nil {
		t.Fatalf("the second client never showed the focused column: %v\n%s", err, b.Snapshot())
	}
	time.Sleep(time.Second)

	before := b.Snapshot()
	t.Logf("the second client before the first one moves focus:\n%s", before)
	if !strings.Contains(before, "CHARLIE") {
		t.Fatalf("the second client is not showing the focused column to start with:\n%s", before)
	}

	// The gesture in the report: alt+p steps the focus one column left, twice,
	// which lands on the column at the far left of the strip.
	for range 2 {
		if err := a.SendKeys(tuitest.Alt("p")); err != nil {
			t.Fatalf("step the focus left: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ALPHA")
	}, uiTimeout); err != nil {
		t.Fatalf("the first client never scrolled to the focused column: %v\n%s", err, a.Snapshot())
	}
	time.Sleep(2 * time.Second)

	after := b.Snapshot()
	t.Logf("the first client, which moved the focus:\n%s", a.Snapshot())
	t.Logf("the second client after the first one moved focus:\n%s", after)
	if !strings.Contains(after, "ALPHA") {
		t.Errorf("the second client never brought the focused column on screen:\n%s", after)
	}
	// The whole strip moved, not just enough of it to show a sliver: the column
	// at the far end is off screen on both clients now.
	if strings.Contains(after, "CHARLIE") {
		t.Errorf("the second client is still showing the far end of the strip:\n%s", after)
	}
}
