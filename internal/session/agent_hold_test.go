package session

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestAgentLoudness pins the ordering the whole anti-flicker policy rests on: the
// states that want a human outrank working, which outranks the quiet ones.
func TestAgentLoudness(t *testing.T) {
	if !(agentLoudness(AgentStateNeedsInput) > agentLoudness(AgentStateWorking)) {
		t.Error("needs_input must be louder than working")
	}
	if !(agentLoudness(AgentStateErrored) > agentLoudness(AgentStateWorking)) {
		t.Error("errored must be louder than working")
	}
	if !(agentLoudness(AgentStateWorking) > agentLoudness(AgentStateIdle)) {
		t.Error("working must be louder than idle")
	}
	if !(agentLoudness(AgentStateIdle) > agentLoudness(AgentStateNone)) {
		t.Error("idle must be louder than none")
	}
	if agentLoudness(AgentStateDone) != agentLoudness(AgentStateIdle) {
		t.Error("done and idle are equally quiet")
	}
}

// TestAgentHoldCollapsesAFlap is the anti-flicker guarantee: a source that says
// something quieter and then takes it back inside the hold window produces one
// transition, not two. The pane never visits the state that was withdrawn.
func TestAgentHoldCollapsesAFlap(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	now := time.Now()

	// The agent starts working. Louder than none, so it is published at once.
	sess.applyAgentProgressAt(id, vt.ProgressIndeterminate, now)
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after the first progress report = %q, want working", got)
	}
	versionAfterWorking := sess.GetState().Version

	// It clears the bar between two steps of the same task. Quieter, so it waits.
	sess.applyAgentProgressAt(id, vt.ProgressClear, now.Add(50*time.Millisecond))
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state during the hold = %q, want working (the quiet state is held)", got)
	}

	// It sets the bar again well inside the window: the hold is cancelled and
	// nothing was ever published, so the pane never blinked through idle.
	sess.applyAgentProgressAt(id, vt.ProgressNormal, now.Add(120*time.Millisecond))
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after the flap = %q, want working", got)
	}
	if got := sess.GetState().Version; got != versionAfterWorking {
		t.Fatalf("a flap bumped the version from %d to %d, want one transition only",
			versionAfterWorking, got)
	}

	// Nothing is left waiting to be published.
	if n := sess.settleAgentHolds(now.Add(time.Hour)); n != 0 {
		t.Fatalf("settle published %d states after a cancelled flap, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after settling a cancelled flap = %q, want working", got)
	}
}

// TestAgentHoldPublishesAStateThatStands checks the hold is a delay and not a
// veto: a quieter state that keeps being true is published once its window has
// elapsed.
func TestAgentHoldPublishesAStateThatStands(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	now := time.Now()

	sess.applyAgentProgressAt(id, vt.ProgressIndeterminate, now)
	sess.applyAgentProgressAt(id, vt.ProgressClear, now)
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state at the start of the hold = %q, want working", got)
	}

	// The same quiet state, still true after the window: published.
	sess.applyAgentProgressAt(id, vt.ProgressClear, now.Add(agentHoldWindow+time.Millisecond))
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after the hold elapsed = %q, want idle", got)
	}
}

// TestAgentHoldSettlesWhenTheSourceGoesSilent checks the backstop: a harness that
// clears its progress bar once and then says nothing still gets its state
// published rather than held forever.
func TestAgentHoldSettlesWhenTheSourceGoesSilent(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	now := time.Now()

	sess.applyAgentProgressAt(id, vt.ProgressIndeterminate, now)
	sess.applyAgentProgressAt(id, vt.ProgressClear, now)
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state during the hold = %q, want working", got)
	}

	// Too early: the window has not elapsed.
	if n := sess.settleAgentHolds(now.Add(100 * time.Millisecond)); n != 0 {
		t.Fatalf("settle published %d states inside the window, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state after an early settle = %q, want working", got)
	}

	// Past the window, with no further word from the harness.
	if n := sess.settleAgentHolds(now.Add(agentHoldWindow + time.Millisecond)); n != 1 {
		t.Fatalf("settle published %d states past the window, want 1", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after settling = %q, want idle", got)
	}
	// The hold is consumed, not repeated.
	if n := sess.settleAgentHolds(now.Add(time.Hour)); n != 0 {
		t.Fatalf("settle republished %d states, want 0", n)
	}
}

// TestAgentHoldPublishesItselfWithNoOneToCallIt is the regression test for a
// hold that could wait forever.
//
// settleAgentHolds had exactly one caller, the stall monitor, which returns
// immediately when the silence timer is disabled. So a user who turned the
// silence timer off also turned off the thing that publishes a held state, and
// a harness that cleared its progress bar once and then went quiet left its pane
// reading working until something unrelated happened to it. The hold now carries
// its own timer, which no other setting can switch off.
//
// It uses real time on purpose: the defect was the absence of a caller, and a
// test that passes the clock in cannot see that.
func TestAgentHoldPublishesItselfWithNoOneToCallIt(t *testing.T) {
	sess, id := bareSessionWithWindow(t)

	sess.applyAgentProgress(id, vt.ProgressIndeterminate)
	sess.applyAgentProgress(id, vt.ProgressClear)
	if got := agentStateOf(t, sess, id); got != AgentStateWorking {
		t.Fatalf("state during the hold = %q, want working", got)
	}

	// Nothing else touches the session: no stall monitor, no further progress
	// report, no client. The hold has to publish itself or never publish at all.
	deadline := time.Now().Add(5 * time.Second)
	for agentStateOf(t, sess, id) != AgentStateIdle {
		if time.Now().After(deadline) {
			t.Fatal("the held idle state was never published; the hold has no backstop of its own")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAgentHoldNeverDelaysAStateThatWantsAHuman is the asymmetry: needs_input and
// errored are published the instant they are seen, however quiet the pane was,
// because a late "the agent is waiting on you" is the expensive mistake.
func TestAgentHoldNeverDelaysAStateThatWantsAHuman(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	now := time.Now()

	sess.applyAgentProgressAt(id, vt.ProgressClear, now)
	if got := agentStateOf(t, sess, id); got != AgentStateIdle {
		t.Fatalf("state after clear from none = %q, want idle (louder than none)", got)
	}

	sess.applyAgentProgressAt(id, vt.ProgressWarning, now)
	if got := agentStateOf(t, sess, id); got != AgentStateNeedsInput {
		t.Fatalf("needs_input was delayed: state = %q, want needs_input at once", got)
	}

	sess.applyAgentProgressAt(id, vt.ProgressError, now)
	if got := agentStateOf(t, sess, id); got != AgentStateErrored {
		t.Fatalf("errored was delayed: state = %q, want errored at once", got)
	}
}
