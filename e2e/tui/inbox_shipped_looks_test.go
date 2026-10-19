package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestInboxOnTheShippedLooks runs the Inbox on the looks v0.8.0 ships, which
// the rest of the Inbox tests pin away: the dock on top and the rail on the
// right. On an 80 column screen the rail is 16 columns wide and runs under
// the right side of the agents view. A double-click on the agent goes to the
// item's session. The New agent button is clicked in the rail's band: the
// click has to reach the agents view, not land on the rail behind it.
//
// Negative control: with the rail's click ahead of the overlay's in
// handleMouseClick, the click selects a rail row, the New agent sheet never
// opens and the wait for it fails.
func TestInboxOnTheShippedLooks(t *testing.T) {
	const cols, rows = 80, 24
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)

	for _, name := range []string{"e2e-home", "e2e-fan"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: cols, rows: rows, args: []string{"attach", "e2e-home"}, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	if s := term.Screen(); !dockOnTop(s) || railHeaderColumn(s) < 0 {
		t.Fatalf("not the shipped looks: dock on top %v, rail at column %d\n%s",
			dockOnTop(s), railHeaderColumn(s), term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-fan", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: go test ./..."); err != nil {
		t.Fatalf("set-agent-state in the other session: %v\n%s", err, out)
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "1 needs you") && strings.Contains(text, "Allow") && strings.Contains(text, "$ go test")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the approval: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	if row := rowWith(s, "in e2e-fan"); row < 0 {
		t.Fatalf("the approval's card does not name its session on 80 columns\n%s", term.Snapshot())
	}
	saveFrame(t, term, "inbox-shipped-looks")

	// A double-click on the agent's tab goes to the item's session.
	tab := rowWith(s, "New agent") + 2
	mouseClick(t, term, 4, tab, tuitest.MouseLeft, 0)
	mouseClick(t, term, 4, tab, tuitest.MouseLeft, 0)
	switched := func(rows []lsRow) bool {
		for _, r := range rows {
			if r.Name == "e2e-fan" && r.Attached {
				return true
			}
		}
		return false
	}
	if listed := waitForRows(t, base, switched); !switched(listed) {
		t.Fatalf("the double-click did not take the client to the item's session: %+v\n%s", listed, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view again: %v", err)
	}
	if err := term.WaitForText("New agent", uiTimeout); err != nil {
		t.Fatalf("the agents view did not open again: %v\n%s", err, term.Snapshot())
	}

	// A cell of the New agent button inside the rail's band: the click
	// reaches the agents view, which covers the rail, and opens the New
	// agent sheet.
	row := rowWith(term.Screen(), "New agent")
	col := cols - narrowRailWidth + 4
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Start agent", uiTimeout); err != nil {
		t.Fatalf("a click on the button at (%d,%d) did not act on the agents view: %v\n%s", col, row, err, term.Snapshot())
	}
	alive(t, term, "after a click in the Inbox over the rail")
}
