package tuie2e

import (
	"encoding/json"
	"testing"
	"time"
)

// waitShown waits until the attached client shows session.
func waitShown(t *testing.T, base, session, why string) {
	t.Helper()
	var shown []string
	for deadline := time.Now().Add(uiTimeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if shown = attachedSessions(base); len(shown) == 1 && shown[0] == session {
			return
		}
	}
	t.Fatalf("%s: the client shows %v, want %s", why, shown, session)
}

// focusedWindowOf is a session's focused window id.
func focusedWindowOf(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "-s", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var st struct {
		Focused string `json:"focused_window_id"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("list-windows output: %v\n%s", err, out)
	}
	return st.Focused
}

// TestHerdrFocusShowsThePanesSession holds pane.focus and agent.focus to
// herdr 0.9.5, where focusing a pane makes its workspace the active one. A
// herdr workspace is a tuios session, so the attached client has to move to
// the pane's session and show the pane, not only focus it in a session
// nobody is looking at.
//
//   - pane.focus on a pane in the session the client shows focuses it, and
//     the client stays.
//   - pane.focus on a pane in another session moves the client there, with
//     that pane focused, and the reply says the pane is focused.
//   - agent.focus on an agent in the first session moves the client back.
//
// Negative control (e2e/tui/NEGATIVE_CONTROLS.md): with the herdrShowSession
// call cut from herdrFocusPane, the test fails at "pane.focus on a pane in
// another session": the client stays on e2e-home.
func TestHerdrFocusShowsThePanesSession(t *testing.T) {
	term, base := crushClient(t)
	home := crushPanes(t, base, "near", "agent")
	const other = "e2e-other"
	if out, err := tuiosCLI(t, base, "new", other, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "far", "-s", other); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	farWin := windowID(t, base, other, "far")
	near := herdrPaneByLabel(t, base, "near")["pane_id"].(string)
	far := herdrPaneByLabel(t, base, "far")["pane_id"].(string)
	agent := herdrPaneByLabel(t, base, "agent")["pane_id"].(string)
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", crushSession, "-w", home["agent"], "--harness", "claude-code", "idle"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	waitShown(t, base, crushSession, "before any focus")

	// A pane in the session the client shows: focused, and the client stays.
	focused := herdrCall(t, base, "pane.focus", map[string]any{"pane_id": near})["pane"].(map[string]any)
	if focused["focused"] != true {
		t.Fatalf("pane.focus on a pane in the shown session: the reply says focused %v", focused["focused"])
	}
	if got := focusedWindowOf(t, base, crushSession); got != home["near"] {
		t.Fatalf("pane.focus on a pane in the shown session focused %s, want %s", got, home["near"])
	}
	waitShown(t, base, crushSession, "pane.focus on a pane in the shown session")

	// A pane in another session: the client moves there, showing the pane.
	focused = herdrCall(t, base, "pane.focus", map[string]any{"pane_id": far})["pane"].(map[string]any)
	waitShown(t, base, other, "pane.focus on a pane in another session")
	if focused["focused"] != true {
		t.Fatalf("pane.focus on a pane in another session: the reply says focused %v, want true", focused["focused"])
	}
	if got := focusedWindowOf(t, base, other); got != farWin {
		t.Fatalf("pane.focus on a pane in another session focused %s, want %s", got, farWin)
	}
	if snap := herdrCall(t, base, "session.snapshot", map[string]any{})["snapshot"].(map[string]any); snap["focused_pane_id"] != far {
		t.Fatalf("after pane.focus the snapshot's focused pane is %v, want %s", snap["focused_pane_id"], far)
	}
	saveFrame(t, term, "herdr-focus-other-session")

	// agent.focus takes the same path back.
	herdrCall(t, base, "agent.focus", map[string]any{"target": agent})
	waitShown(t, base, crushSession, "agent.focus on an agent in the first session")
	if got := focusedWindowOf(t, base, crushSession); got != home["agent"] {
		t.Fatalf("agent.focus focused %s, want %s", got, home["agent"])
	}
	saveFrame(t, term, "herdr-focus-back")
	alive(t, term, "after pane.focus moved the client between sessions")
}
