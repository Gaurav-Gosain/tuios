package tuie2e

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
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
//   - pane.focus on a pane in another session, one that session does not
//     have focused, moves the client there, with that pane focused, and the
//     reply says the pane is focused. The event stream says
//     workspace_focused, tab_focused and pane_focused for it, once each and
//     in that order, as herdr does when the active workspace changes. A
//     pane_focused for a session no client shows is not herdr's: its
//     workspace is not the active one.
//   - agent.focus on an agent in the first session moves the client back.
//
// Negative controls (e2e/tui/NEGATIVE_CONTROLS.md): with the herdrShowSession
// call cut from herdrFocusPane, the test fails at "pane.focus on a pane in
// another session": the client stays on e2e-home. With the
// client-session-changed case cut from the herdr translator, it times out
// waiting for workspace_focused. With pane_focused sent for a session no
// client shows, the focus events read pane_focused first.
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
	if out, err := tuiosCLI(t, base, "new-window", "farther", "-s", other); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	farWin := windowID(t, base, other, "far")
	if out, err := tuiosCLI(t, base, "focus-window", "-s", other, windowID(t, base, other, "farther")); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, out)
	}
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

	// A pane in another session: the client moves there, showing the pane,
	// and the stream says the active workspace, tab and pane changed.
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(uiTimeout))
	_, _ = conn.Write([]byte(`{"id":"es","method":"events.subscribe","params":{"subscriptions":[{"type":"workspace.focused"},{"type":"tab.focused"},{"type":"pane.focused"}]}}` + "\n"))
	stream := bufio.NewReader(conn)
	if ack, _ := stream.ReadString('\n'); !strings.Contains(ack, "subscription_started") {
		t.Fatalf("subscribe ack %q", ack)
	}
	farWS := strings.SplitN(far, ":", 2)[0]
	focused = herdrCall(t, base, "pane.focus", map[string]any{"pane_id": far})["pane"].(map[string]any)
	seen := map[string]string{}
	var order []string
	var lines strings.Builder
	for seen["workspace_focused"] == "" || order[len(order)-1] != "pane_focused" {
		line, err := stream.ReadString('\n')
		if err != nil {
			t.Fatalf("pane.focus on a pane in another session: the stream ended before workspace_focused and pane_focused: %v\n%s", err, lines.String())
		}
		lines.WriteString(line)
		var ev struct {
			Event string `json:"event"`
			Data  struct {
				WorkspaceID string `json:"workspace_id"`
				PaneID      string `json:"pane_id"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil {
			seen[ev.Event] = ev.Data.WorkspaceID + " " + ev.Data.PaneID
			order = append(order, ev.Event)
		}
	}
	if got := strings.Join(order, " "); got != "workspace_focused tab_focused pane_focused" {
		t.Fatalf("pane.focus on a pane in another session: the focus events read %s, want workspace_focused tab_focused pane_focused\n%s", got, lines.String())
	}
	if !strings.HasPrefix(seen["workspace_focused"], farWS+" ") || seen["pane_focused"] != farWS+" "+far || !strings.HasPrefix(seen["tab_focused"], farWS+" ") {
		t.Fatalf("pane.focus on a pane in another session: the focus events name %v, want workspace %s and pane %s\n%s", seen, farWS, far, lines.String())
	}
	saveText(t, "herdr-focus-events", lines.String())
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
