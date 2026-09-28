package session

import "testing"

// TestAStalePushKeepsAFocusTheDaemonNeverMoved: the person focuses a pane,
// and an agent reports a state on another pane before the push lands. The
// push is stale, but nothing the daemon did moved the focus, so the person's
// focus stands. The daemon's used to win, and the reconcile reply snapped the
// client back to the pane it had just left.
//
// Negative control: with the keepClientFocus call cut from UpdateStateFrom,
// the focus stays on the second window and the check fails.
func TestAStalePushKeepsAFocusTheDaemonNeverMoved(t *testing.T) {
	sess, err := NewSession("focus", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	first, err := sess.AddDaemonWindow("review", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	second, err := sess.AddDaemonWindow("build", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != second.ID {
		t.Fatalf("setup: focus = %q, want the second window %q", got, second.ID)
	}

	// The client moves the focus, built on the version it saw.
	push := clientSnapshot(sess)
	push.FocusedWindowID = first.ID

	// An agent reports before the push arrives. It moves no focus.
	if err := sess.SetDaemonWindowAgentState(second.ID, AgentStateDone, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	if accepted := sess.UpdateState(push); accepted {
		t.Error("a push built before a daemon mutation was accepted as current")
	}
	got := sess.GetState()
	if got.FocusedWindowID != first.ID {
		t.Errorf("FocusedWindowID = %q, want the client's move to %q", got.FocusedWindowID, first.ID)
	}
	if w := windowByID(t, got, second.ID); w == nil || w.AgentState != AgentStateDone {
		t.Errorf("the agent's report was lost to the push: %+v", w)
	}
}

// TestAStalePushLosesToADaemonFocusMove: when the mutation the client missed
// did move the focus, the daemon's focus still wins.
func TestAStalePushLosesToADaemonFocusMove(t *testing.T) {
	sess, err := NewSession("focus-move", &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	first, err := sess.AddDaemonWindow("review", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	if _, err := sess.AddDaemonWindow("build", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	push := clientSnapshot(sess)
	push.FocusedWindowID = first.ID

	third, err := sess.AddDaemonWindow("new", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	sess.UpdateState(push)
	if got := sess.GetState().FocusedWindowID; got != third.ID {
		t.Errorf("FocusedWindowID = %q, want the daemon's move to %q", got, third.ID)
	}
}
