package session

import (
	"strings"
	"testing"
	"time"
)

// Fixes a native Collie adapter over the verb socket needs.

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

// TestSendKeysComma: Comma sends the "," that send-keys splits keys on.
func TestSendKeysComma(t *testing.T) {
	keys, err := parseSendKeys("a,Comma b comma", 0)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, k := range keys {
		b.Write(k.bytes(false))
	}
	if b.String() != "a,b," {
		t.Errorf("bytes %q, want a,b,", b.String())
	}
	if c, _ := sendKeysCanonical(keys); c != "a Comma b Comma" {
		t.Errorf("canonical %q", c)
	}
	k, err := parseKeyToken("alt+Comma")
	if err != nil || string(k.bytes(false)) != "\x1b," {
		t.Errorf("alt+Comma = %q, %v", k.bytes(false), err)
	}
}

// TestSessionIDSurvivesRestore saves a session, drops it, and restores it
// from its state file: it comes back with the same id. State from before ids
// were saved, and an id another session holds, get a new one.
func TestSessionIDSurvivesRestore(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "keep")
	id := sess.ID
	if err := sess.persist(sess.ResurrectionState()); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadResurrectionState("keep")
	if err != nil {
		t.Fatal(err)
	}
	if saved.SessionID != id {
		t.Fatalf("the state file holds session_id %q, want %q", saved.SessionID, id)
	}
	if err := d.manager.DeleteSession("keep"); err != nil {
		t.Fatal(err)
	}
	back, err := d.restoreSession(saved)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != id {
		t.Errorf("restored id %q, want %q", back.ID, id)
	}

	// Old state has no id.
	old := *saved
	old.Name, old.SessionID = "old", ""
	fresh, err := d.restoreSession(&old)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == "" || fresh.ID == id {
		t.Errorf("state with no id restored as %q", fresh.ID)
	}
	// An id a live session holds is not taken twice.
	dup := *saved
	dup.Name = "dup"
	other, err := d.restoreSession(&dup)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == id {
		t.Error("two live sessions share one id")
	}
}
