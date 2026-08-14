package session

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestAgentStateForProgress checks the OSC 9;4 state mapping, including that an
// out-of-range state is refused rather than guessed at.
func TestAgentStateForProgress(t *testing.T) {
	cases := []struct {
		in    vt.ProgressState
		state AgentState
		ok    bool
	}{
		{vt.ProgressClear, AgentStateIdle, true},
		{vt.ProgressNormal, AgentStateWorking, true},
		{vt.ProgressIndeterminate, AgentStateWorking, true},
		{vt.ProgressError, AgentStateErrored, true},
		{vt.ProgressWarning, AgentStateNeedsInput, true},
		{vt.ProgressState(9), AgentStateNone, false},
	}
	for _, c := range cases {
		state, ok := agentStateForProgress(c.in)
		if state != c.state || ok != c.ok {
			t.Errorf("agentStateForProgress(%d) = (%q, %v), want (%q, %v)", c.in, state, ok, c.state, c.ok)
		}
	}
}

// TestAgentProgressDrivesState proves an OSC 9;4 report moves a pane through
// working, needs_input and idle, which is the whole point of reading the
// sequence: it is a state feed the harness emits about itself.
func TestAgentProgressDrivesState(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	now := time.Now()

	sess.applyAgentProgressAt(id, vt.ProgressIndeterminate, now)
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after an indeterminate progress report = %q, want working", got)
	}

	sess.applyAgentProgressAt(id, vt.ProgressWarning, now)
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("state after a warning progress report = %q, want needs_input", got)
	}

	// Clearing is quieter than needs_input, so it lands once it has stood for the
	// anti-flicker window rather than the instant it arrives.
	sess.applyAgentProgressAt(id, vt.ProgressClear, now)
	sess.applyAgentProgressAt(id, vt.ProgressClear, now.Add(agentHoldWindow+time.Millisecond))
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after a cleared progress report = %q, want idle", got)
	}

	// A state the sequence cannot name leaves the pane where it was.
	sess.applyAgentProgressAt(id, vt.ProgressState(9), now)
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after an unknown progress state = %q, want idle (unchanged)", got)
	}
}

// TestAgentProgressOutranksDetectorAndYieldsToReport pins the sequence's place in
// the ranking: it takes a pane the foreground-process detector claimed, and it
// cannot touch one the harness is reporting for itself.
func TestAgentProgressOutranksDetectorAndYieldsToReport(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, id)
	agent := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{comm: "claude", argv: []string{"claude"}}, true}})

	// The detector owns the pane; a progress report outranks it.
	if n := sess.applyAgentDetection(running, agent.identify); n != 1 {
		t.Fatalf("promotion changed %d windows, want 1", n)
	}
	sess.applyAgentProgress(id, vt.ProgressWarning)
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("progress over a detected pane = %q, want needs_input", got)
	}

	// The harness then reports for itself, which outranks the sequence.
	if err := sess.SetDaemonWindowAgentState(id, AgentStateDone, "finished"); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	sess.applyAgentProgress(id, vt.ProgressIndeterminate)
	if got := agentStateOf(t, sess, id); got != AgentStateDone {
		t.Fatalf("progress over a reported pane = %q, want done (unchanged)", got)
	}
}

// TestPTYProgressParking checks the hand-off the VT callback uses: a parked state
// is returned once and collapses a burst to the newest value.
func TestPTYProgressParking(t *testing.T) {
	p := &PTY{}
	if _, ok := p.takeAgentProgress(); ok {
		t.Fatal("takeAgentProgress reported a state with none parked")
	}

	// Clear is state 0, so it has to survive the "nothing parked" encoding.
	p.storeAgentProgress(vt.ProgressClear)
	if state, ok := p.takeAgentProgress(); !ok || state != vt.ProgressClear {
		t.Fatalf("parked clear came back as (%d, %v), want (0, true)", state, ok)
	}
	if _, ok := p.takeAgentProgress(); ok {
		t.Fatal("a taken state was returned twice")
	}

	p.storeAgentProgress(vt.ProgressNormal)
	p.storeAgentProgress(vt.ProgressError)
	if state, ok := p.takeAgentProgress(); !ok || state != vt.ProgressError {
		t.Fatalf("a burst came back as (%d, %v), want the newest (error, true)", state, ok)
	}
}
