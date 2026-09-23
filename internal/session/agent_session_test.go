package session

import (
	"strings"
	"testing"
)

// These tests cover set-agent-session: a harness naming its conversation in a
// pane without reporting a state, for the integrations whose hooks are trusted
// with identity and not with state.

func setAgentSessionVerb(t *testing.T, c *verbConn, params string) map[string]any {
	t.Helper()
	return result(t, c.call(t, `{"id":1,"verb":"set-agent-session","params":`+params+`}`))
}

// TestSetAgentSessionLeavesTheStateAlone is the point of the verb: the id is
// stored and persisted, and the state, its source and the attribution are what
// they were, so the screen rules keep deciding the pane's state.
func TestSetAgentSessionLeavesTheStateAlone(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"needs_input","source":"screen","harness":"qwen","kind":"approval"}`)
	before := sess.GetState().Windows[0]

	res := setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"qwen","agent_session_id":"q-1"}`)
	if res["applied"] != true || res["agent_session_id"] != "q-1" {
		t.Fatalf("set-agent-session: %v", res)
	}
	w := sess.GetState().Windows[0]
	if w.AgentSessionID != "q-1" {
		t.Fatalf("session id = %q, want q-1", w.AgentSessionID)
	}
	if w.AgentState != before.AgentState || w.AgentKind != before.AgentKind || w.AgentHarness != before.AgentHarness || w.AgentStateAt != before.AgentStateAt {
		t.Fatalf("the report changed the state: before %+v after %+v", before, w)
	}
	if src := sess.agentClaimFor(w.ID).source; src != AgentSourceScreen {
		t.Fatalf("claim source = %q, want the screen's claim untouched", src)
	}
	got := result(t, c.call(t, `{"id":2,"verb":"get-agent-state","params":{"session":"work","window":"Window"}}`))
	if got["agent_session_id"] != "q-1" || got["state"] != "needs_input" {
		t.Fatalf("get-agent-state: %v", got)
	}

	// The same id again is taken and changes nothing, not even the version.
	version := sess.GetState().Version
	if res := setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"qwen","agent_session_id":"q-1"}`); res["applied"] != true {
		t.Fatalf("a repeat: %v", res)
	}
	if v := sess.GetState().Version; v != version {
		t.Fatalf("a repeat bumped the version from %d to %d", version, v)
	}
}

// TestSetAgentSessionDoesNotAttribute checks a report onto a pane nothing has
// named stores the id and leaves the pane a non-agent pane: attribution is the
// detector's, and a pane named by this alone would read as an agent with no
// state.
func TestSetAgentSessionDoesNotAttribute(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	res := setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"droid","agent_session_id":"d-1"}`)
	if res["applied"] != true {
		t.Fatalf("set-agent-session: %v", res)
	}
	w := sess.GetState().Windows[0]
	if w.AgentSessionID != "d-1" || w.AgentHarness != "" || w.AgentState != AgentStateNone || isAgentWindow(w) {
		t.Fatalf("window after an identity report: %+v", w)
	}
}

// TestSetAgentSessionRefusesANestedRun covers both guards: a harness other
// than the pane's, and a second process of the pane's own harness mid-turn.
func TestSetAgentSessionRefusesANestedRun(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)

	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"working","source":"screen","harness":"qwen"}`)
	setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"qwen","agent_session_id":"outer","harness_pid":4100}`)

	res := setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"droid","agent_session_id":"x","harness_pid":4300}`)
	if res["applied"] != false || res["reason"] != "foreign_harness" || res["agent_session_id"] != "outer" {
		t.Fatalf("another harness nested in the pane: %v", res)
	}
	res = setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"qwen","agent_session_id":"nested","harness_pid":4200}`)
	if res["applied"] != false || res["reason"] != "foreign_session" {
		t.Fatalf("a nested qwen mid-turn: %v", res)
	}
	if id := sess.GetState().Windows[0].AgentSessionID; id != "outer" {
		t.Fatalf("session id = %q after refused reports, want outer", id)
	}

	// The same process starting a new conversation (/clear) takes over.
	res = setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"qwen","agent_session_id":"next","harness_pid":4100}`)
	if res["applied"] != true || res["agent_session_id"] != "next" {
		t.Fatalf("a new conversation in the same process: %v", res)
	}

	// At rest any process may: a new conversation is what it looks like.
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"idle","source":"screen","harness":"qwen"}`)
	res = setAgentSessionVerb(t, c, `{"session":"work","window":"Window","harness":"qwen","agent_session_id":"later","harness_pid":4500}`)
	if res["applied"] != true {
		t.Fatalf("a new conversation at rest: %v", res)
	}
}

// TestDetectorForgetsTheHarnessPIDWhenTheAgentLeaves checks a harness started
// again in the same pane is not refused as a nested run of the one that
// exited: the detector clearing the pane forgets the old process.
func TestDetectorForgetsTheHarnessPIDWhenTheAgentLeaves(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, winID)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "qwen", argv: []string{"qwen"}}, true}})
	shell := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "fish", argv: []string{"-fish"}}, true}})

	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("start: detection changed %d windows", n)
	}
	if _, ok, reason, err := sess.applyAgentSession(winID, AgentSessionReport{Harness: "qwen", SessionID: "first", HarnessPID: 4100}); err != nil || !ok {
		t.Fatalf("first report: %v %v %s", ok, err, reason)
	}
	// The agent exits: the pane is back at its shell.
	sess.applyAgentDetection(shell, agent.identifyDetail)
	// A new qwen starts in the pane and the detector says working again.
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
		t.Fatalf("restart: detection changed %d windows", n)
	}
	_, ok, reason, err := sess.applyAgentSession(winID, AgentSessionReport{Harness: "qwen", SessionID: "second", HarnessPID: 5200})
	if err != nil || !ok {
		t.Fatalf("a restarted harness was refused: %s %v", reason, err)
	}
}

func TestSetAgentSessionValidatesItsParams(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	for _, params := range []string{
		`{"session":"work","window":"Window","agent_session_id":"x"}`,
		`{"session":"work","window":"Window","harness":"qwen"}`,
		`{"session":"work","window":"Window","harness":"qwen","agent_session_id":" "}`,
		`{"session":"work","window":"Window","harness":"qwen","agent_session_id":"` + strings.Repeat("a", 300) + `"}`,
	} {
		if code := errCode(t, c.call(t, `{"id":1,"verb":"set-agent-session","params":`+params+`}`)); code != ErrVerbInvalidParams {
			t.Errorf("%s: got %s, want invalid_params", params, code)
		}
	}
	if code := errCode(t, c.call(t, `{"id":1,"verb":"set-agent-session","params":{"session":"work","window":"nope","harness":"qwen","agent_session_id":"x"}}`)); code == "" {
		t.Error("an unknown window was not an error")
	}
}
