package app

import (
	"strings"
	"testing"
)

// TestFailedSessionSwitchLeavesTheUserWithTheirSession asks to switch to a
// session the daemon will refuse, which is the shape of every switch failure:
// the detach is taken and the attach is not.
//
// A switch is a request to change which session is on screen. Failing it is
// allowed; answering it by leaving the user with no session at all is not.
func TestFailedSessionSwitchLeavesTheUserWithTheirSession(t *testing.T) {
	const panes = 3
	r := newRig(t, panes)

	before := r.m.SessionName
	ptyID := r.win(0).PTYID
	if ptyID == "" {
		t.Fatalf("pane has no PTY")
	}

	// A path separator is refused by the daemon's name validation, so the attach
	// leg fails after the detach leg has already been taken.
	err := r.m.SwitchToSession("bad/name")
	if err == nil {
		t.Fatalf("vacuous: the switch was supposed to fail")
	}
	if !strings.Contains(err.Error(), "bad/name") {
		t.Fatalf("the error does not name the session asked for: %v", err)
	}

	if got := len(r.m.Windows); got != panes {
		t.Fatalf("a failed switch left the user with %d windows instead of %d: %v", got, panes, err)
	}
	if r.m.SessionName != before {
		t.Fatalf("a failed switch moved the client off %q to %q", before, r.m.SessionName)
	}

	// Windows on screen are not enough: the detach dropped every subscription,
	// so a pane that is not streaming again is a picture of a shell, not a shell.
	w := r.winByPTY(ptyID)
	if w == nil {
		t.Fatalf("pane %s is gone from the window set", ptyID)
	}
	r.feed(w, "echo rolledback", "rolledback")
	rigWaitUntil(t, "the pane to stream again", func() bool {
		return strings.Contains(clientText(r.winByPTY(ptyID)), "rolledback")
	})
}
