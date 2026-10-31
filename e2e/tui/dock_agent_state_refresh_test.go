package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDockAgentCellClearsWhenTheAnswerMovesThePaneOn holds a dock component
// watching agent state to every transition, not only the ones the person is
// alerted about. The cell counts the panes on needs_input, the way a fleet cell
// shows a hand while an agent waits. Answering the question moves the pane to
// working, which [notifications.agent.states] leaves without an alert, and the
// count has to drop all the same. When the refresh rode on the alert, the cell
// kept its hand after the rail had cleared.
func TestDockAgentCellClearsWhenTheAnswerMovesThePaneOn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, fmt.Sprintf(`
[dock]
left = ["mode", "workspaces", "trail", "custom/waiting"]

[dock.custom.waiting]
command = 'echo WAIT$(%s list-agents -s e2e | grep -c needs_input)'
refresh = "event:after-agent-state"

[notifications.agent]
settle_seconds = 0
suppress_focused = false

[notifications.agent.states]
needs_input = true
working = false
`, tuiosBin))
	term := startIn(t, base, startOpts{cols: 160, rows: 40, args: []string{"new", "e2e"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")
	renameWindow(t, term, "ASKER")

	waitCell := func(want, why string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			if strings.Contains(dockRowText(t, term), want) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("the dock cell never read %s %s\n%s", want, why, term.Snapshot())
	}
	setState := func(state string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-agent-state", state, "-s", "e2e", "-w", "ASKER"); err != nil {
			t.Fatalf("set-agent-state %s failed: %v\n%s", state, err, out)
		}
	}

	setState("working")
	waitCell("WAIT0", "once the pane was an agent")
	setState("needs_input")
	waitCell("WAIT1", "while the pane waited on the person")
	setState("working")
	waitCell("WAIT0", "after the answer moved the pane to working")
}
