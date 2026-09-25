package session

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestIdleGateNeedsThreeConfirmations walks the gate through a working pane's
// idle reading: the first look starts the wait, looks closer together than the
// interval do not count, and the third spaced confirmation publishes.
func TestIdleGateNeedsThreeConfirmations(t *testing.T) {
	var g idleGate
	t0 := time.Now()
	step := func(at time.Duration) (bool, time.Duration) {
		return g.admit("w", AgentStateWorking, t0.Add(at))
	}
	if ok, wait := step(0); ok || wait != idleConfirmInterval {
		t.Fatalf("first look: publish=%v wait=%v, want false and %v", ok, wait, idleConfirmInterval)
	}
	if ok, _ := step(50 * time.Millisecond); ok {
		t.Fatal("a look inside the interval published")
	}
	if ok, _ := step(100 * time.Millisecond); ok {
		t.Fatal("published after one confirmation")
	}
	if ok, _ := step(200 * time.Millisecond); ok {
		t.Fatal("published after two confirmations")
	}
	if ok, _ := step(300 * time.Millisecond); !ok {
		t.Fatal("did not publish after three confirmations")
	}
	if g.pendingFor("w") {
		t.Fatal("a published reading was left pending")
	}
}

// TestIdleGateCapsTheWait checks a reading seen only twice, far apart, still
// publishes once it has stood for the cap.
func TestIdleGateCapsTheWait(t *testing.T) {
	var g idleGate
	t0 := time.Now()
	if ok, _ := g.admit("w", AgentStateWorking, t0); ok {
		t.Fatal("first look published")
	}
	if ok, _ := g.admit("w", AgentStateWorking, t0.Add(idleConfirmCap)); !ok {
		t.Fatal("a reading that stood for the cap was not published")
	}
}

// TestIdleGateCancel checks a look reading something else ends the wait, so
// the next idle reading starts from nothing.
func TestIdleGateCancel(t *testing.T) {
	var g idleGate
	t0 := time.Now()
	g.admit("w", AgentStateWorking, t0)
	g.admit("w", AgentStateWorking, t0.Add(100*time.Millisecond))
	g.admit("w", AgentStateWorking, t0.Add(200*time.Millisecond))
	g.cancel("w")
	if ok, _ := g.admit("w", AgentStateWorking, t0.Add(300*time.Millisecond)); ok {
		t.Fatal("a cancelled wait kept its confirmations")
	}
}

// TestIdleGateOnlyHoldsWorking checks a pane that was not working publishes at
// once: there is no turn to flap out of.
func TestIdleGateOnlyHoldsWorking(t *testing.T) {
	var g idleGate
	for _, st := range []AgentState{AgentStateUnknown, AgentStateNeedsInput, AgentStateDone, AgentStateNone} {
		if ok, _ := g.admit("w", st, time.Now()); !ok {
			t.Errorf("idle over %q was held", st)
		}
	}
}

// TestIdleGateStartupGrace checks nothing is published while a harness is new
// in a pane, and that the gate says when to look again.
func TestIdleGateStartupGrace(t *testing.T) {
	var g idleGate
	t0 := time.Now()
	g.noteHarnessSeen("w", "claude-code", t0)
	ok, wait := g.admit("w", AgentStateUnknown, t0.Add(time.Second))
	if ok {
		t.Fatal("published inside the startup grace")
	}
	if wait != agentStartupGrace-time.Second {
		t.Fatalf("recheck in %v, want %v", wait, agentStartupGrace-time.Second)
	}
	if ok, _ := g.admit("w", AgentStateUnknown, t0.Add(agentStartupGrace)); !ok {
		t.Fatal("did not publish once the grace ended")
	}
	// The same harness seen again does not restart it; a new one does.
	g.noteHarnessSeen("w", "claude-code", t0.Add(agentStartupGrace))
	if ok, _ := g.admit("w", AgentStateUnknown, t0.Add(agentStartupGrace)); !ok {
		t.Fatal("seeing the same harness again restarted the grace")
	}
	g.noteHarnessSeen("w", "codex", t0.Add(agentStartupGrace))
	if ok, _ := g.admit("w", AgentStateUnknown, t0.Add(agentStartupGrace+time.Second)); ok {
		t.Fatal("a new harness in the pane got no grace")
	}
}

