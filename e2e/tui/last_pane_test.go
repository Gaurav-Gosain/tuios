package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The last_pane action (`;`) flips between the two panes focus last travelled
// between. The record of the pane focus left is settled once a message, off
// the landed focus, so every path that moves focus feeds it: the pane jumps
// here go through the focus-window CLI, the new pane through `n`, the
// workspace switch through Alt+2.
//
// NEGATIVE CONTROL: against a binary whose PrevFocusedID is written only in
// FocusWindow (main before the fix), the new-pane and workspace cases return
// a stale pane instead of the one focus just left.

func TestLastPane(t *testing.T) {
	term, base := tiledPanes(t, "lastpane", "", 3)
	t.Cleanup(func() { killDaemonNow(t, base) })

	_, cur := focusedLayout(t, base, "lastpane")
	if cur == "" {
		t.Fatal("no pane is focused after the setup")
	}
	layout, _ := focusedLayout(t, base, "lastpane")
	others := make([]string, 0, 3)
	for _, r := range layout {
		others = append(others, r.ID)
	}
	if len(others) != 3 {
		t.Fatalf("setup laid out %d panes, want 3", len(others))
	}
	focusPane := func(id string) string {
		out, err := tuiosCLI(t, base, "focus-window", "--session", "lastpane", id)
		if err != nil {
			t.Fatalf("focus-window %s: %v: %s", id[:8], err, out)
		}
		return waitFocusChange(t, base, "lastpane", cur)
	}

	// Walk the focus 1, 2, 3 and flip back and forth: the plain case.
	for _, id := range others {
		cur = focusPane(id)
	}
	if cur != others[2] {
		t.Fatalf("focus ended on %s, want the last pane %s", cur[:8], others[2][:8])
	}
	send(t, term, ";")
	if got := waitFocusChange(t, base, "lastpane", cur); got != others[1] {
		t.Fatalf("first flip landed on %s, want %s\n%s", got[:8], others[1][:8], term.Snapshot())
	}
	send(t, term, ";")
	if got := waitFocusChange(t, base, "lastpane", others[1]); got != others[2] {
		t.Fatalf("second flip landed on %s, want %s\n%s", got[:8], others[2][:8], term.Snapshot())
	}

	// A new pane takes focus without a jump, and the flip must still come
	// back to the pane focus just left.
	send(t, term, "n")
	waitWindowCount(t, term, 4, "the new pane")
	fresh := waitFocusChange(t, base, "lastpane", others[2])
	if fresh == others[2] {
		t.Fatalf("the new pane never took focus\n%s", term.Snapshot())
	}
	send(t, term, ";")
	if got := waitFocusChange(t, base, "lastpane", fresh); got != others[2] {
		t.Fatalf("after a new pane the flip landed on %s, want %s\n%s",
			got[:8], others[2][:8], term.Snapshot())
	}

	// A switch to an empty workspace lands on no pane at all, so the pane
	// focus left is the one the first pane there must flip back to.
	sendKeys(t, term, tuitest.Alt("2"))
	time.Sleep(500 * time.Millisecond)
	send(t, term, "n")
	// The dock counts the current workspace's panes, and workspace two has
	// exactly the one.
	waitWindowCount(t, term, 1, "the pane on workspace two")
	fresh = waitFocusChange(t, base, "lastpane", fresh)
	send(t, term, ";")
	if got := waitFocusChange(t, base, "lastpane", fresh); got != others[2] {
		t.Fatalf("after the workspace switch the flip landed on %s, want %s\n%s",
			got[:8], others[2][:8], term.Snapshot())
	}
}
