//go:build !slim

package session

import "testing"

func TestRestrictConnectionOwnScopeReachesOnlyTheCallersSession(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })

	c := dialVerb(t, sp)
	res := restrict(t, c, map[string]any{"scope": "own"})
	if res["session"] != "a" || res["window"] != a1 || res["via"] != "pid" || res["scope"] != "own" {
		t.Fatalf("restrict = %v, want session a, window a1 by pid", res)
	}
	if got := res["sessions"].([]any); len(got) != 1 || got[0] != "a" {
		t.Errorf("sessions = %v, want [a]", got)
	}

	// An omitted session is the caller's own, not the most recently active
	// one: b was made last and is the most recently active.
	listed := result(t, callP(c, t, "list-agents", nil))
	if listed["session"] != "a" {
		t.Errorf("list-agents with no session listed %v, want a", listed["session"])
	}
	wantForbidden(t, "list-windows of b", callP(c, t, "list-windows", map[string]any{"session": "b"}))
	wantForbidden(t, "capture-pane of b", callP(c, t, "capture-pane", map[string]any{"session": "b", "window": b1}))
	wantForbidden(t, "list-sessions", callP(c, t, "list-sessions", nil))
	wantForbidden(t, "list-agents all_sessions", callP(c, t, "list-agents", map[string]any{"all_sessions": true}))
	wantForbidden(t, "wait-for any_session", callP(c, t, "wait-for", map[string]any{"condition": "agent-state", "any_session": true, "until": "idle", "timeout": 10}))
	wantForbidden(t, "new-window", callP(c, t, "new-window", map[string]any{"session": "a"}))
	wantForbidden(t, "kill-session", callP(c, t, "kill-session", map[string]any{"session": "b"}))
	wantForbidden(t, "send-text into b", callP(c, t, "send-text", map[string]any{"session": "b", "window": b1, "text": "x"}))

	// Writing into another pane of its own session is allowed: the
	// connection is not read-only.
	result(t, callP(c, t, "send-text", map[string]any{"window": a2, "text": "echo scoped\r"}))

	// A self report with no window lands on the caller's own pane, and one
	// naming another pane is refused.
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "working"}))
	if st := d.manager.GetSession("a").GetState(); st.Windows[0].AgentState != AgentStateWorking {
		t.Errorf("the caller's pane is %v after its own report, want working", st.Windows[0].AgentState)
	}
	wantForbidden(t, "set-agent-state on another pane", callP(c, t, "set-agent-state", map[string]any{"window": a2, "state": "idle"}))
	wantForbidden(t, "set-agent-state in b", callP(c, t, "set-agent-state", map[string]any{"session": "b", "window": b1, "state": "idle"}))

	// Mail goes out as the caller's own pane.
	sent := result(t, callP(c, t, "send-agent-message", map[string]any{"to": a2, "text": "hi"}))
	if sent["from"] != a1 {
		t.Errorf("mail from = %v, want the caller's pane %s", sent["from"], a1)
	}
	wantForbidden(t, "mail from another pane", callP(c, t, "send-agent-message", map[string]any{"to": a1, "from": a2, "text": "spoof"}))
	wantForbidden(t, "reading another pane's inbox", callP(c, t, "read-agent-messages", map[string]any{"to": a2}))
	// A session on another machine is never in reach, even one named like
	// the caller's own.
	wantForbidden(t, "mail to another machine", callP(c, t, "send-agent-message", map[string]any{"host": "build", "session": "a", "to": a2, "text": "out"}))
	// The link and held-mail verbs are not for a restricted caller.
	wantForbidden(t, "close-pane", callP(c, t, "close-pane", map[string]any{"pane": "f2c1"}))
	// The shell and ask-human verbs: run is typing, ask-human asks only as
	// the caller's own pane, and answering for the person is never allowed.
	wantForbidden(t, "run in b", callP(c, t, "run", map[string]any{"session": "b", "window": b1, "command": "true"}))
	wantForbidden(t, "ask-human as another pane", callP(c, t, "ask-human", map[string]any{"window": a2, "question": "ok?", "options": []string{"yes"}, "wait": false}))
	wantForbidden(t, "answer-ask", callP(c, t, "answer-ask", map[string]any{"request_id": "x", "answer": "yes", "human_nonce": "n"}))
	wantForbidden(t, "release-agent-message", callP(c, t, "release-agent-message", map[string]any{"session": "a", "id": 1}))
	// A selector reaches every session, so a write or wait by one is refused.
	// list-agents takes one, narrowed to the caller's own session.
	wantForbidden(t, "send-agent-message by selector", callP(c, t, "send-agent-message", map[string]any{"select": "session:b", "text": "x"}))
	wantForbidden(t, "ask-agent by selector", callP(c, t, "ask-agent", map[string]any{"select": "session:b", "text": "x"}))
	wantForbidden(t, "wait-for by selector", callP(c, t, "wait-for", map[string]any{"condition": "agent-state", "select": "session:b", "until": "idle", "timeout": 10}))
	if sel := result(t, callP(c, t, "list-agents", map[string]any{"select": "harness:*"})); sel["session"] != "a" {
		t.Errorf("list-agents by selector listed %v, want the caller's session a", sel["session"])
	}
	// start-agent opens its pane in the caller's own session, never another.
	wantForbidden(t, "start-agent in b", callP(c, t, "start-agent", map[string]any{"session": "b", "agent": "true"}))

	// An unrestricted connection is untouched.
	plain := dialVerb(t, sp)
	result(t, callP(plain, t, "list-windows", map[string]any{"session": "b"}))
	result(t, callP(plain, t, "list-sessions", nil))
}

func TestRestrictConnectionReadOnlyRefusesTyping(t *testing.T) {
	d, sp, a1, a2, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"scope": "own", "read_only": true})

	wantForbidden(t, "send-text", callP(c, t, "send-text", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "send-keys", callP(c, t, "send-keys", map[string]any{"window": a2, "keys": "Enter"}))
	wantForbidden(t, "ask-agent", callP(c, t, "ask-agent", map[string]any{"window": a2, "text": "x"}))
	wantForbidden(t, "respond", callP(c, t, "respond", map[string]any{"window": a2, "action": "approve"}))
	wantForbidden(t, "fan", callP(c, t, "fan", map[string]any{"count": 1, "agent": "claude", "prompt": "x", "repo": "/"}))
	// Reporting its own state, and reading, still work.
	result(t, callP(c, t, "set-agent-state", map[string]any{"state": "idle"}))
	result(t, callP(c, t, "capture-pane", map[string]any{"window": a2}))

	// read_only by itself, over every session.
	all := dialVerb(t, sp)
	restrict(t, all, map[string]any{"scope": "all", "read_only": true})
	result(t, callP(all, t, "list-windows", map[string]any{"session": "b"}))
	result(t, callP(all, t, "list-sessions", nil))
	wantForbidden(t, "send-text under read_only", callP(all, t, "send-text", map[string]any{"session": "a", "window": a2, "text": "x"}))
	wantForbidden(t, "close-window under read_only", callP(all, t, "close-window", map[string]any{"session": "a", "window": a2}))
}
