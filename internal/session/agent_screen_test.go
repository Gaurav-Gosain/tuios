package session

import (
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
