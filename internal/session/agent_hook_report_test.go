package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// These tests cover what set-agent-state gained for hook reporters: the kind
// of a block, the harness's session id and the nested-session guard it drives,
// the if_state condition, and the exact transcript join.

func setAgentStateVerb(t *testing.T, c *verbConn, params string) map[string]any {
	t.Helper()
	return result(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":`+params+`}`))
}

func TestSetAgentStateStoresKindOnlyWhileBlocked(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"needs_input","kind":"approval","message":"approve Bash"}`)
	got := result(t, c.call(t, `{"id":2,"verb":"get-agent-state","params":{"session":"work","window":"Window"}}`))
	if got["blocked_by"] != "approval" {
		t.Fatalf("blocked_by = %v, want approval", got["blocked_by"])
	}
	if k := sess.GetState().Windows[0].AgentKind; k != "approval" {
		t.Fatalf("WindowState.AgentKind = %q, want approval", k)
	}

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working"}`)
	if k := sess.GetState().Windows[0].AgentKind; k != "" {
		t.Fatalf("a working report left kind %q behind", k)
	}
}

func TestSetAgentStateRejectsKindOutsideNeedsInput(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	for _, params := range []string{
		`{"session":"work","window":"Window","state":"working","kind":"approval"}`,
		`{"session":"work","window":"Window","state":"needs_input","kind":"permission"}`,
	} {
		resp := c.call(t, `{"id":1,"verb":"set-agent-state","params":`+params+`}`)
		if errCode(t, resp) != ErrVerbInvalidParams {
			t.Errorf("%s: got %v, want invalid_params", params, resp)
		}
	}
}

func TestSetAgentStateIfState(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done"}`)
	res := setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","if_state":"needs_input"}`)
	if res["applied"] != false || res["reason"] != "if_state" || res["state"] != "done" {
		t.Fatalf("a report whose if_state did not hold: %v", res)
	}

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"needs_input"}`)
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","if_state":"needs_input, unknown"}`)
	if res["applied"] != true || res["state"] != "working" {
		t.Fatalf("a report whose if_state held: %v", res)
	}

	resp := c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"work","window":"Window","state":"working","if_state":"blocked"}}`)
	if errCode(t, resp) != ErrVerbInvalidParams {
		t.Fatalf("an unknown if_state state: %v", resp)
	}
}

// TestSetAgentStateRefusesANestedSession is the nested-run case: a claude -p a
// tool call started in the pane runs the same hooks, and its Stop must not mark
// the pane done while the outer turn is still going.
func TestSetAgentStateRefusesANestedSession(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","harness":"claude-code","agent_session_id":"outer"}`)
	res := setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","harness":"claude-code","agent_session_id":"nested"}`)
	if res["applied"] != false || res["reason"] != "foreign_session" {
		t.Fatalf("a nested session's Stop: %v", res)
	}
	if id := sess.GetState().Windows[0].AgentSessionID; id != "outer" {
		t.Fatalf("session id = %q after a refused report, want outer", id)
	}

	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","harness":"claude-code","agent_session_id":"outer"}`)
	if res["applied"] != true {
		t.Fatalf("the pane's own Stop: %v", res)
	}

	// At rest a new conversation takes the pane over: /clear, a restart.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"idle","harness":"claude-code","agent_session_id":"next"}`)
	if res["applied"] != true {
		t.Fatalf("a new session at rest: %v", res)
	}
	if id := sess.GetState().Windows[0].AgentSessionID; id != "next" {
		t.Fatalf("session id = %q, want next", id)
	}

	// A report with no session id is not guarded, as before the field existed.
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","harness":"claude-code","agent_session_id":"next"}`)
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done"}`)
	if res["applied"] != true {
		t.Fatalf("a plain report: %v", res)
	}
}

// TestSetAgentStateNewSessionInTheSameHarnessTakesOver is the interrupted
// turn: Claude Code fires no Stop when the user presses Esc, so the pane stays
// working, and a /clear or /resume then starts a new session id in the same
// process. That report must take the pane over. A nested run is a different
// process and is still refused.
func TestSetAgentStateNewSessionInTheSameHarnessTakesOver(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	// The turn starts and is interrupted: no Stop follows.
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","harness":"claude-code","agent_session_id":"A","harness_pid":4100}`)

	// A nested run in another process is still refused.
	res := setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","harness":"claude-code","agent_session_id":"N","harness_pid":4200}`)
	if res["applied"] != false || res["reason"] != "foreign_session" {
		t.Fatalf("a nested run's Stop: %v", res)
	}
	// So is one that did not say which process it came from.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","harness":"claude-code","agent_session_id":"N"}`)
	if res["applied"] != false || res["reason"] != "foreign_session" {
		t.Fatalf("a report with no harness pid: %v", res)
	}

	// /clear in the same claude process: SessionStart for B.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"idle","harness":"claude-code","agent_session_id":"B","harness_pid":4100}`)
	if res["applied"] != true || res["state"] != "idle" {
		t.Fatalf("a new session from the same harness process: %v", res)
	}
	if id := sess.GetState().Windows[0].AgentSessionID; id != "B" {
		t.Fatalf("session id = %q, want B", id)
	}
	// And B's next turn reports as normal.
	res = setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","harness":"claude-code","agent_session_id":"B","harness_pid":4100}`)
	if res["applied"] != true {
		t.Fatalf("B's next turn: %v", res)
	}
}

