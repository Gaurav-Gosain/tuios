package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestReportMessageIsNotOverwrittenByTheScreen: a report that carries its own
// words keeps them. The screen tier ranks below a report, so its claim is
// declined and the message stands.
func TestReportMessageIsNotOverwrittenByTheScreen(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID := bareSessionWithWindow(t)
	report := AgentReport{State: AgentStateNeedsInput, Source: AgentSourceReport, Harness: "claude-code", Message: "asks which branch to use"}
	if _, _, err := sess.ApplyAgentReport(winID, report); err != nil {
		t.Fatalf("ApplyAgentReport: %v", err)
	}
	ptyID := sess.ListPTYIDs()[0]
	paintPane(t, sess.GetPTY(ptyID), "Do you want to proceed?\r\n\xe2\x9d\xaf 1. Yes\r\n")
	sess.scanScreenForAgent(ptyID, reg)
	if got := agentMessageOf(t, sess, winID); got != "asks which branch to use" {
		t.Fatalf("the screen wrote over a report's message: %q", got)
	}
}
