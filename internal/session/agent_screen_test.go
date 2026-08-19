package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// claudePermissionPrompt is what Claude Code paints and then goes silent behind.
// It is the exact shape the bundled manifest's first rule keys on.
const claudePermissionPrompt = "Do you want to proceed?\r\n" +
	"\xe2\x9d\xaf 1. Yes\r\n" +
	"  2. Yes, and don't ask again\r\n" +
	"  3. No, and tell Claude what to do differently (esc)\r\n"

// agentPaneWithHarness returns a session, its window id and its PTY id, with the
// window already attributed to harnessID so the screen tier has rules to run.
//
// The claim is AgentSourceDetect because that is what an unhooked harness gets:
// the foreground-process detector saw the binary and said working, which is the
// only thing it can honestly say. That is the case the screen tier is for.
func agentPaneWithHarness(t *testing.T, harnessID string, state AgentState) (*Session, string, string) {
	t.Helper()
	sess, winID := bareSessionWithWindow(t)
	report := AgentReport{State: state, Source: AgentSourceDetect, Harness: harnessID}
	if _, _, err := sess.ApplyAgentReport(winID, report); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	ids := sess.ListPTYIDs()
	if len(ids) != 1 {
		t.Fatalf("session has %d PTYs, want 1", len(ids))
	}
	return sess, winID, ids[0]
}

// TestStallTimerLooksAtTheScreenBeforeCallingAPaneIdle is the regression test
// for the worst thing the silence timer did: print idle over a pane that is
// blocked on a human.
//
// A harness waiting for an answer paints the question and then emits nothing,
// which is the same silence a harness that finished produces. The timer read
// that silence as "finished", wrote idle, and idle is the one state the alert
// policy deliberately ignores, so the pane went quiet in every sense at exactly
// the moment someone needed to be told.
//
// The nil-look half is the old behaviour, kept in the same test so the
// difference is the assertion rather than the shape of the call.
func TestStallTimerLooksAtTheScreenBeforeCallingAPaneIdle(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}

	const stall = 30 * time.Second
	quiet := func(string) int64 { return 0 } // the pane never wrote anything
	past := time.Now().Add(stall + time.Second)

	t.Run("without a look the blocked pane is called idle", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
		feedVT(t, sess.GetPTY(ptyID), claudePermissionPrompt)

		if n := sess.applyStallHeuristic(past, stall, quiet, nil); n != 1 {
			t.Fatalf("demoted %d panes, want 1", n)
		}
		if got := agentStateOf(t, sess, winID); got != AgentStateIdle {
			t.Fatalf("state = %q, want idle", got)
		}
	})

	t.Run("with a look it stays blocked", func(t *testing.T) {
		sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
		feedVT(t, sess.GetPTY(ptyID), claudePermissionPrompt)

		look := func(id string) bool { return sess.scanScreenForAgent(id, reg) }
		if n := sess.applyStallHeuristic(past, stall, quiet, look); n != 0 {
			t.Fatalf("demoted %d panes that are visibly waiting on a human, want 0", n)
		}
		if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
			t.Fatalf("state = %q, want needs_input", got)
		}
	})
}

// paintPane writes to the pane's emulator and records that the pane wrote, which
// is one event in the daemon and two calls here because the test bypasses the
// read loop that would otherwise do both.
func paintPane(t *testing.T, p *PTY, data string) {
	t.Helper()
	feedVT(t, p, data)
	p.lastOutput.Store(time.Now().UnixNano())
}

// backdateAgentClaim moves a window's claim timestamp into the past, standing in
// for the seconds a harness spends working between reporting that it started and
// stopping on a prompt.
func backdateAgentClaim(t *testing.T, sess *Session, windowID string, by time.Duration) {
	t.Helper()
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	for i := range sess.state.Windows {
		if sess.state.Windows[i].ID == windowID {
			sess.state.Windows[i].AgentStateAt = time.Now().Add(-by).UnixNano()
			return
		}
	}
	t.Fatalf("window %s not found", windowID)
}

// blockedPaneClaimedByItsHarness is the defect's setup: a pane whose harness
// reported working for itself at report rank, then stopped and painted a
// permission prompt without saying anything further.
func blockedPaneClaimedByItsHarness(t *testing.T) (*Session, string, string) {
	t.Helper()
	sess, winID := bareSessionWithWindow(t)
	report := AgentReport{State: AgentStateWorking, Source: AgentSourceReport, Harness: "claude-code"}
	if _, _, err := sess.ApplyAgentReport(winID, report); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	ids := sess.ListPTYIDs()
	if len(ids) != 1 {
		t.Fatalf("session has %d PTYs, want 1", len(ids))
	}
	backdateAgentClaim(t, sess, winID, 4*time.Second)
	paintPane(t, sess.GetPTY(ids[0]), claudePermissionPrompt)
	return sess, winID, ids[0]
}

