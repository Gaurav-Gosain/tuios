package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// railNarrowDefaultConfig is the shipped rail: the default layout, 24 columns
// wide.
const railNarrowDefaultConfig = "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 24\n"

// TestRailTerminalsListsEveryPane: the terminals section is the list of panes,
// and the agents section is a status view over the panes that run an agent. On
// a rail 30 columns wide or less, a pane that ran an agent was listed only
// under agents. A person who runs an agent in every pane saw the terminals
// header over one row, "3 in agents", and no terminals at all. On the shipped
// 24 column rail each pane now has its row under terminals, and each agent
// pane has its row under agents as well.
//
// Negative control, confirmed red: with the compact rule back in
// sidebarPanelLinesForTree, the wait for the three rows under terminals times
// out on "3 in agents".
func TestRailTerminalsListsEveryPane(t *testing.T) {
	term, base := railClient(t, "e2e", railNarrowDefaultConfig, startOpts{cols: 120, rows: 40})

	panes := []string{"AGENTONE", "AGENTTWO", "AGENTTRI"}
	renameWindow(t, term, panes[0])
	for _, name := range panes[1:] {
		newWindow(t, term)
		renameWindow(t, term, name)
	}
	for _, name := range panes {
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", name); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", name, err, out)
		}
	}

	listed := func(s tuitest.Screen) bool {
		terms, agents := railHeaderRow(s, "terminals"), railHeaderRow(s, "agents")
		if terms < 0 || agents < 0 || railRowOf(s, "in agents") >= 0 {
			return false
		}
		for _, name := range panes {
			row := railRowAfter(s, name, terms)
			if (agents > terms && row > agents) || row < 0 || railRowAfter(s, name, agents) < 0 {
				return false
			}
		}
		return true
	}
	if err := term.WaitFor(listed, uiTimeout); err != nil {
		railShot(t, term, "terminals-every-pane-failed")
		t.Fatalf("ASSERTION: with every pane an agent, a pane is missing under terminals or under agents: %v\n%s",
			err, term.Snapshot())
	}
	railShot(t, term, "terminals-every-pane")

	alive(t, term, "after every pane became an agent")
}
