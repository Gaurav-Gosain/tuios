package session

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

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
