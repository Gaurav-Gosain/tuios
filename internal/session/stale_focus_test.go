package session

import "testing"

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
