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

// paintPane writes to the pane's emulator and records that the pane wrote, which
// is one event in the daemon and two calls here because the test bypasses the
// read loop that would otherwise do both.
//
// The screen is cleared first, in the same write, because the pane has a real
// shell in it. That shell prints its prompt a few tens of milliseconds after
// the window is created, and a test that paints before the prompt arrives sees
// a clean screen while one that paints after sees its text appended to the
// prompt's line: "sh-3.2$ Do you want to make this edit to main.go?" instead of
// the question on its own. Which side of that the test lands on is a race it
// usually won and sometimes lost, and losing it is a nightly failure on a rule
// that matches the line rather than a defect in the rule. Clearing costs
// nothing and settles it, and it goes in the same feedVT call so the shell,
// which writes under the same lock, cannot land between the clear and the text.
func paintPane(t *testing.T, p *PTY, data string) {
	t.Helper()
	feedVT(t, p, clearScreen+data)
	p.lastOutput.Store(time.Now().UnixNano())
}

// claudePermissionPrompt is what Claude Code paints and then goes silent behind.
// It is the exact shape the bundled manifest's first rule keys on.
const claudePermissionPrompt = "Do you want to proceed?\r\n" +
	"\xe2\x9d\xaf 1. Yes\r\n" +
	"  2. Yes, and don't ask again\r\n" +
	"  3. No, and tell Claude what to do differently (esc)\r\n"

// clearScreen erases the display and homes the cursor.
const clearScreen = "\x1b[2J\x1b[H"
