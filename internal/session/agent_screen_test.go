package session

import (
	"testing"
	"time"
)

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
