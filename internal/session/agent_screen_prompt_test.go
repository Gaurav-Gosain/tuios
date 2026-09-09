package session

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestScreenClaimCarriesThePromptLine: the screen tier reports the question
// the rule read, fronted by what sort of block it is, rather than the
// manifest's fixed sentence.
func TestScreenClaimCarriesThePromptLine(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatalf("loading the bundled manifests: %v", errs)
	}
	sess, winID, ptyID := agentPaneWithHarness(t, "claude-code", AgentStateWorking)
	paintPane(t, sess.GetPTY(ptyID), "Do you want to make this edit to main.go?\r\n\xe2\x9d\xaf 1. Yes\r\n  2. No\r\n")

	if !sess.scanScreenForAgent(ptyID, reg) {
		t.Fatal("the screen scan matched nothing on a painted permission prompt")
	}
	if got := agentStateOf(t, sess, winID); got != AgentStateNeedsInput {
		t.Fatalf("state = %q, want needs_input", got)
	}
	if got, want := agentMessageOf(t, sess, winID), "approval: Do you want to make this edit to main.go?"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

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
