package tuie2e

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// dockAgentsStrip matches the dock's session controls under --ascii-only, at
// the bar's right-hand end: the agents control "i" with its count when there is
// one, then "+", "d" and "X". The controls without the agents control match
// with the first group empty.
var dockAgentsStrip = regexp.MustCompile(`(\si(?: (\d+))?)?  \+  d  X$`)

// dockAgents finds the dock row and reads its session controls: the column
// of the agents control's "i" (-1 when it is not drawn), its count ("" for
// none) and the column of "+". The dock is the first or the last row.
func dockAgents(s tuitest.Screen) (row, col int, count string, plus int, ok bool) {
	_, rows := s.Size()
	for _, y := range []int{0, rows - 1} {
		line := strings.TrimRight(s.Line(y), " ")
		m := dockAgentsStrip.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		cells := func(b int) int { return len([]rune(line[:b])) }
		plus = cells(strings.LastIndex(line, "+"))
		if m[2] < 0 {
			return y, -1, "", plus, true
		}
		if m[4] >= 0 {
			count = line[m[4]:m[5]]
		}
		return y, cells(m[2]) + 1, count, plus, true
	}
	return 0, -1, "", -1, false
}

// TestDockAgentsButton drives the dock's agents control on a real daemon. A
// session with no agent pane has no control. A pane with an agent state brings
// it in, a pane that waits on the person puts the count on it in a colour of
// its own, a hover names the view and its key, and a click opens the view.
//
// How it could pass wrongly: a control that is never drawn passes the first
// half, so the same session must then show it. A count that is drawn in the
// plain dock ink passes the text check, so its foreground is compared with the
// + control's.
//
// Negative control: on origin/main the dock has no agents control, and the
// wait for it after set-agent-state working fails.
func TestDockAgentsButton(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "e2e", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "e2e", "--name", "AGENTPANE"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	// attachIn waits for a rounded pane border, which --ascii-only does not
	// draw, so the client is attached here and waited on by its dock.
	term := startIn(t, base, startOpts{cols: 120, rows: 32, args: []string{"--ascii-only", "attach", "e2e"}, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, _, _, ok := dockAgents(s)
		return ok && strings.Contains(s.Text(), "AGENTPANE")
	}, bootTimeout); err != nil {
		t.Fatalf("the client never drew the session: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	if _, col, _, _, _ := dockAgents(term.Screen()); col >= 0 {
		t.Fatalf("the dock shows the agents control with no agent pane\n%s", term.Snapshot())
	}
	railShot(t, term, "dock-agents-none")

	setState := func(state string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "set-agent-state", state, "-s", "e2e", "-w", "AGENTPANE"); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", state, err, out)
		}
	}

	setState("working")
	if err := term.WaitFor(func(s tuitest.Screen) bool { _, col, _, _, _ := dockAgents(s); return col >= 0 }, uiTimeout); err != nil {
		railShot(t, term, "dock-agents-missing")
		t.Fatalf("the dock never showed the agents control for an agent pane: %v\n%s", err, term.Snapshot())
	}
	if _, _, count, _, _ := dockAgents(term.Screen()); count != "" {
		t.Fatalf("the agents control shows a count of %s with no agent waiting\n%s", count, term.Snapshot())
	}
	railShot(t, term, "dock-agents-idle")

	setState("needs_input")
	if err := term.WaitFor(func(s tuitest.Screen) bool { _, _, count, _, _ := dockAgents(s); return count == "1" }, uiTimeout); err != nil {
		t.Fatalf("the agents control never showed the count of 1: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	row, col, _, plus, _ := dockAgents(s)
	if count, add := s.Cell(col+2, row), s.Cell(plus, row); count.Fg == add.Fg {
		t.Fatalf("the count is in the + control's colour %v, not the attention colour\n%s", add.Fg, term.SnapshotStyled())
	}
	railShot(t, term, "dock-agents-waiting")

	moveMouse(t, term, col, row)
	if err := term.WaitForText("Agents view, 1 needs you (ctrl+b i)", uiTimeout); err != nil {
		t.Fatalf("a hover on the agents control showed no hint with the key: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "dock-agents-hover")

	if strings.Contains(term.Screen().Text(), "Needs you") {
		t.Fatalf("the agents view group is on screen before the click\n%s", term.Snapshot())
	}
	clickAt(t, term, col, row, 1)
	if err := term.WaitForText("Needs you", uiTimeout); err != nil {
		t.Fatalf("a click on the agents control did not open the agents view: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "dock-agents-open")
	alive(t, term, "after the agents view opened from the dock")
}