// TestAVisibleBlockerBeatsAClaimThatWentQuiet is the regression test for the hole
// the source ranking left open.
//
// A harness with a hook reports working when its turn starts. If it then hits a
// prompt the hook does not cover, it stops and paints the question and says
// nothing more. The screen tier can read that question, but it reports below the
// harness, so the ranking refused it and the pane stayed working forever: the
// silence timer will not touch a pane whose screen answers, and no other tier
// asserts needs_input. The user was never told they were being waited for, which
// is the one thing the feature exists to do.
func TestAVisibleBlockerBeatsAClaimThatWentQuiet(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID, _ := blockedPaneClaimedByItsHarness(t)

	const stall = 30 * time.Second
	look := func(id string) bool { return sess.scanScreenForAgent(id, reg) }
	quiet := func(string) int64 { return 0 }
	if n := sess.applyStallHeuristic(time.Now().Add(stall+time.Second), stall, quiet, look); n != 0 {
		t.Fatalf("demoted %d panes that are visibly waiting on a human, want 0", n)
	}

	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input: the prompt is on the screen and the harness has gone silent", got)
	}
	claim := sess.agentClaimFor(winID)
	if claim.source != AgentSourceScreen || !claim.blocker {
		t.Fatalf("claim = %+v, want the screen holding it as an override", claim)
	}
	if claim.prior.source != AgentSourceReport || claim.prior.state != AgentStateWorking {
		t.Fatalf("prior = %+v, want the report's working claim kept for handing back", claim.prior)
	}
}

// TestAReportThatNamesNoHarnessLeavesTheAttributionAlone is the regression test
// for a pane going blind between two things that were both working correctly.
//
// The shipped hook shim reports a state and nothing else, because a hook knows
// what its turn is doing and has no reason to know what tuios calls the program
// it runs inside. That report used to write its empty harness id over the one
// the foreground-process detector had worked out, and the screen tier keys on
// that id to know whose rules to run: after one hook event the pane had no rules
// left, so no prompt on it could ever be seen again.
func TestAReportThatNamesNoHarnessLeavesTheAttributionAlone(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID := bareSessionWithWindow(t)
	ptyID := ptyIDOfWindow(t, sess, winID)
	matcher := newAgentMatcher(nil)
	running := fakeResolver(map[string]fakeProc{ptyID: {foregroundInfo{
		comm: "claude",
		argv: []string{"claude"},
		exe:  "/home/u/.local/share/claude/versions/2.1.222",
	}, true}})
	if n := sess.applyAgentDetection(running, matcher.identify); n != 1 {
		t.Fatalf("detection promoted %d windows, want 1", n)
	}

	// The shim's UserPromptSubmit hook, which is the no-source, no-harness path
	// every caller predating the harness registry takes.
	if err := sess.SetDaemonWindowAgentState(winID, AgentStateWorking, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}
	if got := agentHarnessIDOf(t, sess, winID); got != "claude-code" {
		t.Fatalf("harness = %q after a report that named none, want the detector's claude-code", got)
	}

	// The turn then stops on a permission prompt the hooks do not cover, which is
	// the case the screen tier and the visible-blocker override exist for.
	backdateAgentClaim(t, sess, winID, agentBlockerOverrideGrace+time.Second)
	paintPane(t, sess.GetPTY(ptyID), claudePermissionPrompt)

	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the screen tier found no rules to run, so the pane's attribution is gone")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input: the prompt is on the screen", got)
	}
}

// agentHarnessIDOf reads the harness a window is attributed to.
func agentHarnessIDOf(t *testing.T, sess *Session, windowID string) string {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.AgentHarness
		}
	}
	t.Fatalf("window %s not found", windowID)
	return ""
}

