package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailTerminalsKeepsItsHeaderWhenAllPanesAreAgents: on a rail 30 columns
// wide or less, a pane that runs an agent is listed under agents and not under
// terminals. In a session whose panes all run agents, the terminals section
// had no rows left, so its header and its "+" (new terminal) were not drawn,
// and the rail had no terminals section at all. The section now keeps its
// header and its "+", with one row that says where the panes are.
//
// Negative control, confirmed red: with movedToAgents in
// sidebarPanelLinesForTree left at 0, the wait for the "2 in agents" row
// times out.
func TestRailTerminalsKeepsItsHeaderWhenAllPanesAreAgents(t *testing.T) {
	term, base := railClient(t, "e2e", railFilesSwitchConfig, startOpts{cols: 120, rows: 40})

	renameWindow(t, term, "AGENTONE")
	newWindow(t, term)
	renameWindow(t, term, "PLAINPANE")
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", "AGENTONE"); err != nil {
		t.Fatalf("set-agent-state AGENTONE: %v\n%s", err, out)
	}

	// One plain pane left: terminals lists it, with no note.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		terms, agents := railHeaderRow(s, "terminals"), railHeaderRow(s, "agents")
		plain := railRowOf(s, "PLAINPANE")
		return terms >= 0 && agents >= 0 && plain > terms && plain < agents &&
			railRowAfter(s, "AGENTONE", agents) > agents && railRowOf(s, "in agents") < 0
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never listed the plain pane under terminals and the agent under agents: %v\n%s",
			err, term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e", "-w", "PLAINPANE"); err != nil {
		t.Fatalf("set-agent-state PLAINPANE: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		terms, agents := railHeaderRow(s, "terminals"), railHeaderRow(s, "agents")
		note := railRowOf(s, "2 in agents")
		return terms >= 0 && agents > terms && note == terms+1 &&
			strings.HasSuffix(strings.TrimRight(railLine(s, terms), " │"), "+") &&
			railRowAfter(s, "PLAINPANE", agents) > agents
	}, uiTimeout); err != nil {
		railShot(t, term, "terminals-all-agents-failed")
		t.Fatalf("ASSERTION: with every pane an agent, the terminals header, its + or the \"2 in agents\" row is missing: %v\n%s",
			err, term.Snapshot())
	}
	railShot(t, term, "terminals-all-agents")

	// The "+" still opens a terminal, and the new pane is listed again.
	s := term.Screen()
	terms := railHeaderRow(s, "terminals")
	plus := strings.LastIndex(strings.TrimRight(railLine(s, terms), " │"), "+")
	before := countWindows(s)
	mouseClick(t, term, len([]rune(railLine(s, terms)[:plus])), terms, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == before+1 && railRowOf(s, "in agents") < 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the terminals + did not open a pane, or the note stayed: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after the terminals section lost and got back its rows")
}
