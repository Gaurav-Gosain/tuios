package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	ghosttyGermanPipe = "\x1b[55;3;124u"

	ghosttyGermanBackslash = "\x1b[55;4;92u"

	altShiftBackslashText = "\x1b[92;4;124u"

	escPlus2 = "\x1b2"
)

// TestLeaderTakesTheCharacterAnOptionChordTyped is #850 end to end.
//
// Negative controls (NEGATIVE_CONTROLS.md):
//   - main: the leader and Option and 7 do not split, and this fails at the
//     first count.
//   - optionTypedKey ahead of the chord spellings in bindingKeys: Option and
//     7 in window mode splits instead of switching to workspace 7.
//   - optionTypedKey without the US shifted check: Alt, Shift and \ after
//     the leader splits.
func TestLeaderTakesTheCharacterAnOptionChordTyped(t *testing.T) {
	base := t.TempDir()
	const session = "e2e-opt-german-pipe"
	term := macSessionOn(t, base, session, "", "com.apple.keylayout.German")
	waitPanesOn(t, term, base, session, 1, 2, "the fixture")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((3+4))", "ready-7", shellTimeout)

	leaderKey(t, term, ghosttyGermanPipe)
	waitPanesOn(t, term, base, session, 1, 3, "the leader, then Option and 7 (|)")
	saveFrame(t, term, "leader-option-7-split")

	press(t, term, "Option and 2 (ESC 2)", escPlus2)
	waitWorkspace(t, base, session, 2)
	waitPanesOn(t, term, base, session, 2, 1, "workspace 2")

	leaderKey(t, term, ghosttyGermanBackslash)
	waitPanesOn(t, term, base, session, 2, 2, "the leader, then Option, Shift and 7 (\\)")

	leaderKey(t, term, altShiftBackslashText)
	leaderKey(t, term, ghosttyGermanPipe)
	waitPanesAtLeast(t, term, base, session, 2, 3, "Alt, Shift and \\ after the leader, then the leader and |")
	time.Sleep(4 * insertGuard)
	if n := panesOn(t, base, session, 2); n != 3 {
		t.Fatalf("workspace 2 has %d panes, want 3: Alt, Shift and \\ after the leader split as |\n%s", n, term.Snapshot())
	}

	leaveTerminalMode(t, term)
	press(t, term, "Option and 7 in window mode", ghosttyGermanPipe)
	waitWorkspace(t, base, session, 7)
	if n := panesOn(t, base, session, 2); n != 3 {
		t.Fatalf("Option and 7 in window mode split a pane (%d on workspace 2)\n%s", n, term.Snapshot())
	}
	if n := panesOn(t, base, session, 7); n != 1 {
		t.Fatalf("Option and 7 in window mode split workspace 7 (%d panes)\n%s", n, term.Snapshot())
	}
}

func waitPanesAtLeast(t *testing.T, term *tuitest.Terminal, base, sess string, ws, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	for panesOn(t, base, sess, ws) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%s: workspace %d has %d panes, want %d or more\n%s", what, ws, panesOn(t, base, sess, ws), n, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
