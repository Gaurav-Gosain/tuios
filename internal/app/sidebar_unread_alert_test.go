//go:build !slim

package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestATurnBetweenTwoSyncsRaisesTheDoneAlert: the folded turn is a finish
// like any other, so the alert policy hears of it.
//
// Negative control: with the considerAgentAlert call cut from
// noteAgentTurnWithin, the second done raises no dock message.
func TestATurnBetweenTwoSyncsRaisesTheDoneAlert(t *testing.T) {
	m := alertOS(t, zeroSettle())
	captureHost(t, m)
	w := m.Windows[1]
	sync := func(seq uint64) {
		m.updateWindowFromState(w, &session.WindowState{
			ID: w.ID, CustomName: w.ID, Workspace: 1,
			AgentState: session.AgentStateDone, CompletionSeq: seq,
		})
	}
	sync(0)
	m.Notifications = nil
	sync(1)
	if len(m.Notifications) != 1 {
		t.Fatalf("the folded finish raised %d dock messages, want 1", len(m.Notifications))
	}
}
