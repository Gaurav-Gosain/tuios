package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// restoreRow is one row of list-windows --json.
type restoreRow struct {
	Name      string `json:"display_name"`
	Minimized bool   `json:"minimized"`
	Focused   bool   `json:"focused"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// waitRestoreRow waits until the window named name in session matches ok, and
// returns it.
func waitRestoreRow(t *testing.T, term *tuitest.Terminal, base, session, name, what string, ok func(restoreRow) bool) restoreRow {
	t.Helper()
	var last string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
		last = out
		if err == nil {
			var res struct {
				Windows []restoreRow `json:"windows"`
			}
			_ = json.Unmarshal([]byte(out), &res)
			for _, r := range res.Windows {
				if r.Name == name && ok(r) {
					return r
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: %s never matched\n%s\n%s", what, name, last, term.Snapshot())
	return restoreRow{}
}

// TestRailClickRestoresAMinimizedAgent is issue 1730. A click on the rail row
// of an agent whose pane is minimized has to restore the pane and focus it.
// The click only focused the pane, and the pane stayed minimized. A pane
// minimized through set-window has no box saved, and a restore grew it to
// 0x0, so the restored pane must also have a size, and a box of its own: the
// daemon gives every pane made with no client the same box.
func TestRailClickRestoresAMinimizedAgent(t *testing.T) {
	// Both panes are made and the agent minimized with no client attached,
	// the way a script does it: the daemon gives both the same box.
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, railFilesSwitchConfig)
	for _, args := range [][]string{
		{"new", "e2e", "--detach"},
		{"set-window", "-s", "e2e", "-w", "0", "--name", "AGENTONE"},
		{"new-window", "-s", "e2e", "PLAINPANE"},
		{"set-agent-state", "working", "-s", "e2e", "-w", "AGENTONE", "-m", "reading the tests"},
		{"set-window", "-s", "e2e", "-w", "AGENTONE", "--minimize"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", "e2e"}})
	if err := term.WaitForText("PLAINPANE", bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	waitRestoreRow(t, term, base, "e2e", "AGENTONE", "the agent pane minimized",
		func(r restoreRow) bool { return r.Minimized })

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		agents := railHeaderRow(s, "agents")
		return agents >= 0 && railRowAfter(s, "AGENTONE", agents) > agents
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never listed the agent: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	railShot(t, term, "01-minimized-agent")

	s := term.Screen()
	row := railRowAfter(s, "AGENTONE", railHeaderRow(s, "agents"))
	col := strings.Index(railLine(s, row), "AGENTONE")
	mouseClick(t, term, len([]rune(railLine(s, row)[:col])), row, tuitest.MouseLeft, 0)

	agent := waitRestoreRow(t, term, base, "e2e", "AGENTONE", "ASSERTION: the click restored and focused the agent pane",
		func(r restoreRow) bool { return !r.Minimized && r.Focused && r.Width > 0 && r.Height > 0 })
	plain := waitRestoreRow(t, term, base, "e2e", "PLAINPANE", "the other pane", func(restoreRow) bool { return true })
	if agent.X == plain.X && agent.Y == plain.Y {
		t.Fatalf("ASSERTION: the restored pane sits on the other pane's corner (%d,%d)\n%s", agent.X, agent.Y, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	railShot(t, term, "02-after-click")
	alive(t, term, "after the click on a minimized agent")
}