// claudeIdleScreen is the measured idle Claude Code screen, trimmed to the rows
// the rules read. See internal/harness/testdata/screens.
const claudeIdleScreen = "  \xe2\x97\x90 medium \xc2\xb7 /effort\r\n" +
	"\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\r\n" +
	"\xe2\x9d\xaf \r\n" +
	"\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\r\n" +
	"  \xe2\x8f\xb5\xe2\x8f\xb5 auto mode on (shift+tab to cycle)\r\n"

// claudeWorkingScreen is the same box under a live turn.
const claudeWorkingScreen = "\xe2\x9c\xbb Thinking\xe2\x80\xa6 (12s)\r\n" +
	"\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\r\n" +
	"\xe2\x9d\xaf \r\n" +
	"\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\xe2\x94\x80\r\n" +
	"  \xe2\x8f\xb5\xe2\x8f\xb5 auto mode on (shift+tab to cycle) \xc2\xb7 esc to interrupt\r\n"

func bundledRegistry(t *testing.T) *harness.Registry {
	t.Helper()
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	return reg
}

// pastStartupGrace marks a harness as seen in a window long enough ago that
// its startup grace is over.
func pastStartupGrace(sess *Session, windowID, harnessID string) {
	sess.idle.noteHarnessSeen(windowID, harnessID, time.Now().Add(-2*agentStartupGrace))
}

// waitForAgentState polls until the window reaches want or the deadline passes.
func waitForAgentState(t *testing.T, sess *Session, windowID string, want AgentState, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if agentStateOf(t, sess, windowID) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return agentStateOf(t, sess, windowID) == want
}

// TestIdleBoxIsConfirmedBeforeItIsPublished is the hysteresis end to end: a
// working Claude Code pane showing its empty prompt box stays working on the
// first look, and the gate's own looks publish idle once the box has held.
func TestIdleBoxIsConfirmedBeforeItIsPublished(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	pastStartupGrace(sess, winID, "claude-code")
	paintPane(t, sess.GetPTY(ptyID), claudeIdleScreen)

	start := time.Now()
	if !sess.scanPaneForAgent(ptyID, reg) {
		t.Fatal("the idle box did not count as a match")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state after one look = %q, want working until confirmed", got)
	}
	if !waitForAgentState(t, sess, winID, AgentStateIdle, 2*time.Second) {
		t.Fatalf("the confirmed box never published idle, state %q", agentStateOf(t, sess, winID))
	}
	if took := time.Since(start); took < 2*idleConfirmInterval {
		t.Fatalf("idle published after %v, sooner than the confirmations allow", took)
	}
	if src := sess.agentClaimFor(winID).source; src != AgentSourceScreen {
		t.Fatalf("idle came from %q, want screen", src)
	}
}

// TestIdleBoxThatFlapsIsNotPublished checks a turn that shows its box for a
// moment and goes back to work never reaches idle.
func TestIdleBoxThatFlapsIsNotPublished(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	pastStartupGrace(sess, winID, "claude-code")
	pty := sess.GetPTY(ptyID)

	paintPane(t, pty, claudeIdleScreen)
	sess.scanPaneForAgent(ptyID, reg)
	paintPane(t, pty, claudeWorkingScreen)
	sess.scanPaneForAgent(ptyID, reg)

	time.Sleep(idleConfirmCap + 200*time.Millisecond)
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state = %q after a flap, want working", got)
	}
	if sess.idle.pendingFor(winID) {
		t.Fatal("the flap left an idle reading pending")
	}
}

