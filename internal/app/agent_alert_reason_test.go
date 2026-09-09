package app

import (
	"strings"
	"testing"
)

// TestAgentAlertCarriesTheReason: an alert says what the agent asked, not only
// that it asked. The dock toast and the in-band notification both carry the
// pane's message after the headline, and a pane with no message gets the
// headline alone.
func TestAgentAlertCarriesTheReason(t *testing.T) {
	m := alertOS(t, zeroSettle())
	host := captureHost(t, m)

	m.Windows[0].AgentState = "working"
	m.Windows[0].AgentMessage = "approval: Do you want to make this edit to main.go?"
	m.noteAgentState(m.Windows[0], "needs_input")
	if len(m.Notifications) != 1 {
		t.Fatalf("raised %d dock messages, want 1", len(m.Notifications))
	}
	want := "w-1 needs input · approval: Do you want to make this edit to main.go?"
	if got := m.Notifications[0].Message; got != want {
		t.Fatalf("dock toast = %q, want %q", got, want)
	}
	if out := host.b.String(); !strings.Contains(out, "approval: Do you want to make this edit to main.go?") {
		t.Fatalf("the in-band notification does not carry the question: %q", out)
	}

	m.Notifications = nil
	m.Windows[1].AgentState = "working"
	m.noteAgentState(m.Windows[1], "needs_input")
	if got := m.Notifications[0].Message; got != "w-2 needs input" {
		t.Fatalf("a pane with no message alerted with %q, want the headline alone", got)
	}
}
