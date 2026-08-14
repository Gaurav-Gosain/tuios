package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailRightClickKillsTheSessionUnderThePointer drives the destructive path
// end to end against a real daemon: a right-click on a session this client is
// not attached to offers to kill THAT session, the confirmation names it, and
// taking it leaves the client exactly where it was.
//
// It is worth an e2e of its own because the failure it guards against is silent
// and unrecoverable: the menu row used to dispatch an action that addressed the
// attached session, so the one thing this test would catch is a user killing the
// session they were sitting in.
func TestRailRightClickKillsTheSessionUnderThePointer(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	// Right-click the row of the session this client is NOT in.
	col, row := findOnScreen(t, term, "bravo")
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	if err := term.WaitForText("Kill session", uiTimeout); err != nil {
		t.Fatalf("the row's menu offered no kill: %v\n%s", err, term.Snapshot())
	}
	// Opening a menu on a row is not choosing that row: a switch announces
	// itself, and nothing has been taken yet.
	if strings.Contains(term.Screen().Text(), "Session: bravo") {
		t.Fatalf("right-clicking bravo attached this client to it\n%s", term.Snapshot())
	}

	col, row = findOnScreen(t, term, "Kill session")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Close bravo?", uiTimeout); err != nil {
		t.Fatalf("the confirmation did not name the session it would kill: %v\n%s", err, term.Snapshot())
	}

	// The dialog opens on cancel, so the destructive row takes a second answer.
	col, row = findOnScreen(t, term, "Close session")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return !strings.Contains(text, "bravo") && strings.Contains(text, "alpha")
	}, uiTimeout); err != nil {
		t.Fatalf("bravo did not leave the rail, or alpha went with it: %v\n%s", err, term.Snapshot())
	}
	if countWindows(term.Screen()) != 1 {
		t.Errorf("killing another session disturbed this one's panes\n%s", term.Snapshot())
	}
	// The daemon is the authority on which session died, and it is the assertion
	// that would fail if the row's kill had reached the attached session instead.
	out, err := tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("list sessions: %v: %s", err, out)
	}
	if strings.Contains(out, "bravo") || !strings.Contains(out, "alpha") {
		t.Errorf("the daemon still lists bravo, or lost alpha:\n%s", out)
	}

	alive(t, term, "after killing another session from the rail")
}
