package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	gate := t.TempDir()
	writeConfig(t, base, fmt.Sprintf(`
[dock]
left = ["mode", "workspaces", "trail", "custom/waiting"]

[dock.custom.waiting]
command = 'n=$(%[1]s list-agents -s e2e | grep -c needs_input); if [ -e %[2]s/hold ]; then : > %[2]s/started; until [ ! -e %[2]s/hold ]; do sleep 0.05; done; fi; echo $n >> %[2]s/runs; echo WAIT$n'
refresh = "event:after-agent-state"

[notifications.agent]
settle_seconds = 0
suppress_focused = false

[notifications.agent.states]
needs_input = true
working = false
`, tuiosBin, gate))
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

	// Issue 1416: the answer lands while the cell is still running. The run has
	// read needs_input and is held on a file, so the event comes in mid-run with
	// no race. It has to start one more run, which reads the pane as working.
	// Negative control: keep fire's re-run for the rail only, and the second run
	// never comes, so the cell keeps WAIT1.
	hold := filepath.Join(gate, "hold")
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setState("needs_input")
	gateDeadline := time.Now().Add(uiTimeout)
	for {
		if _, err := os.Stat(filepath.Join(gate, "started")); err == nil {
			break
		}
		if time.Now().After(gateDeadline) {
			t.Fatalf("the held run never started\n%s", term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(gate, "runs"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setState("working")
	// Long enough for the debounce to fire inside the run. A run released sooner
	// would see the event after it ended and pass on any build, so this cannot
	// fail a good build, only weaken the check.
	time.Sleep(time.Second)
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	gateDeadline = time.Now().Add(uiTimeout)
	for {
		runs, _ := os.ReadFile(filepath.Join(gate, "runs"))
		if slices.Contains(strings.Fields(string(runs)), "0") {
			break
		}
		if time.Now().After(gateDeadline) {
			t.Fatalf("an event during a run did not start one more run: runs %q\n%s", runs, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitCell("WAIT0", "after an answer that landed during a run")
}
