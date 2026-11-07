package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail takes the keyboard and no mode. Its lease (mode.go) saves the
// mode chosen when it opened and hands it back on esc, while a rail action
// that relocates the person cancels the lease and its choice stands.
//
// This is a guard, not a regression test: no keyboard browse in the rail
// makes a choice of mode, so removing the hand-back changes nothing a test
// can see (NEGATIVE_CONTROLS.md, "The rail as a mode lease").

// railPanes is two panes in window management, with the rail open on the
// keyboard.
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
	time.Sleep(insertGuard)
	if err := term.SendKeys(","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("after esc out of the rail, ',' did not open settings: not window management\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "settings")
}
