package session

import (
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
	var joined string
	for _, line := range tail {
		joined += line.(string) + "\n"
	}
	if !strings.Contains(joined, "Do you want to proceed?") {
		t.Fatalf("the dumped tail is not what the classifier matched:\n%s", joined)
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
