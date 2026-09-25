package session

import (
	"testing"
)

// newTestSession creates a session backed by a real shell for the state-op tests.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	sess, err := NewSession("ops-test", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess
}

// addWindow adds a daemon window and fails the test with the spawn's own error
// rather than letting it resurface later as a wrong window count. The flake
// that taught this: a transient kernel EPERM on the spawn made this file's
// tests report "window count = 0, want 1" with the actual cause discarded.
func addWindow(t *testing.T, sess *Session, title string) WindowState {
	t.Helper()
	w, err := sess.AddDaemonWindow(title, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow(%q): %v", title, err)
	}
	return w
}

func TestFocusAndCycleDaemonWindows(t *testing.T) {
	sess := newTestSession(t)

	w1 := addWindow(t, sess, "one")
	w2 := addWindow(t, sess, "two")
	w3 := addWindow(t, sess, "three")

	if err := sess.FocusDaemonWindow(w1.ID); err != nil {
		t.Fatalf("FocusDaemonWindow failed: %v", err)
	}
	if sess.GetState().FocusedWindowID != w1.ID {
		t.Fatalf("focus = %q, want %q", sess.GetState().FocusedWindowID, w1.ID)
	}

	// Next wraps forward: w1 -> w2 -> w3 -> w1.
	if err := sess.CycleDaemonFocus(1); err != nil {
		t.Fatalf("CycleDaemonFocus failed: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != w2.ID {
		t.Errorf("after next, focus = %q, want %q", got, w2.ID)
	}

	// Prev wraps backward from w2 to w1.
	if err := sess.CycleDaemonFocus(-1); err != nil {
		t.Fatalf("CycleDaemonFocus(-1) failed: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != w1.ID {
		t.Errorf("after prev, focus = %q, want %q", got, w1.ID)
	}

	// Prev from the first window wraps to the last.
	if err := sess.CycleDaemonFocus(-1); err != nil {
		t.Fatalf("CycleDaemonFocus(-1) wrap failed: %v", err)
	}
	if got := sess.GetState().FocusedWindowID; got != w3.ID {
		t.Errorf("after wrap prev, focus = %q, want %q", got, w3.ID)
	}
}

func TestFindWindowStateIndexAmbiguity(t *testing.T) {
	windows := []WindowState{
		{ID: "aaa-1", CustomName: "dup"},
		{ID: "aaa-2", CustomName: "dup"},
		{ID: "bbb-3", Title: "unique"},
	}

	if _, err := findWindowStateIndex(windows, "aaa"); err == nil {
		t.Error("expected ambiguous prefix error")
	}
	if _, err := findWindowStateIndex(windows, "dup"); err == nil {
		t.Error("expected ambiguous name error")
	}
	if idx, err := findWindowStateIndex(windows, "bbb-3"); err != nil || idx != 2 {
		t.Errorf("exact ID match: idx=%d err=%v", idx, err)
	}
	if idx, err := findWindowStateIndex(windows, "unique"); err != nil || idx != 2 {
		t.Errorf("title match: idx=%d err=%v", idx, err)
	}
	if _, err := findWindowStateIndex(windows, "nope"); err == nil {
		t.Error("expected no-match error")
	}
}

func TestFindWindowStateIndexByPosition(t *testing.T) {
	windows := []WindowState{
		{ID: "aaa-x", Title: "first"},
		{ID: "17b2c3d4-aaaa", Title: "second"},
		{ID: "ccc-z", Title: "third"},
	}

	// An all-digit target in range is the position list-windows prints, and it
	// wins over a digit-only id prefix.
	if idx, err := findWindowStateIndex(windows, "1"); err != nil || idx != 1 {
		t.Errorf("index target 1: idx=%d err=%v", idx, err)
	}
	if idx, err := findWindowStateIndex(windows, "0"); err != nil || idx != 0 {
		t.Errorf("index target 0: idx=%d err=%v", idx, err)
	}
	// Out of range falls through to prefix matching.
	if idx, err := findWindowStateIndex(windows, "17"); err != nil || idx != 1 {
		t.Errorf("digit prefix 17: idx=%d err=%v", idx, err)
	}
	// Out of range and no prefix match is a plain no-match.
	if _, err := findWindowStateIndex(windows, "9"); err == nil {
		t.Error("expected no-match error for out-of-range index")
	}
	// A window named like a digit loses to the position; the exact id always wins.
	named := []WindowState{{ID: "aaa", CustomName: "1"}, {ID: "bbb"}}
	if idx, err := findWindowStateIndex(named, "1"); err != nil || idx != 1 {
		t.Errorf("index beats digit name: idx=%d err=%v", idx, err)
	}
}
