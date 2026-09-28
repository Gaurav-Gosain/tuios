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

// twoPanes is a session with two daemon windows, the second focused.
func twoPanes(t *testing.T, name string) (*Session, string, string) {
	t.Helper()
	sess, err := NewSession(name, &SessionConfig{Shell: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(sess.Stop)
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
	return sess, first.ID, second.ID
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

// TestAFocusVerbOnTheHeldFocusBeatsAStalePush: the focus is on the second
// window, and a push that moves it to the first is in flight. A focus verb
// then names the second window. The value does not change, but the verb is
// the later intent, so the stale push loses.
//
// Negative control: with the markFocusIntentLocked call cut from
// FocusDaemonWindow, the push moves the focus to the first window.
func TestAFocusVerbOnTheHeldFocusBeatsAStalePush(t *testing.T) {
	sess, first, second := twoPanes(t, "verb-intent")

	push := clientSnapshot(sess)
	push.PushOrigin = "client-a"
	push.FocusedWindowID = first

	if err := sess.FocusDaemonWindow(second); err != nil {
		t.Fatalf("FocusDaemonWindow: %v", err)
	}
	sess.UpdateState(push)
	if got := sess.GetState().FocusedWindowID; got != second {
		t.Errorf("FocusedWindowID = %q, want the focus verb's %q", got, second)
	}
}