// TestListVerbsNamesTheHookFields checks list-verbs lists every hook field of
// set-agent-state. tuios agent-hook reads that list to decide what a daemon
// supports, because a daemon ignores a param it does not know rather than
// refusing it, so a field missing here is a field the hook never sends.
func TestListVerbsNamesTheHookFields(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"list-verbs","params":{"verb":"set-agent-state"}}`))
	verbs, _ := res["verbs"].([]any)
	if len(verbs) != 1 {
		t.Fatalf("list-verbs: %v", res)
	}
	have := map[string]bool{}
	for _, p := range verbs[0].(map[string]any)["params"].([]any) {
		have[p.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"kind", "agent_session_id", "transcript_path", "if_state", "harness_pid"} {
		if !have[name] {
			t.Errorf("set-agent-state does not list %s", name)
		}
	}
}

func TestSetAgentStateRefusesAForeignHarnessMidTurn(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","harness":"codex"}`)
	res := setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"done","harness":"claude-code","agent_session_id":"x"}`)
	if res["applied"] != false || res["reason"] != "foreign_harness" {
		t.Fatalf("a claude run nested in codex: %v", res)
	}
}

// TestAgentIdentitySurvivesAClientSync checks a client push, which never
// carries the kind or the session id, does not wipe them, including from a
// client that does carry the older agent fields.
func TestAgentIdentitySurvivesAClientSync(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{{
		ID: "w1", AgentState: AgentStateNeedsInput, AgentKind: "approval", AgentSessionID: "s1",
	}}}
	incoming := &SessionState{Windows: []WindowState{{ID: "w1", AgentState: AgentStateNeedsInput, AgentMessage: "x"}}}
	retainDaemonExclusive(incoming, canonical)
	w := incoming.Windows[0]
	if w.AgentKind != "approval" || w.AgentSessionID != "s1" {
		t.Fatalf("after a sync: kind %q session %q", w.AgentKind, w.AgentSessionID)
	}
}

func TestSetAgentStateJoinsTheReportedTranscript(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	params := fmt.Sprintf(`{"session":"work","window":"Window","state":"idle","harness":"claude-code","agent_session_id":"s","transcript_path":%q}`, path)
	setAgentStateVerb(t, c, params)

	id := sess.GetState().Windows[0].ID
	sess.transcripts.mu.Lock()
	j := sess.transcripts.joins[id]
	sess.transcripts.mu.Unlock()
	if j == nil || !j.exact || j.reader.Path() != path {
		t.Fatalf("join = %+v, want an exact join on %s", j, path)
	}
}

func TestMatchPaneByProcess(t *testing.T) {
	panes := []paneShell{{"a", "w1", 100}, {"b", "w2", 200}, {"c", "w3", 1}}
	cases := []struct {
		name string
		sid  int
		pids []int
		want string
		by   string
	}{
		{"terminal session", 200, []int{5, 100}, "w2", "tty"},
		{"ancestor after setsid", 999, []int{998, 100, 1}, "w1", "pid"},
		{"init is nobody's pane", 0, []int{1}, "", ""},
		{"nothing matches", 7, []int{8, 9}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ok := matchPaneByProcess(panes, tc.sid, tc.pids)
			if tc.want == "" {
				if ok {
					t.Fatalf("matched %+v, want nothing", m)
				}
				return
			}
			if !ok || m.pane.windowID != tc.want || m.by != tc.by {
				t.Fatalf("got %+v %v, want %s by %s", m, ok, tc.want, tc.by)
			}
		})
	}
}

func TestResolvePaneVerbFindsTheShell(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	w := sess.GetState().Windows[0]
	pid := sess.GetPTY(w.PTYID).ShellPID()
	if pid <= 1 {
		t.Skip("the window has no shell pid on this platform")
	}
	c := dialVerb(t, sp)
	res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"resolve-pane","params":{"pids":[%d]}}`, pid)))
	if res["window_id"] != w.ID || res["session"] != "work" || res["by"] != "pid" {
		t.Fatalf("resolve-pane: %v", res)
	}
}
