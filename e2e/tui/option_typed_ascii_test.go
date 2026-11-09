package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Discussion #850: on a German Mac layout | is Option and 7, and \ is
// Option, Shift and 7. After the leader tuios asks the terminal for every key
// as an escape code with its text, so Ghostty sends Option and 7 as the 7 key
// with the Alt bit and the text "|". tuios read only alt+7, which the leader
// does not bind, and the split never came.
//
// Each sequence is byte for byte what the terminal writes for the key.
//
// How these could pass wrongly, written down first:
//   - The split could come from another key. Each step counts the panes on
//     workspace 1 from the daemon, before and after one key.
//   - A key that must do nothing could do nothing for an unrelated reason.
//     The step that says so is followed by a key that splits, in the same
//     session, and the count is read after it.
//   - The fix could take the chord's own binding away. Window mode binds
//     opt+7 to workspace 7, and the test checks that it still switches.
const (
	// ghosttyGermanPipe is Option and 7 on a German Mac layout from Ghostty
	// under report-all with associated text: key 55 (7), Alt, text 124 (|).
	ghosttyGermanPipe = "\x1b[55;3;124u"

	// ghosttyGermanBackslash is Option, Shift and 7 on that layout: key 55,
	// Alt and Shift, text 92 (\).
	ghosttyGermanBackslash = "\x1b[55;4;92u"

	// altShiftBackslashText is Alt, Shift and \ on a US layout from a
	// terminal that sends the text with the chord, as Windows Terminal does:
	// key 92 (\), Alt and Shift, text 124 (|). The | is the key's own shifted
	// character, so this is the chord alt+|, not a typed |.
	altShiftBackslashText = "\x1b[92;4;124u"

	// escPlus2 is the left Option key and 2 as Meta: ESC 2.
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
	// macSession leaves workspace 1 with the pane under test and one more.
	waitPanesOn(t, term, base, session, 1, 2, "the fixture")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((3+4))", "ready-7", shellTimeout)

	leaderKey(t, term, ghosttyGermanPipe)
	waitPanesOn(t, term, base, session, 1, 3, "the leader, then Option and 7 (|)")
	saveFrame(t, term, "leader-option-7-split")

	// Three panes side by side leave no room for a fourth, so the rest runs
	// on workspace 2, which holds one pane.
	press(t, term, "Option and 2 (ESC 2)", escPlus2)
	waitWorkspace(t, base, session, 2)
	waitPanesOn(t, term, base, session, 2, 1, "workspace 2")

	leaderKey(t, term, ghosttyGermanBackslash)
	waitPanesOn(t, term, base, session, 2, 2, "the leader, then Option, Shift and 7 (\\)")

	// alt+| is a chord no table binds. If it split, the split after it
	// makes 4 panes, not 3. The count is read once the second split is in
	// and the session has had time to show another.
	leaderKey(t, term, altShiftBackslashText)
	leaderKey(t, term, ghosttyGermanPipe)
	waitPanesAtLeast(t, term, base, session, 2, 3, "Alt, Shift and \\ after the leader, then the leader and |")
	time.Sleep(4 * insertGuard)
	if n := panesOn(t, base, session, 2); n != 3 {
		t.Fatalf("workspace 2 has %d panes, want 3: Alt, Shift and \\ after the leader split as |\n%s", n, term.Snapshot())
	}

	// Window mode binds opt+7 to workspace 7: the chord's binding wins over
	// the character it typed.
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

// waitPanesAtLeast waits for workspace ws of session sess to hold n panes or
// more.
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
