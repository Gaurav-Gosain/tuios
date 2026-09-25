package session

import (
	"testing"
	"time"
)

// TestSetAgentStateRejectsBadState checks the verb validates the state name.
func TestSetAgentStateRejectsBadState(t *testing.T) {
	_, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	// A session with a window so the failure is the state, not the target.
	// (startTestDaemon holds no sessions; create one over the manager is not
	// reachable here, so a bogus state must fail before session resolution.)
	code := errCode(t, c.call(t, `{"id":1,"verb":"set-agent-state","params":{"session":"work","state":"bogus"}}`))
	if code != ErrVerbInvalidParams {
		t.Fatalf("bad state gave code %q, want %q", code, ErrVerbInvalidParams)
	}

	missing := errCode(t, c.call(t, `{"id":2,"verb":"set-agent-state","params":{"session":"work"}}`))
	if missing != ErrVerbInvalidParams {
		t.Fatalf("missing state gave code %q, want %q", missing, ErrVerbInvalidParams)
	}
}

// bareSessionWithWindow builds a session with one window but no daemon socket, so
// the stall heuristic can be exercised directly without any network.
func bareSessionWithWindow(t *testing.T) (*Session, string) {
	t.Helper()
	t.Cleanup(useResurrectionDir(t.TempDir()))
	sess, err := NewSession("stall", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	win, err := sess.AddDaemonWindow("Window", nil)
	if err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess, win.ID
}

func agentStateOf(t *testing.T, sess *Session, windowID string) AgentState {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.AgentState
		}
	}
	t.Fatalf("window %s not found", windowID)
	return AgentStateNone
}

// TestStallHeuristicDemotesQuietWorking checks a pane that reported working but
// has gone quiet is demoted to idle only after the silence window elapses.
func TestStallHeuristicDemotesQuietWorking(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	quiet := func(string) int64 { return 0 } // no output ever

	const stall = 30 * time.Second

	// Well within the window: no demotion.
	now := time.Now()
	if n := sess.applyStallHeuristic(now.Add(10*time.Second), stall, quiet, nil); n != 0 {
		t.Fatalf("demoted %d windows inside the stall window, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state inside window = %q, want working", got)
	}

	// Past the window: demoted to idle.
	if n := sess.applyStallHeuristic(now.Add(stall+time.Second), stall, quiet, nil); n != 1 {
		t.Fatalf("demoted %d windows past the stall window, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state past window = %q, want idle", got)
	}
}

// TestStallHeuristicRespectsRecentOutput checks a working pane that keeps
// producing output is never demoted, however long it works.
func TestStallHeuristicRespectsRecentOutput(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	const stall = 30 * time.Second
	// Evaluate well past the original working report, but with output that arrived
	// only a second before this evaluation: a busy pane keeps emitting, so its
	// last output stays fresh relative to now even though it has worked a while.
	evalAt := time.Now().Add(5 * stall)
	recent := func(string) int64 { return evalAt.Add(-time.Second).UnixNano() }
	if n := sess.applyStallHeuristic(evalAt, stall, recent, nil); n != 0 {
		t.Fatalf("demoted %d windows with recent output, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state with recent output = %q, want working", got)
	}
}

// TestStallHeuristicNeverOverridesExplicit checks the heuristic only ever moves a
// pane out of working: an explicit needs_input (or any non-working state) is left
// alone no matter how long it sits.
func TestStallHeuristicNeverOverridesExplicit(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	quiet := func(string) int64 { return 0 }
	const stall = 30 * time.Second
	long := time.Now().Add(time.Hour)

	for _, state := range []AgentState{AgentStateNeedsInput, AgentStateDone, AgentStateErrored, AgentStateIdle} {
		if err := sess.SetDaemonWindowAgentState(id, state, ""); err != nil {
			t.Fatalf("SetDaemonWindowAgentState(%q): %v", state, err)
		}
		if n := sess.applyStallHeuristic(long, stall, quiet, nil); n != 0 {
			t.Fatalf("heuristic changed %d windows in state %q, want 0", n, state)
		}
		if got := agentStateOf(t, sess, id); got != state {
			t.Fatalf("state %q was overridden to %q by the heuristic", state, got)
		}
	}
}

// TestStallHeuristicDisabled checks a non-positive stall disables the heuristic.
func TestStallHeuristicDisabled(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	quiet := func(string) int64 { return 0 }
	if n := sess.applyStallHeuristic(time.Now().Add(time.Hour), 0, quiet, nil); n != 0 {
		t.Fatalf("disabled heuristic demoted %d windows, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state with heuristic disabled = %q, want working", got)
	}
}

// TestResolveAgentStallTimeout checks the config/env/default precedence.
func TestResolveAgentStallTimeout(t *testing.T) {
	t.Setenv("TUIOS_AGENT_STALL_SECONDS", "")
	if got := resolveAgentStallTimeout(5 * time.Second); got != 5*time.Second {
		t.Errorf("explicit config: got %v, want 5s", got)
	}
	if got := resolveAgentStallTimeout(-1); got != 0 {
		t.Errorf("negative config should disable: got %v, want 0", got)
	}
	if got := resolveAgentStallTimeout(0); got != defaultAgentStallTimeout {
		t.Errorf("zero config with no env: got %v, want default %v", got, defaultAgentStallTimeout)
	}
	t.Setenv("TUIOS_AGENT_STALL_SECONDS", "12")
	if got := resolveAgentStallTimeout(0); got != 12*time.Second {
		t.Errorf("env override: got %v, want 12s", got)
	}
	t.Setenv("TUIOS_AGENT_STALL_SECONDS", "0")
	if got := resolveAgentStallTimeout(0); got != 0 {
		t.Errorf("env 0 should disable: got %v, want 0", got)
	}
}

// TestAgentStateRetainedAcrossClientSync checks a client state sync that omits
// agent fields does not wipe them: the daemon carries them over by window id.
func TestAgentStateRetainedAcrossClientSync(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	if err := sess.SetDaemonWindowAgentState(id, AgentStateWorking, "building"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	// A client sync built from the daemon state but with the agent fields cleared,
	// exactly as a client (which never sets them) would send.
	incoming := sess.GetState()
	incoming.BaseVersion = incoming.Version
	for i := range incoming.Windows {
		incoming.Windows[i].AgentState = AgentStateNone
		incoming.Windows[i].AgentMessage = ""
		incoming.Windows[i].AgentStateAt = 0
	}
	sess.UpdateState(incoming)

	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("agent state after client sync = %q, want working (should be retained)", got)
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == id && w.AgentMessage != "building" {
			t.Fatalf("agent message after client sync = %q, want building", w.AgentMessage)
		}
	}
}
