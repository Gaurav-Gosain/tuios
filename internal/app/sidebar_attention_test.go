package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSidebarCacheInvalidatesOnAttention checks the signature covers both new
// inputs: a window's agent state and the purely local unread bit. Either going
// stale would leave the rail showing yesterday's triage.
func TestSidebarCacheInvalidatesOnAttention(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	win := &terminal.Window{ID: "w1", CustomName: "ALPHA", AgentState: "working"}
	m := &OS{Settings: config.Global, Windows: []*terminal.Window{win}, FocusedWindow: 0, Width: 120, Height: 40, SessionName: "s"}

	base := m.sidebarSignature()
	win.AgentState = "done"
	afterState := m.sidebarSignature()
	if afterState == base {
		t.Fatal("agent state change did not move the signature")
	}
	m.markAgentSeen("w1")
	if m.sidebarSignature() == afterState {
		t.Fatal("clearing the unread bit did not move the signature")
	}
}
