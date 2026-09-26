package session

import (
	"reflect"
	"testing"
	"time"
)

// bareSessionWithWindow builds a session with one window but no daemon socket, so
// a session can be exercised directly without any network.
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

// TestAClientSyncKeepsTheDaemonsWindowFields: no client sets these fields, so
// a client push that omits them must not wipe them. The daemon carries each
// over from its own state by window id.
func TestAClientSyncKeepsTheDaemonsWindowFields(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		canonical, incoming, want WindowState
	}{
		{
			name:      "agent state and message",
			canonical: WindowState{ID: "w1", AgentState: AgentStateWorking, AgentMessage: "building", AgentStateAt: 5},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1", AgentState: AgentStateWorking, AgentMessage: "building", AgentStateAt: 5},
		},
		{
			name:      "agent kind and session id",
			canonical: WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentKind: "approval", AgentSessionID: "s1"},
			incoming:  WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentMessage: "x"},
			want:      WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentKind: "approval", AgentSessionID: "s1"},
		},
		{
			// A push echoing a state the daemon has since moved on from. The
			// agent fields used to be carried only when the push left them all
			// empty, so this put working back over needs_input.
			name:      "a stale agent state in the push",
			canonical: WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentMessage: "approve?", AgentHarness: "claude-code", AgentStateAt: 9},
			incoming:  WindowState{ID: "w1", AgentState: AgentStateWorking, AgentMessage: "building", AgentHarness: "claude-code", AgentStateAt: 5},
			want:      WindowState{ID: "w1", AgentState: AgentStateNeedsInput, AgentMessage: "approve?", AgentHarness: "claude-code", AgentStateAt: 9},
		},
		{
			name:      "a stale foreground command, pid and directory in the push",
			canonical: WindowState{ID: "w1", ForegroundCmd: "", ShellPID: 42, Cwd: "/now"},
			incoming:  WindowState{ID: "w1", ForegroundCmd: "nvim", ShellPID: 41, Cwd: "/then"},
			want:      WindowState{ID: "w1", ShellPID: 42, Cwd: "/now"},
		},
		{
			name:      "finished turn count",
			canonical: WindowState{ID: "w1", CompletionSeq: 1},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1", CompletionSeq: 1},
		},
		{
			name:      "foreground command",
			canonical: WindowState{ID: "w1", ForegroundCmd: "nvim"},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1", ForegroundCmd: "nvim"},
		},
		{
			name:      "exited foreground command",
			canonical: WindowState{ID: "w1"},
			incoming:  WindowState{ID: "w1"},
			want:      WindowState{ID: "w1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming := &SessionState{Windows: []WindowState{tc.incoming}}
			retainDaemonExclusive(incoming, &SessionState{Windows: []WindowState{tc.canonical}})
			if got := incoming.Windows[0]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("after a client sync:\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
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
