package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railPill is the dock mode pill's text while the sidebar rail owns the
// keyboard. Asserting on it proves the keyboard scope actually took, not just
// that a key was sent.
const railPill = "SIDEBAR"

// TestSidebarKeyboardSwitchAndExit drives the rail entirely by keyboard against
// a real daemon: s enters the rail (the dock pill reads SIDEBAR), j walks the
// cursor down to the other session, enter switches to it, and esc leaves the
// rail so focus returns to the panes. It mirrors the mouse switch test with keys.
func TestSidebarKeyboardSwitchAndExit(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "alpha", "--detach"); err != nil {
		t.Fatalf("create alpha: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "bravo", "--detach"); err != nil {
		t.Fatalf("create bravo: %v: %s", err, out)
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

	// s enters the rail: the dock pill switches to SIDEBAR, the authoritative
	// "who owns the keyboard" indicator.
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}

	// G jumps to the last row. The rail's last two rows are its footer controls
	// (the width stepper, then "+ new"), so two k steps land on the row above
	// them: the other (collapsed) session bravo.
	if err := term.SendKeys("G"); err != nil {
		t.Fatalf("jump to last row: %v", err)
	}
	for range 2 {
		if err := term.SendKeys("k"); err != nil {
			t.Fatalf("move cursor: %v", err)
		}
	}

	// enter activates the cursor row and switches to bravo.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate cursor row: %v", err)
	}
	if err := term.WaitForText("Session: bravo", uiTimeout); err != nil {
		t.Fatalf("enter on the bravo row did not switch sessions: %v\n%s", err, term.Snapshot())
	}

	// esc leaves the rail: focus returns to the panes and the SIDEBAR pill goes.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("leave rail: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), railPill)
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not return focus to the panes: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after driving the sidebar by keyboard")
}
