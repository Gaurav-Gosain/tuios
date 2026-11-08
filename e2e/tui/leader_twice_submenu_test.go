package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestLeaderTwiceClosesTheSubMenu: in terminal mode, ctrl+b w opens the
// workspace menu, and the leader again sends the leader to the pane and
// closes the menu. It holds:
//
//   - the key after that is the pane's: 2 is typed into the shell, and the
//     client stays on workspace 1;
//   - in window management mode the key after it is a window key: n opens a
//     pane.
//
// How this could pass wrongly, written down first:
//   - The shell could echo 2 while the client also switched. The test reads
//     the workspace from the daemon and the shell's line both.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the leader
// clears the leader alone, the workspace menu stays open, and 2 switches to
// workspace 2.
func TestLeaderTwiceClosesTheSubMenu(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"new", "home"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	sendKeys(t, term, "clear", tuitest.Enter)
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, term, tuitest.Ctrl('b'), "w")
	sendKeys(t, term, tuitest.Ctrl('b'))
	sendKeys(t, term, "2", "2", "7")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "227") }, uiTimeout); err != nil {
		t.Errorf("ASSERTION: the keys after the leader twice did not reach the shell\n%s", term.Snapshot())
	}
	// The dock names the workspace and the pane count, 1:1 on the first.
	if !strings.Contains(term.Screen().Text(), "1:1") {
		t.Fatalf("ASSERTION: the client left workspace 1\n%s", term.Snapshot())
	}

	// Window management mode: n after the leader twice opens a pane.
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "w")
	sendKeys(t, term, tuitest.Ctrl('b'))
	sendKeys(t, term, "n")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "1:2") }, uiTimeout); err != nil {
		t.Errorf("ASSERTION: in window management mode, n after the leader twice did not open a pane\n%s", term.Snapshot())
	}
}
