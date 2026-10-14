package session

import "testing"

// TestAPushThatDidNotMoveTheFocusKeepsThePeersMove pins the daemon half of
// SessionState.FocusUnmoved. Client a moves the focus and the strip. Client b
// pushes for a reason of its own (a new layout generation) before a's move
// reached it, so its push still names the old focus. The session keeps a's
// move and answers b. In e2e/tui, TestScrollingPeerFollowsFocusIntoView lost
// a's move this way about one run in five under load.
func TestAPushThatDidNotMoveTheFocusKeepsThePeersMove(t *testing.T) {
	sess := newTestSession(t)
	state := func(origin, focus string, x int) *SessionState {
		return &SessionState{
			Windows: []WindowState{
				{ID: "w-alpha", Workspace: 1},
				{ID: "w-bravo", Workspace: 1},
				{ID: "w-charlie", Workspace: 1},
			},
			CurrentWorkspace: 1,
			FocusedWindowID:  focus,
			WorkspaceFocus:   map[int]string{1: focus},
			ScrollStrip:      &ScrollStripState{ViewportX: x},
			PushOrigin:       origin,
		}
	}
	focusOf := func() (string, int) {
		st := sess.GetState()
		x := -1
		if st.ScrollStrip != nil {
			x = st.ScrollStrip.ViewportX
		}
		return st.FocusedWindowID, x
	}

	sess.UpdateState(state("a", "w-charlie", 65))
	sess.UpdateState(state("a", "w-bravo", 51))

	stale := state("b", "w-charlie", 65)
	stale.FocusUnmoved = true
	if sess.UpdateState(stale) {
		t.Error("a push whose stale focus was replaced was accepted as sent, so its client is not told")
	}
	if f, x := focusOf(); f != "w-bravo" || x != 51 {
		t.Errorf("after b's push that did not move the focus, the session has %s at %d, want a's move: w-bravo at 51", f, x)
	}

	// The positive half: b moving the focus itself is taken as sent.
	moved := state("b", "w-alpha", 0)
	if !sess.UpdateState(moved) {
		t.Error("a push that moved the focus was not accepted")
	}
	if f, x := focusOf(); f != "w-alpha" || x != 0 {
		t.Errorf("after b moved the focus, the session has %s at %d, want w-alpha at 0", f, x)
	}
}