// TestIdleWaitsOutTheStartupGrace checks a harness just seen in a pane is not
// called idle at once, and is once the grace has passed.
func TestIdleWaitsOutTheStartupGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the startup grace")
	}
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateUnknown)
	paintPane(t, sess.GetPTY(ptyID), claudeIdleScreen)

	sess.scanPaneForAgent(ptyID, reg)
	time.Sleep(500 * time.Millisecond)
	if got := agentStateOf(t, sess, winID); got != AgentStateUnknown {
		t.Fatalf("state inside the grace = %q, want unknown", got)
	}
	if !waitForAgentState(t, sess, winID, AgentStateIdle, agentStartupGrace+time.Second) {
		t.Fatalf("idle never published after the grace, state %q", agentStateOf(t, sess, winID))
	}
}

// TestIdleYieldsToALouderTier checks the two cross-tier cases: a rest glyph in
// the title does not hide a permission prompt on the screen, and an empty box on
// the screen does not hide a spinner in the title.
func TestIdleYieldsToALouderTier(t *testing.T) {
	reg := bundledRegistry(t)

	t.Run("title at rest, screen blocked", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
		pastStartupGrace(sess, winID, "claude-code")
		pty := sess.GetPTY(ptyID)
		feedVT(t, pty, "\x1b]0;\xe2\x9c\xb3 Claude Code\x07")
		paintPane(t, pty, claudePermissionPrompt)
		sess.scanPaneForAgent(ptyID, reg)
		time.Sleep(idleConfirmCap + 200*time.Millisecond)
		if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
			t.Fatalf("state = %q, want needs_input", got)
		}
	})

	t.Run("title working, screen at rest", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
		pastStartupGrace(sess, winID, "claude-code")
		pty := sess.GetPTY(ptyID)
		feedVT(t, pty, "\x1b]0;\xe2\xa0\x82 Fix the test\x07")
		paintPane(t, pty, claudeIdleScreen)
		sess.scanPaneForAgent(ptyID, reg)
		if sess.idle.pendingFor(winID) {
			t.Fatal("an idle box under a working title started a wait")
		}
		time.Sleep(idleConfirmCap + 200*time.Millisecond)
		if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
			t.Fatalf("state = %q, want working", got)
		}
		if src := sess.agentClaimFor(winID).source; src != AgentSourceOSC {
			t.Fatalf("working came from %q, want the title (osc)", src)
		}
	})
}

// TestSpinnerTitleDoesNotHideAPermissionPrompt is the regression test for a
// working title keeping a prompt from ever showing. A spinner title left on the
// pane re-stamped its working claim on every look, just before the screen's
// needs_input reading asked to override it. The override then always saw a
// claim fresher than the pane's last write, the grace never ran out, and the
// pane stayed working for as long as the prompt stood.
func TestSpinnerTitleDoesNotHideAPermissionPrompt(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	pastStartupGrace(sess, winID, "claude-code")
	pty := sess.GetPTY(ptyID)
	feedVT(t, pty, "\x1b]0;\xe2\xa0\x82 Fix the test\x07")
	pty.lastOutput.Store(time.Now().UnixNano())
	sess.scanPaneForAgent(ptyID, reg)
	if src := sess.agentClaimFor(winID).source; src != AgentSourceOSC {
		t.Fatalf("working came from %q, want the title (osc)", src)
	}

	time.Sleep(20 * time.Millisecond)
	paintPane(t, pty, claudePermissionPrompt)
	deadline := time.Now().Add(agentBlockerOverrideGrace + time.Second)
	for time.Now().Before(deadline) {
		sess.scanPaneForAgent(ptyID, reg)
		if agentStateOf(t, sess, winID) == AgentStateNeedsInput {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("state = %q after %v of looks, want needs_input", agentStateOf(t, sess, winID), agentBlockerOverrideGrace+time.Second)
}

// TestPromptSilencesAWorkingTitleInTheSameLook checks one look that reads a
// spinner in the title and a permission prompt on the screen reports the
// prompt. Applying the title first moved an at-rest pane to working with a fresh
// stamp, and the prompt read a moment later was refused as older than it.
func TestPromptSilencesAWorkingTitleInTheSameLook(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateUnknown)
	pastStartupGrace(sess, winID, "claude-code")
	rest := AgentReport{State: AgentStateIdle, Source: AgentSourceOSC, Harness: "claude-code", paneWroteAt: 1}
	if _, applied, err := sess.ApplyAgentReport(winID, rest); err != nil || !applied {
		t.Fatalf("rest claim: applied=%v err=%v", applied, err)
	}
	pty := sess.GetPTY(ptyID)
	feedVT(t, pty, "\x1b]0;\xe2\xa0\x82 Fix the test\x07")
	paintPane(t, pty, claudePermissionPrompt)
	sess.scanPaneForAgent(ptyID, reg)
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input", got)
	}
}

