package session

import (
	"testing"
)

// These tests cover what hook reporters rely on: the hook fields list-verbs
// advertises, and matching a calling process to its pane.

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
