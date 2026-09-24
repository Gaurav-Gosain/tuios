package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// fakeSeenAtClock makes markAgentSeenAt read a clock the test moves.
func fakeSeenAtClock(t *testing.T, start int64) *int64 {
	t.Helper()
	now := start
	prev := agentSeenAtNow
	agentSeenAtNow = func() int64 { return now }
	t.Cleanup(func() { agentSeenAtNow = prev })
	return &now
}

// TestAgentSeenAtRecordsWhenTheUserLooksAway: focus entering an agent pane
// and focus leaving it both record the time, so for a pane out of view the
// stored value is when the user looked away. It survives a restart in
// sidebar.json, beside the seen turn counts.
func TestAgentSeenAtRecordsWhenTheUserLooksAway(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.SidebarAgentSeenAt = nil
	now := fakeSeenAtClock(t, 1000)

	m.FocusedWindow = 1
	m.FocusWindow(0) // into w-idle, out of w-work
	if got, ok := m.agentAwaySince("w-idle"); !ok || got != 1000 {
		t.Fatalf("w-idle seen at %d (%v) after focusing it, want 1000", got, ok)
	}
	if got := m.SidebarAgentSeenAt["w-work"]; got != 1000 {
		t.Fatalf("w-work seen at %d after leaving it, want 1000", got)
	}

	*now = 5000
	m.FocusWindow(2) // out of w-idle at 5000
	if got, _ := m.agentAwaySince("w-idle"); got != 5000 {
		t.Fatalf("w-idle away since %d, want 5000: the moment focus left it", got)
	}

	restored := &OS{Settings: config.Global}
	restored.loadSidebarState()
	if got, ok := restored.agentAwaySince("w-idle"); !ok || got != 5000 {
		t.Fatalf("after a restart w-idle away since %d (%v), want 5000", got, ok)
	}
	if got, _ := restored.agentAwaySince("w-done"); got != 5000 {
		t.Fatalf("after a restart w-done seen at %d, want 5000", got)
	}
}

// TestAgentSeenAtIgnoresPlainPanes: a pane no agent reported on records
// nothing and writes nothing, so focus changes cost nothing without agents.
func TestAgentSeenAtIgnoresPlainPanes(t *testing.T) {
	m := newNarrowOS(t, 120, 40)
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{
		{ID: "a", Width: 40, Height: 20, Workspace: 1},
		{ID: "b", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	fakeSeenAtClock(t, 1000)
	m.FocusWindow(1)
	m.FocusWindow(0)
	if len(m.SidebarAgentSeenAt) != 0 {
		t.Fatalf("plain panes recorded %v", m.SidebarAgentSeenAt)
	}
}
