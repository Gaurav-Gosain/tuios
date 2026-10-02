//go:build !slim

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

// TestTwoClientsStalePushAfterAnotherClientsMove: client A moves the focus and
// the daemon accepts it. An agent report then advances the version. A push
// from client B, built before A's move, arrives stale. A's move is newer than
// anything B saw, so it stands.
//
// Negative control: with the clientFocusMoved loop cut from
// pushOwnsFocusLocked, B's push puts the focus back on the second window.
func TestTwoClientsStalePushAfterAnotherClientsMove(t *testing.T) {
	sess, first, second := twoPanes(t, "two-clients")

	pushB := clientSnapshot(sess)
	pushB.PushOrigin = "client-b"

	pushA := clientSnapshot(sess)
	pushA.PushOrigin = "client-a"
	pushA.FocusedWindowID = first
	if accepted := sess.UpdateState(pushA); !accepted {
		t.Fatal("setup: client A's current push was refused")
	}

	if err := sess.SetDaemonWindowAgentState(second, AgentStateDone, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	if accepted := sess.UpdateState(pushB); accepted {
		t.Error("client B's stale push was accepted as current")
	}
	if got := sess.GetState().FocusedWindowID; got != first {
		t.Errorf("FocusedWindowID = %q, want client A's move to %q", got, first)
	}
}

// TestAClientsOwnEarlierMoveDoesNotBlockItsStalePush: a client's earlier move
// is older than its own later push, so it cannot make that push lose its
// focus.
func TestAClientsOwnEarlierMoveDoesNotBlockItsStalePush(t *testing.T) {
	sess, first, second := twoPanes(t, "own-move")

	move := clientSnapshot(sess)
	move.PushOrigin = "client-a"
	move.FocusedWindowID = first
	if accepted := sess.UpdateState(move); !accepted {
		t.Fatal("setup: the client's current push was refused")
	}

	back := clientSnapshot(sess)
	back.PushOrigin = "client-a"
	back.FocusedWindowID = second
	if err := sess.SetDaemonWindowAgentState(first, AgentStateDone, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	sess.UpdateState(back)
	if got := sess.GetState().FocusedWindowID; got != second {
		t.Errorf("FocusedWindowID = %q, want the client's latest move to %q", got, second)
	}
}