// TestTheOverrideIsHandedBackWhenThePromptLeaves is the other half, and the
// reason the exception is safe to cut into the ranking at all.
//
// The screen tier asserts needs_input and nothing else, so a pane it took over
// has no other way off that state: it would stick on needs_input exactly the way
// it used to stick on working, which is the same bug wearing a different glyph.
// A look that finds no rule matching therefore puts the displaced claim back.
func TestTheOverrideIsHandedBackWhenThePromptLeaves(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID, ptyID := blockedPaneClaimedByItsHarness(t)
	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the prompt on the screen did not match")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input before the prompt leaves", got)
	}

	// The prompt is still up, so the next look matches the same rule again. That
	// is the standing override rather than a fresh claim, and it has to keep
	// hold of what it displaced or there would be nothing left to hand back.
	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the prompt did not match while it is still on the screen")
	}
	if prior := sess.agentClaimFor(winID).prior; prior.source != AgentSourceReport {
		t.Fatalf("prior = %+v, want the displaced claim still held", prior)
	}

	// The user answered: the pane repaints over the question, which is the only
	// way a prompt can ever leave a screen, and that repaint is what runs the
	// look that ends the override.
	paintPane(t, sess.GetPTY(ptyID), "\x1b[2J\x1b[H"+"Running tests...\r\n")
	if sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("a screen with no prompt on it still matched a rule")
	}

	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state = %q, want the working the override displaced", got)
	}
	claim := sess.agentClaimFor(winID)
	if claim.source != AgentSourceReport || claim.blocker {
		t.Fatalf("claim = %+v, want the report's claim back and the override gone", claim)
	}

	// And the restored claim is an ordinary one again, so the silence timer can
	// demote it the way it always could. Nothing is stuck.
	const stall = 30 * time.Second
	backdateAgentClaim(t, sess, winID, stall+time.Second)
	look := func(id string) bool { return sess.scanScreenForAgent(id, reg) }
	if n := sess.applyStallHeuristic(time.Now(), stall, func(string) int64 { return 0 }, look); n != 1 {
		t.Fatalf("demoted %d panes, want 1: the restored claim is not stuck", n)
	}
}

// TestAFreshReportIsNotSecondGuessedByARule keeps the exception about staleness
// rather than about the ranking being wrong. A harness that is reporting for
// itself right now is the better source even when a rule can see something, and
// the grace window is what gives its hook time to describe the screen it just
// painted before a rule speaks over it.
func TestAFreshReportIsNotSecondGuessedByARule(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID := bareSessionWithWindow(t)
	report := AgentReport{State: AgentStateWorking, Source: AgentSourceReport, Harness: "claude-code"}
	if _, _, err := sess.ApplyAgentReport(winID, report); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	ptyID := sess.ListPTYIDs()[0]
	paintPane(t, sess.GetPTY(ptyID), claudePermissionPrompt)

	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the prompt on the screen did not match")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state = %q, want working: the report is seconds old and outranks the rule", got)
	}

	// The same screen, once the report has stood unrefreshed past the grace.
	backdateAgentClaim(t, sess, winID, agentBlockerOverrideGrace+time.Second)
	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the prompt on the screen did not match the second time")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input once the report has gone quiet", got)
	}
}

// TestAClaimTheScreenHasNotPaintedOverIsNotStale is the other staleness half.
// A report is only describing a screen that is gone if the pane has painted
// since, and a pane that has written nothing since the report has not.
func TestAClaimTheScreenHasNotPaintedOverIsNotStale(t *testing.T) {
	sess, winID, _ := blockedPaneClaimedByItsHarness(t)

	// The prompt was already on the screen when the harness reported working, so
	// the report is old without having been painted over. The report goes
	// straight to the gate because a live PTY's shell writes on its own schedule
	// and would otherwise supply an output time the test did not choose.
	stale := time.Now().Add(-10 * time.Second).UnixNano()
	if _, _, err := sess.ApplyAgentReport(winID, AgentReport{
		State:       AgentStateNeedsInput,
		Source:      AgentSourceScreen,
		Harness:     "claude-code",
		paneWroteAt: stale,
	}); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateWorking {
		t.Fatalf("state = %q, want working: the pane has not painted since the report", got)
	}
}

// TestOnlyABlockingRuleMayOverride keeps the hole the size it was cut. A screen
// rule asserting working or idle is guessing at a process from how it looks, and
// a guess must never outrank a source that was told.
func TestOnlyABlockingRuleMayOverride(t *testing.T) {
	dir := t.TempDir()
	manifest := `schema_version = 1
id = "spinner-harness"
display_name = "Spinner"

[detect]
argv0 = ["spinner-harness"]

[screen]
enabled = true
lines = 8

[[screen.rule]]
state = "working"
all = ["Do you want"]
`
	if err := os.WriteFile(filepath.Join(dir, "spinner-harness.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	reg, errs := harness.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("loading the manifests: %v", errs)
	}

	sess, winID := bareSessionWithWindow(t)
	report := AgentReport{State: AgentStateIdle, Source: AgentSourceReport, Harness: "spinner-harness"}
	if _, _, err := sess.ApplyAgentReport(winID, report); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	ptyID := sess.ListPTYIDs()[0]
	backdateAgentClaim(t, sess, winID, 4*time.Second)
	paintPane(t, sess.GetPTY(ptyID), claudePermissionPrompt)

	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the rule did not match")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateIdle {
		t.Fatalf("state = %q, want idle: a working rule may not override a report", got)
	}
}