// TestRepeatedLookKeepsItsStamp checks a look reading back the claim its own
// source already holds changes nothing: no new stamp and no version bump. A
// title that stays on one spinner frame is read on every look, and a restamp
// each time kept its claim looking fresh forever. A report from outside the
// daemon's looks still restamps, since that is a source actively reporting.
func TestRepeatedLookKeepsItsStamp(t *testing.T) {
	sess, winID := bareSessionWithWindow(t)
	look := AgentReport{State: AgentStateWorking, Message: "busy", Source: AgentSourceOSC, Harness: "claude-code", paneWroteAt: 1}
	if _, applied, err := sess.ApplyAgentReport(winID, look); err != nil || !applied {
		t.Fatalf("first look: applied=%v err=%v", applied, err)
	}
	stampOf := func() int64 {
		st := sess.GetState()
		idx, err := findWindowStateIndex(st.Windows, winID)
		if err != nil {
			t.Fatalf("find window: %v", err)
		}
		return st.Windows[idx].AgentStateAt
	}
	before, version := stampOf(), sess.GetState().Version
	time.Sleep(5 * time.Millisecond)
	state, applied, err := sess.ApplyAgentReport(winID, look)
	if err != nil || applied || state != AgentStateWorking {
		t.Fatalf("repeat look: state=%q applied=%v err=%v, want working, false, nil", state, applied, err)
	}
	if got := stampOf(); got != before {
		t.Fatalf("repeat look restamped the state: %d, was %d", got, before)
	}
	if got := sess.GetState().Version; got != version {
		t.Fatalf("repeat look bumped the version: %d, was %d", got, version)
	}

	look.Message = "still busy"
	if _, applied, _ := sess.ApplyAgentReport(winID, look); !applied {
		t.Fatal("a new message from the same look was not applied")
	}

	before = stampOf()
	time.Sleep(5 * time.Millisecond)
	report := AgentReport{State: AgentStateWorking, Message: "still busy", Source: AgentSourceOSC, Harness: "claude-code"}
	if _, applied, _ := sess.ApplyAgentReport(winID, report); !applied {
		t.Fatal("a repeated report from outside the looks was not applied")
	}
	if got := stampOf(); got == before {
		t.Fatal("a repeated report from outside the looks did not restamp the state")
	}
}

