package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func railPanes(t *testing.T) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 120, rows: 36})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
	leaveTerminalMode(t, term)
	if err := term.SendKeys("s"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("terminals", uiTimeout); err != nil {
		t.Fatalf("s did not open the rail: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	return term
}

// TestRailEscHandsBackWindowMode: the rail opened in window management and
// left with esc after a browse leaves window management, where "," opens the
// settings panel.
func TestRailEscHandsBackWindowMode(t *testing.T) {
	term := railPanes(t)
	for _, k := range []any{"j", "k", tuitest.Esc} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := term.SendKeys(","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("after esc out of the rail, ',' did not open settings: not window management\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "settings")
}

// TestRailEscKeepsAChoiceMadeWhileItWasOpen: the rail opened in window
// management, then a choice of terminal mode made from outside the client
// (run-command TerminalMode, as a script or a lesson does) while the rail
// has the keyboard, then esc. The choice spent the rail's lease, so esc leaves
// the rail and keeps terminal mode: a line typed then reaches the shell.
//
// How this could pass wrongly, written down first:
//   - The run-command could fail and the shell get the line anyway. The test
//     checks the dock says terminal mode before the esc.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on main the esc hands
// back the window management the rail saved, and the line runs as window
// keys.
func TestRailEscKeepsAChoiceMadeWhileItWasOpen(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"new", "home"}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
	leaveTerminalMode(t, term)
	choose := func() {
		t.Helper()
		if out, err := tuiosCLI(t, base, "run-command", "-s", "home", "TerminalMode"); err != nil {
			t.Fatalf("run-command TerminalMode: %v\n%s", err, out)
		}
	}
	choose()
	if err := term.SendKeys("echo plain-$((1+2))", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("plain-3", uiTimeout); err != nil {
		t.Fatalf("run-command TerminalMode did not choose terminal mode: %v\n%s", err, term.Snapshot())
	}
	leaveTerminalMode(t, term)
	sendKeys(t, term, "s")
	if err := term.WaitForText("terminals", uiTimeout); err != nil {
		t.Fatalf("s did not open the rail: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	choose()
	sendKeys(t, term, tuitest.Esc)
	if err := term.SendKeys("echo kept-$((2+3))", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("kept-5", uiTimeout); err != nil {
		t.Errorf("ASSERTION: after esc out of the rail the line did not reach the shell: the rail handed back window management over the choice made while it was open\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "after-esc")
}
