package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// switchWorkspace moves to a workspace and waits for it to hold n windows.
func switchWorkspace(t *testing.T, term *tuitest.Terminal, ws string, n int) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", ws); err != nil {
		t.Fatalf("switch to workspace %s: %v", ws, err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == n
	}, uiTimeout); err != nil {
		t.Fatalf("workspace %s never settled at %d windows: %v\n%s", ws, n, err, term.Snapshot())
	}
}

// TestWorkspaceSwitchDoesNotRepaintThePane drives the reported bug end to end: a
// pane that printed something, hidden and shown again, must still show it once.
//
// The report was a pane showing fish's welcome banner and prompt twice, the
// second copy lower down with blank space between, gaining another copy on every
// switch. Hiding a workspace unsubscribes its panes and showing it subscribes
// them again, and the daemon used to answer every subscribe with the pane's
// whole output history, which the client painted below the paint already there.
func TestWorkspaceSwitchDoesNotRepaintThePane(t *testing.T) {
	const marker = "PANEMARK-7431"

	term, _ := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e-switch"}})
	waitBoot(t, term)

	newWindow(t, term)
	enterTerminalMode(t, term)
	// Printed by the shell rather than typed, so the command line itself cannot
	// be mistaken for the output and every hit is a real paint.
	runInShell(t, term, `printf '%s\n' "$(echo PANEMARK)-7431"`, marker, shellTimeout)
	leaveTerminalMode(t, term)

	for range 3 {
		switchWorkspace(t, term, "2", 0)
		switchWorkspace(t, term, "1", 1)
	}

	// WaitStable so the count is taken after the switch's repaint has landed.
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the switches: %v", err)
	}
	if n := strings.Count(term.Screen().Text(), marker); n != 1 {
		t.Fatalf("the pane shows its output %d times after three workspace round trips, want 1\n%s",
			n, term.Snapshot())
	}
	alive(t, term, "after workspace switching")
}
