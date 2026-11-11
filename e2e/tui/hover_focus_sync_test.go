package tuie2e

import (
	"testing"
	"time"
)

// TestHoverFocusReachesTheDaemon is issue #1842: with focus_follows_mouse on,
// hovering a pane moved the client's focus but never told the daemon, because
// motion does not sync. The daemon kept the old focused window, so the next
// state push it sent (a pane's output, an agent state, a rename) gave the old
// pane the focus back.
//
// Hover the left half and then the right half of two tiled panes. The daemon
// must name a different focused window after each hover, and a rename of the
// pane hovered first, which pushes state to the client, must not move it back.
func TestHoverFocusReachesTheDaemon(t *testing.T) {
	const session = "hoverfocus"
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nfocus_follows_mouse = true\n")
	term := startIn(t, base, startOpts{cols: 160, rows: 30, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "hover focus setup")
	enableTiling(t, term)
	time.Sleep(time.Second)

	focused := func() string {
		wl, err := daemonWindows(base, session)
		if err != nil {
			t.Fatalf("list-windows: %v", err)
		}
		return wl.FocusedWindowID
	}
	// waitFocusMoves waits until the daemon names a focused window other than
	// from, and returns it.
	waitFocusMoves := func(from, what string) string {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			if got := focused(); got != "" && got != from {
				return got
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: %s: the daemon still names %s as the focused window\n%s", what, from, term.Snapshot())
		return ""
	}

	// Whichever pane has the focus now, one of the two halves moves it.
	start := focused()
	mouseHover(t, term, 40, 15)
	time.Sleep(time.Second)
	left := focused()
	if left == start {
		mouseHover(t, term, 120, 15)
		left = waitFocusMoves(start, "after a hover over the other pane")
		mouseHover(t, term, 40, 15)
		waitFocusMoves(left, "after a hover back over the first pane")
		left = focused()
	}
	mouseHover(t, term, 120, 15)
	right := waitFocusMoves(left, "after a hover from the left pane to the right one")

	// A rename of the left pane makes the daemon push its state to the client.
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "-w", left, "--name", "hovered-away"); err != nil {
		t.Fatalf("rename-window: %v\n%s", err, out)
	}
	waitText(t, term, "the renamed pane", "hovered-away")
	time.Sleep(time.Second)
	if got := focused(); got != right {
		t.Fatalf("ASSERTION: after a state push the daemon names %s as focused, want the hovered pane %s\n%s", got, right, term.Snapshot())
	}
}
