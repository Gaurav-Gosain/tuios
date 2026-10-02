//go:build !slim

package session

import (
	"testing"
	"time"
)

// TestSetAgentStateFromAPaneMarksThatPane runs set-agent-state with no
// window from inside a pane. It must mark that pane, in its own session,
// and not the focused pane of the session last active. Outside every pane
// it still marks the focused pane.
func TestSetAgentStateFromAPaneMarksThatPane(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	sa := d.manager.GetSession("a")
	focused := sa.GetState().FocusedWindowID
	caller := a1
	if focused == a1 {
		caller = a2
	}
	state := func(sess, id string) AgentState {
		w, _ := findWindowState(d.manager.GetSession(sess).GetState(), id)
		return w.AgentState
	}

	d.setApprovalPeer(func(*connState) (bool, string) { return true, caller })
	pane := dialVerb(t, sp)
	result(t, callP(pane, t, "set-agent-state", map[string]any{"state": "working"}))
	d.setApprovalPeer(nil)
	if got := state("a", caller); got != AgentStateWorking {
		t.Errorf("the caller's pane is %s, want working", got.Name())
	}
	if got := state("a", focused); got != AgentStateNone {
		t.Errorf("the focused pane of a is %s, want none", got.Name())
	}
	if got := state("b", b1); got != AgentStateNone {
		t.Errorf("session b, the last active, is %s, want none", got.Name())
	}

	// Outside every pane, no window means the focused one.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	person := dialVerb(t, sp)
	result(t, callP(person, t, "set-agent-state", map[string]any{"session": "a", "state": "idle"}))
	d.setApprovalPeer(nil)
	if got := state("a", focused); got != AgentStateIdle {
		t.Errorf("from outside a pane the focused pane is %s, want idle", got.Name())
	}
}

// TestWorkspaceRenamedEvent: naming, renaming and clearing a workspace each
// raise workspace-renamed with the workspace and its new name.
func TestWorkspaceRenamedEvent(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "ws")
	sub := dialVerb(t, sp)
	result(t, callP(sub, t, "subscribe", map[string]any{"session": "ws", "types": []string{"workspace-renamed"}}))
	c := dialVerb(t, sp)
	for _, step := range []struct {
		name string
		ws   int
	}{{"build", 2}, {"deploy", 2}, {"", 2}} {
		result(t, callP(c, t, "set-workspace-name", map[string]any{"session": "ws", "workspace": step.ws, "name": step.name}))
		ev := readEvent(t, sub)
		title, _ := ev["title"].(string)
		if ev["type"] != "workspace-renamed" || ev["workspace"] != float64(step.ws) || title != step.name || ev["session"] != "ws" {
			t.Errorf("after naming workspace %d %q: event %v", step.ws, step.name, ev)
		}
	}
	// A name set again to itself raises nothing.
	result(t, callP(c, t, "set-workspace-name", map[string]any{"session": "ws", "workspace": 3, "name": "x"}))
	readEvent(t, sub)
	result(t, callP(c, t, "set-workspace-name", map[string]any{"session": "ws", "workspace": 3, "name": "x"}))
	_ = sub.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if line, err := sub.r.ReadBytes('\n'); err == nil {
		t.Errorf("an unchanged name raised %s", line)
	}
}