// TestStallTimerIgnoresALeftoverSpinnerTitle checks a working title on a pane
// that has been silent for the whole stall window does not keep the pane
// working. A spinner writes a new frame each time it turns, so one that has
// not turned is a frame left behind.
func TestStallTimerIgnoresALeftoverSpinnerTitle(t *testing.T) {
	reg := bundledRegistry(t)
	const stall = 30 * time.Second
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	pastStartupGrace(sess, winID, "claude-code")
	pty := sess.GetPTY(ptyID)
	paintPane(t, pty, "\x1b]0;\xe2\xa0\x82 Fix the test\x07")
	sess.scanPaneForAgent(ptyID, reg)
	if src := sess.agentClaimFor(winID).source; src != AgentSourceOSC {
		t.Fatalf("working came from %q, want the title (osc)", src)
	}

	quiet := func(string) int64 { return 0 }
	look := func(id string) bool { return sess.scanStalledPane(id, reg) }
	if n := sess.applyStallHeuristic(time.Now().Add(stall+time.Second), stall, quiet, look); n != 1 {
		t.Fatalf("demoted %d panes, want 1", n)
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateUnknown {
		t.Fatalf("state = %q, want unknown", got)
	}
}

// TestIdleUnderAStrongerClaimDoesNotHoldOffTheTimer checks an idle box on a
// pane a hook reported working for is not counted as an answer: the rule's
// claim would be refused, and the silence timer is what retires a hook that
// stopped reporting.
func TestIdleUnderAStrongerClaimDoesNotHoldOffTheTimer(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateWorking, Harness: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	pastStartupGrace(sess, winID, "claude-code")
	paintPane(t, sess.GetPTY(ptyID), claudeIdleScreen)
	if sess.scanPaneForAgent(ptyID, reg) {
		t.Fatal("an idle box a report outranks counted as an answer")
	}
	if sess.idle.pendingFor(winID) {
		t.Fatal("an idle box a report outranks started a wait")
	}
}

// TestScreenIdleIsGivenBackWhenTheBoxLeaves checks the screen tier stops
// defending an idle it took once no rule matches: the box that proved rest is
// gone.
func TestScreenIdleIsGivenBackWhenTheBoxLeaves(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateUnknown)
	pastStartupGrace(sess, winID, "claude-code")
	pty := sess.GetPTY(ptyID)
	paintPane(t, pty, claudeIdleScreen)
	sess.scanPaneForAgent(ptyID, reg)
	if got := agentStateOf(t, sess, winID); got != AgentStateIdle {
		t.Fatalf("state = %q, want idle", got)
	}
	if _, held := sess.agentClaimHeld(winID); !held {
		t.Fatal("the screen took no claim")
	}
	paintPane(t, pty, "plain text with no box\r\n")
	sess.scanPaneForAgent(ptyID, reg)
	if c, held := sess.agentClaimHeld(winID); held && c.source == AgentSourceScreen {
		t.Fatal("the screen kept defending idle with no box on the screen")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateIdle {
		t.Fatalf("state = %q, want idle left in place", got)
	}
}

// TestIdleBoxLookedAtAgainIsNotRepublished checks a pane already idle on the
// screen's word is left alone by the next look: typing into the prompt box
// makes output, and republishing on each look would push state to every
// client per keystroke and restamp how long the pane has been idle.
func TestIdleBoxLookedAtAgainIsNotRepublished(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateUnknown)
	pastStartupGrace(sess, winID, "claude-code")
	paintPane(t, sess.GetPTY(ptyID), claudeIdleScreen)
	sess.scanPaneForAgent(ptyID, reg)
	if got := agentStateOf(t, sess, winID); got != AgentStateIdle {
		t.Fatalf("state = %q, want idle", got)
	}
	before := sess.GetState()
	sess.scanPaneForAgent(ptyID, reg)
	after := sess.GetState()
	if after.Version != before.Version {
		t.Fatalf("a second look at an idle pane bumped the version %d -> %d", before.Version, after.Version)
	}
	if windowStateOf(t, sess, winID).AgentStateAt != before.Windows[0].AgentStateAt {
		t.Fatal("a second look restamped the idle state")
	}
}

// TestAVisibleBlockerTakesARestClaimAtOnce checks the one change to the
// override grace: a claim saying the agent is at rest gives way to a prompt
// painted after it without the two-second wait, since the settle look that
// sees the prompt runs well inside it.
func TestAVisibleBlockerTakesARestClaimAtOnce(t *testing.T) {
	reg := bundledRegistry(t)
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{State: AgentStateIdle, Source: AgentSourceOSC}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	paintPane(t, sess.GetPTY(ptyID), claudePermissionPrompt)
	sess.scanScreenForAgent(ptyID, reg)
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input over a rest claim", got)
	}
}
