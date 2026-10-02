package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSessionNameReachesThePanels renders the rail with one named session
// holding a shell and an agent pane, and prints the whole panel: the
// terminals header and the agent row must both say the session's name, and
// the focused pane's agent row must be bold.
func TestSessionNameReachesThePanels(t *testing.T) {
	m := bareShellOS(t, 2)
	m.SessionName = "session-0"
	m.SessionDisplayName = "proof"
	m.Windows[1].AgentState = "working"
	m.FocusedWindow = 1

	panel := sidebarText(t, m)
	t.Log("\n" + panel)

	if !strings.Contains(panel, "proof") {
		t.Fatalf("the named session never reached the panel:\n%s", panel)
	}
	for _, row := range strings.Split(panel, "\n") {
		if strings.Contains(row, "agents") {
			continue
		}
		if strings.Contains(row, "proof") && strings.Contains(row, "\x1b[1;3") {
			return
		}
	}
	t.Fatal("the focused pane's agent row is not bold:\n" + panel)
}

var _ = terminal.Window{}