// TestIdlePaneArmsNoTimers is the idle-cost guard for both timers the agent
// tiers added.
//
// Neither is a ticker, and that is the whole design: the screen tier's settle
// timer is armed by output and the hold's backstop by a held state, so a session
// where nothing is happening holds neither and wakes for neither. A regression
// that armed either one unconditionally would not show up as a failure anywhere
// else, because everything would still be correct, only awake.
func TestIdlePaneArmsNoTimers(t *testing.T) {
	sess, _, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	pty := sess.GetPTY(ptyID)

	// Long enough that a timer armed at session start would have fired and
	// re-armed by now, had one existed.
	time.Sleep(2 * screenSettleDelay)

	pty.screenSettleMu.Lock()
	settle := pty.screenSettle
	pty.screenSettleMu.Unlock()
	if settle != nil {
		t.Error("a pane that has produced no output armed the screen settle timer")
	}

	sess.agentHoldMu.Lock()
	held, timer := len(sess.agentHolds), sess.agentHoldTimer
	sess.agentHoldMu.Unlock()
	if held != 0 || timer != nil {
		t.Errorf("a session with nothing held has %d holds and timer=%v", held, timer != nil)
	}
}

// TestExplainAgentScreenVerbShowsWhatTheClassifierSaw drives the diagnostic over
// the real socket. Writing a screen rule is otherwise guesswork twice over: the
// text is matched inside the daemon against a pane that has moved on by the time
// anyone looks, and a rule that fails says nothing about which of its strings was
// the reason.
func TestExplainAgentScreenVerbShowsWhatTheClassifierSaw(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	ids := sess.ListPTYIDs()
	feedVT(t, sess.GetPTY(ids[0]), claudePermissionPrompt)

	c := dialVerb(t, sp)
	res := result(t, c.call(t,
		`{"id":1,"verb":"explain-agent-screen","params":{"session":"work","window":"Window","harness":"claude-code"}}`))

	if res["matched"] != true {
		t.Fatalf("the prompt on the screen did not match: %v", res)
	}
	if res["rule_state"] != "needs_input" {
		t.Fatalf("rule_state = %v, want needs_input", res["rule_state"])
	}

	tail, ok := res["tail"].([]any)
	if !ok || len(tail) == 0 {
		t.Fatalf("tail = %v, want the pane's screen lines", res["tail"])
	}
	var joined strings.Builder
	for _, line := range tail {
		joined.WriteString(line.(string) + "\n")
	}
	if !strings.Contains(joined.String(), "Do you want to proceed?") {
		t.Fatalf("the dumped tail is not what the classifier matched:\n%s", joined.String())
	}

	// Every rule is reported, matching or not, and a refusal names the strings
	// that caused it. That is the half that makes a failing rule fixable.
	rules, ok := res["rules"].([]any)
	if !ok || len(rules) < 2 {
		t.Fatalf("rules = %v, want one entry per declared rule", res["rules"])
	}
	refused := 0
	for _, r := range rules {
		m := r.(map[string]any)
		if m["matched"] == true {
			continue
		}
		refused++
		if m["missing"] == nil && m["none_of"] == nil && m["blocked"] == nil && m["empty"] == nil {
			t.Errorf("rule %v refused without saying why: %v", m["index"], m)
		}
	}
	if refused == 0 {
		t.Fatal("no rule refused, so the reason-reporting half is untested")
	}
}

// TestExplainAgentScreenVerbAnswersForAPaneWithNoHarness keeps the diagnostic
// usable on the pane a user actually has open. Most panes are not agents, and
// saying so is the answer rather than an error.
func TestExplainAgentScreenVerbAnswersForAPaneWithNoHarness(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	res := result(t, c.call(t,
		`{"id":1,"verb":"explain-agent-screen","params":{"session":"work","window":"Window"}}`))
	if res["harness_id"] != "" {
		t.Fatalf("harness_id = %v, want empty", res["harness_id"])
	}
	if res["matched"] != false {
		t.Fatalf("matched = %v, want false", res["matched"])
	}
}

// TestStallTimerStillDemotesAPaneWithNothingOnItsScreen keeps the fallback the
// timer exists for. A look that finds no rule is not a reason to leave a pane
// looking busy forever: the screen was read and said nothing, which is as much
// evidence as there is going to be.
func TestStallTimerStillDemotesAPaneWithNothingOnItsScreen(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	feedVT(t, sess.GetPTY(ptyID), "$ go build ./...\r\nok\r\n")

	const stall = 30 * time.Second
	look := func(id string) bool { return sess.scanScreenForAgent(id, reg) }
	n := sess.applyStallHeuristic(time.Now().Add(stall+time.Second), stall,
		func(string) int64 { return 0 }, look)
	if n != 1 {
		t.Fatalf("demoted %d panes whose screen says nothing, want 1", n)
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateIdle {
		t.Fatalf("state = %q, want idle", got)
	}
}
