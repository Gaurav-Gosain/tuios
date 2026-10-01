package session

import (
	"strconv"
	"testing"
	"time"
)

// These tests cover the subagents a pane's agent is running: what moves the
// set, what clears it, the guard on report-agent-activity, the expiry of a
// subagent that never stopped, and the rate a pane may report at.

// reportActivity calls report-agent-activity for the work session's window
// as the Claude Code hook would, in conversation sid.
func reportActivity(t *testing.T, c *verbConn, sid string, activity map[string]any) map[string]any {
	t.Helper()
	return callP(c, t, "report-agent-activity", map[string]any{
		"session": "work", "window": "Window", "harness": "claude-code", "agent_session_id": sid, "activity": activity,
	})
}

// subagentStart is a subagent_start activity for id.
func subagentStart(id string) map[string]any {
	return map[string]any{"event": "subagent_start", "agent_id": id, "agent_type": "Explore"}
}

// paneSubagents is what the session's one window says about its subagents:
// the count it carries and the subagents key of its metadata.
func paneSubagents(sess *Session) (int, string) {
	w := sess.GetState().Windows[0]
	return w.AgentSubagents, agentMetaValue(w.AgentMeta, AgentMetaSubagents)
}

// agentAtRest gives the work window an agent at rest in conversation s1.
func agentAtRest(t *testing.T, c *verbConn) {
	t.Helper()
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"idle","harness":"claude-code","agent_session_id":"s1"}`)
}

// TestMoveSubagents holds moveSubagentsLocked to what each change does: a
// start is counted, a second start of the same id is not but renews it, a
// stop for an id never started is nothing, the cap holds, a pane with no agent
// state counts nothing, and a session start forgets them all.
func TestMoveSubagents(t *testing.T) {
	s := &Session{}
	w := &WindowState{ID: "w", AgentState: AgentStateDone}
	start := func(id string, now int64) (int, bool) {
		return s.moveSubagentsLocked(w, subagentChange{op: ActivitySubagentStart, id: id, agentType: "Explore"}, now)
	}
	if n, moved := start("a1", 100); n != 1 || !moved {
		t.Fatalf("a start: %d %v, want 1 and moved", n, moved)
	}
	if n, moved := start("a1", 200); n != 1 || moved {
		t.Fatalf("a second start of a1: %d %v, want 1 and not moved", n, moved)
	}
	if seen := s.agentSubagents["w"]["a1"].seen; seen != 200 {
		t.Fatalf("a second start left a1 seen at %d, want it renewed to 200", seen)
	}
	if n, moved := s.moveSubagentsLocked(w, subagentChange{op: ActivitySubagentStop, id: "never"}, 300); n != 1 || moved {
		t.Fatalf("a stop for an unknown id: %d %v, want 1 and not moved", n, moved)
	}
	for i := 2; i <= subagentsMax; i++ {
		if n, moved := start("a"+strconv.Itoa(i), 400); n != i || !moved {
			t.Fatalf("start %d: %d %v", i, n, moved)
		}
	}
	if n, moved := start("past-the-cap", 500); n != subagentsMax || moved {
		t.Fatalf("a start past the cap: %d %v, want %d and not moved", n, moved, subagentsMax)
	}
	if n, moved := s.moveSubagentsLocked(w, subagentChange{op: ActivitySubagentStop, id: "a1"}, 600); n != subagentsMax-1 || !moved {
		t.Fatalf("a stop: %d %v, want %d and moved", n, moved, subagentsMax-1)
	}
	if n, moved := s.moveSubagentsLocked(w, subagentChange{op: ActivitySessionStart}, 700); n != 0 || !moved || len(s.agentSubagents) != 0 {
		t.Fatalf("a session start: %d %v with %d sets left, want 0, moved and none", n, moved, len(s.agentSubagents))
	}
	none := &WindowState{ID: "n"}
	if n, moved := s.moveSubagentsLocked(none, subagentChange{op: ActivitySubagentStart, id: "a1"}, 800); n != 0 || moved {
		t.Fatalf("a start on a pane with no state: %d %v, want 0 and not moved", n, moved)
	}
}

// TestSubagentsClearWithTheAgent: the count clears on the agent's session
// start, when the pane's state goes to none, and when the window closes. Each
// clear comes after a start that was counted, so a count that never moved
// cannot pass for one that was cleared.
func TestSubagentsClearWithTheAgent(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	agentAtRest(t, c)

	counted := func(step string) {
		t.Helper()
		res := result(t, reportActivity(t, c, "s1", subagentStart("a1")))
		if n, key := paneSubagents(sess); res["recorded"] != true || n != 1 || key != "1 subagent" {
			t.Fatalf("%s: the start answered %v and the pane says %d %q, want it counted", step, res, n, key)
		}
	}
	cleared := func(step string) {
		t.Helper()
		if n, key := paneSubagents(sess); n != 0 || key != "" || sess.subagentCount(sess.GetState().Windows[0].ID) != 0 {
			t.Fatalf("%s: the pane still says %d %q", step, n, key)
		}
	}

	counted("before the session start")
	result(t, reportActivity(t, c, "s1", map[string]any{"event": "session_start", "text": "resume"}))
	cleared("after the session start")

	counted("before the state went to none")
	setAgentStateVerb(t, c, `{"session":"work","window":"Window","state":"none","harness":"claude-code","agent_session_id":"s1"}`)
	cleared("after the state went to none")

	agentAtRest(t, c)
	counted("before the window closed")
	id := sess.GetState().Windows[0].ID
	if _, err := sess.CloseDaemonWindow(id); err != nil {
		t.Fatal(err)
	}
	if n := sess.subagentCount(id); n != 0 {
		t.Fatalf("the closed window still holds %d subagents", n)
	}
}

// TestActivityReportGuard: report-agent-activity never takes a pane over, so
// a subagent of another conversation or another harness is refused on a pane
// at rest, where a state report from it would be let through. The pane's own
// conversation and harness are counted.
func TestActivityReportGuard(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	agentAtRest(t, c)

	res := result(t, reportActivity(t, c, "s2", subagentStart("nested")))
	if res["reason"] != agentRefusedForeignSession || res["recorded"] != false {
		t.Fatalf("another conversation's subagent: %v, want refused as foreign_session", res)
	}
	res = result(t, callP(c, t, "report-agent-activity", map[string]any{
		"session": "work", "window": "Window", "harness": "codex", "activity": subagentStart("other"),
	}))
	if res["reason"] != agentRefusedForeignHarness || res["recorded"] != false {
		t.Fatalf("another harness's subagent: %v, want refused as foreign_harness", res)
	}
	if n, _ := paneSubagents(sess); n != 0 {
		t.Fatalf("a refused subagent was counted: %d", n)
	}
	res = result(t, reportActivity(t, c, "s1", subagentStart("own")))
	if _, refused := res["reason"]; refused || res["recorded"] != true || res["subagents"] != float64(1) {
		t.Fatalf("the pane's own subagent: %v, want it counted", res)
	}
	if st := sess.GetState().Windows[0]; st.AgentState != AgentStateIdle || st.AgentSessionID != "s1" {
		t.Fatalf("report-agent-activity moved the pane: state %s, session %q", st.AgentState, st.AgentSessionID)
	}
}

// TestQuietSubagentsExpire: a subagent the pane hears nothing more of, a stop
// lost to an interrupt, is dropped subagentQuiet after it was last reported.
// The start arms the prune for that moment, and the prune run then drops it
// and clears the count. Run just before, it drops nothing.
func TestQuietSubagentsExpire(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	agentAtRest(t, c)

	result(t, reportActivity(t, c, "s1", subagentStart("a1")))
	id := sess.GetState().Windows[0].ID
	sess.stateMu.RLock()
	seen := sess.agentSubagents[id]["a1"].seen
	sess.stateMu.RUnlock()
	armed := sess.subagentPrune.due()
	if want := seen + int64(subagentQuiet); armed != want {
		t.Fatalf("the prune is armed for %d, want %d, an hour after the start", armed, want)
	}
	quiet := time.Unix(0, seen).Add(subagentQuiet)
	if n := sess.expireSubagents(quiet.Add(-time.Minute)); n != 0 {
		t.Fatalf("a minute early the prune dropped %d", n)
	}
	if n, _ := paneSubagents(sess); n != 1 {
		t.Fatalf("a minute early the pane says %d subagents, want 1", n)
	}
	if n := sess.expireSubagents(quiet); n != 1 {
		t.Fatalf("at the hour the prune dropped %d, want 1", n)
	}
	if n, key := paneSubagents(sess); n != 0 || key != "" {
		t.Fatalf("after the prune the pane says %d %q", n, key)
	}
}

// TestPaneBucketsHoldTheBurstAndTheRate: a pane may spend the burst at once,
// then one token per 1/rate seconds. Another pane has a bucket of its own.
func TestPaneBucketsHoldTheBurstAndTheRate(t *testing.T) {
	var b paneBuckets
	t0 := time.Unix(1000, 0)
	for i := range int(agentActivityBurst) {
		if !b.take("w", t0, agentActivityRate, agentActivityBurst) {
			t.Fatalf("call %d of the burst was refused", i+1)
		}
	}
	if b.take("w", t0, agentActivityRate, agentActivityBurst) {
		t.Fatal("a call past the burst was let through")
	}
	if !b.take("other", t0, agentActivityRate, agentActivityBurst) {
		t.Fatal("another pane shared the first pane's bucket")
	}
	later := t0.Add(time.Duration(float64(time.Second) / agentActivityRate))
	if !b.take("w", later, agentActivityRate, agentActivityBurst) {
		t.Fatal("a token did not come back at the rate")
	}
	if b.take("w", later, agentActivityRate, agentActivityBurst) {
		t.Fatal("more than one token came back")
	}
}

// TestReportAgentActivityIsRateLimited: past its burst a pane's calls are
// refused with rate_limited, so it cannot push state to every client as fast
// as it can call. The burst itself goes through.
func TestReportAgentActivityIsRateLimited(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	agentAtRest(t, c)
	refused := 0
	for i := range 100 {
		resp := reportActivity(t, c, "s1", map[string]any{"event": "tool", "tool": "Bash", "target": "make " + strconv.Itoa(i)})
		if resp["error"] == nil {
			continue
		}
		mustRefuse(t, resp, ErrVerbRateLimited, "a call past the burst")
		if i < int(agentActivityBurst) {
			t.Fatalf("call %d, inside the burst, was refused", i+1)
		}
		refused++
	}
	if refused == 0 {
		t.Fatal("100 calls at once were all let through")
	}
}

// TestSetAgentStateStillRequiresAState: set-agent-state is the state report,
// and its callers depend on state being required, activity or not. The
// activity goes with report-agent-activity.
func TestSetAgentStateStillRequiresAState(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	c := dialVerb(t, sp)
	mustRefuse(t, callP(c, t, "set-agent-state", map[string]any{
		"session": "work", "window": "Window", "activity": map[string]any{"event": "tool", "tool": "Bash"},
	}), ErrVerbInvalidParams, "set-agent-state with activity and no state")
	mustRefuse(t, callP(c, t, "set-agent-state", map[string]any{
		"session": "work", "window": "Window", "state": "idle", "activity": subagentStart("a1"),
	}), ErrVerbInvalidParams, "set-agent-state with a subagent's activity")
	result(t, callP(c, t, "report-agent-activity", map[string]any{
		"session": "work", "window": "Window", "activity": map[string]any{"event": "tool", "tool": "Bash"},
	}))
}
