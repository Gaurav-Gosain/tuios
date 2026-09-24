package session

import (
	"fmt"
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

// agentPaneWithWindow is bareSessionWithWindow with a harness named on the
// window, which is what an OSC 9;4 report needs before it may drive agent
// state (see progressTarget). No state is set: the pane is attributed to an
// agent and the agent has not said anything yet.
func agentPaneWithWindow(t *testing.T) (*Session, string) {
	t.Helper()
	sess, id := bareSessionWithWindow(t)
	attributeHarness(t, sess, id, "claude-code")
	return sess, id
}

// attributeHarness names a harness on a window without setting a state.
func attributeHarness(t *testing.T, sess *Session, windowID, harnessID string) {
	t.Helper()
	err := sess.mutateState(func(st *SessionState) error {
		for i := range st.Windows {
			if st.Windows[i].ID == windowID {
				st.Windows[i].AgentHarness = harnessID
				return nil
			}
		}
		return fmt.Errorf("window %s not found", windowID)
	})
	if err != nil {
		t.Fatalf("attribute harness: %v", err)
	}
}

// TestAgentProgressDrivesState proves an OSC 9;4 report moves an agent's pane
// through working, needs_input and idle, which is the whole point of reading
// the sequence: it is a state feed the harness emits about itself.
func TestAgentProgressDrivesState(t *testing.T) {
	sess, id := agentPaneWithWindow(t)
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
	if n := sess.applyAgentDetection(running, agent.identifyDetail); n != 1 {
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

// TestPlainPaneProgressIsNotAnAgent is the regression test for a progress bar
// making an agent out of a shell. A package manager or a build tool that draws
// its bar with OSC 9;4 turned a plain pane into an agent: a state mark on the
// rail, a silence timer, and an Inbox entry when the bar went to its warning
// state. The sequence says a program is busy, not that an agent is there.
func TestPlainPaneProgressIsNotAnAgent(t *testing.T) {
	sess, id := bareSessionWithWindow(t)
	now := time.Now()
	for _, state := range []vt.ProgressState{
		vt.ProgressIndeterminate, vt.ProgressNormal, vt.ProgressWarning, vt.ProgressError, vt.ProgressClear,
	} {
		sess.applyAgentProgressAt(id, state, now)
		if got := agentStateOf(t, sess, id); got != AgentStateNone {
			t.Fatalf("a plain pane after progress state %d has agent state %q, want none", state, got)
		}
	}
	if n := sess.settleAgentHolds(now.Add(time.Hour)); n != 0 {
		t.Fatalf("settle published %d states for a plain pane, want 0", n)
	}
	if got := agentStateOf(t, sess, id); got != AgentStateNone {
		t.Fatalf("a plain pane after settling has agent state %q, want none", got)
	}
}

// TestPlainPaneProgressReachesNoInbox runs the same case through the daemon,
// the way a pane's output reaches it: the emulator parks the report and the
// output handler applies it. A plain pane gets no agent state and nothing in
// the Inbox, while the same report on a pane attributed to a harness still
// reaches both, so the gate is about the pane and not about the sequence.
func TestPlainPaneProgressReachesNoInbox(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, plain, agent := twoWindowSession(t, d, "bars")
	c := dialVerb(t, sp)
	attributeHarness(t, sess, agent, "claude-code")

	feedProgress := func(windowID string) {
		t.Helper()
		ptyID := ptyIDOfWindow(t, sess, windowID)
		pty := sess.GetPTY(ptyID)
		feedVT(t, pty, "\x1b]9;4;4;30\x07")
		state, ok := pty.takeAgentProgress()
		if !ok {
			t.Fatal("the emulator parked no progress report")
		}
		sess.applyPaneProgress(ptyID, windowID, state, d.agentMatcher.registry)
	}

	feedProgress(plain)
	feedProgress(agent)

	// The attributed pane is the positive control: once its item is in the
	// Inbox, the plain pane has had the same chance to put one there.
	items := waitAttention(t, c, "the attributed pane's warning", hasItemFor(agent))
	for _, it := range items {
		if it["window"] == plain {
			t.Fatalf("a plain pane's progress bar put %v in the Inbox", it)
		}
	}
	if got := agentStateOf(t, sess, plain); got != AgentStateNone {
		t.Fatalf("a plain pane's progress bar gave it agent state %q, want none", got)
	}
	if got := agentStateOf(t, sess, agent); got != AgentStateNeedsInput {
		t.Fatalf("an attributed pane's warning = %q, want needs_input", got)
	}
	// The report is still readable on the plain pane, for anything that shows
	// progress as progress.
	if got := sess.GetPTY(ptyIDOfWindow(t, sess, plain)).ProgressText(); got != "4;4;30" {
		t.Fatalf("the plain pane's progress text = %q, want 4;4;30", got)
	}
}

// hasItemFor holds when any Inbox item is about the window.
func hasItemFor(window string) func([]map[string]any) bool {
	return func(items []map[string]any) bool {
		for _, it := range items {
			if it["window"] == window {
				return true
			}
		}
		return false
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
	p.storeAgentProgress(vt.ProgressClear, 0)
	if state, ok := p.takeAgentProgress(); !ok || state != vt.ProgressClear {
		t.Fatalf("parked clear came back as (%d, %v), want (0, true)", state, ok)
	}
	if _, ok := p.takeAgentProgress(); ok {
		t.Fatal("a taken state was returned twice")
	}

	p.storeAgentProgress(vt.ProgressNormal, 40)
	p.storeAgentProgress(vt.ProgressError, 55)
	if state, ok := p.takeAgentProgress(); !ok || state != vt.ProgressError {
		t.Fatalf("a burst came back as (%d, %v), want the newest (error, true)", state, ok)
	}
}

// The last report stays readable after it is taken, written the way an
// osc_progress rule reads it.
func TestPTYProgressText(t *testing.T) {
	p := &PTY{}
	if got := p.ProgressText(); got != "" {
		t.Fatalf("a pane that sent no report has progress text %q", got)
	}
	for _, tc := range []struct {
		state   vt.ProgressState
		percent int
		want    string
	}{
		{vt.ProgressClear, 0, "4;0"},
		{vt.ProgressNormal, 40, "4;1;40"},
		{vt.ProgressError, 55, "4;2;55"},
		{vt.ProgressIndeterminate, 0, "4;3"},
		{vt.ProgressWarning, 7, "4;4;7"},
	} {
		p.storeAgentProgress(tc.state, tc.percent)
		p.takeAgentProgress()
		if got := p.ProgressText(); got != tc.want {
			t.Errorf("state %d percent %d: progress text %q, want %q", tc.state, tc.percent, got, tc.want)
		}
	}
}
