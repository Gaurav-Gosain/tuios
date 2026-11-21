package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func enterCopyModeHere(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "[")
	waitCopyCursor(t, term, what)
}

func waitCopyModeLeft(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := copyCursorCell(s)
		return !ok
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s did not leave copy mode\n%s", what, term.Snapshot())
	}
}

// TestEnterLeavesCopyMode is issue 1637. Enter leaves copy mode and the pane
// has the keyboard back. From a selection it copies the selection first. In
// the search prompt Enter still runs the search and stays in copy mode.
func TestEnterLeavesCopyMode(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	out := &lockedBuffer{}
	opts := copyColorOpts
	opts.out = out
	term := startIn(t, base, opts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo 'nee''dle-1637'; seq 1 30; echo 'rea''dy'", "ready", shellTimeout)

	enterCopyModeHere(t, term, "copy mode from the leader")
	sendKeys(t, term, tuitest.Enter)
	waitCopyModeLeft(t, term, "Enter in normal navigation")
	runInShell(t, term, "echo back-$((16+21))", "back-37", shellTimeout)

	enterCopyModeHere(t, term, "copy mode again")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "q/↵/esc") || strings.Contains(s.Text(), "q/enter/esc")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the copy mode legend does not name the exit key\n%s", term.Snapshot())
	}
	from := len(clipboardWrites(out))
	sendKeys(t, term, "k", "V")
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, term, tuitest.Enter)
	waitCopyModeLeft(t, term, "Enter in a selection")
	waitClipboardSequence(t, term, out, from, "back-37")

	enterCopyModeHere(t, term, "copy mode for the search")
	sendKeys(t, term, "?")
	if err := term.SendKeys("needle-1637"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, term, tuitest.Enter)
	row := lastRowWith(term.Screen(), "needle-1637")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r == lastRowWith(s, "needle-1637")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Enter in the search prompt did not run the search (needle row %d)\n%s", row, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Enter)
	waitCopyModeLeft(t, term, "Enter after the search")
}

// TestCopyModeExitUnboundKeepsEnter: copy_mode_exit = [] gives back the old
// behaviour, where Enter does nothing in copy mode.
func TestCopyModeExitUnboundKeepsEnter(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig+"\n[keybindings.copy_mode]\ncopy_mode_exit = []\n")
	term := startIn(t, base, copyColorOpts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo 'rea''dy'", "ready", shellTimeout)
	enterCopyModeHere(t, term, "copy mode")
	sendKeys(t, term, tuitest.Enter)
	time.Sleep(time.Second)
	if _, _, ok := copyCursorCell(term.Screen()); !ok {
		t.Fatalf("ASSERTION: Enter left copy mode with copy_mode_exit unbound\n%s", term.Snapshot())
	}
	sendKeys(t, term, "q")
	waitCopyModeLeft(t, term, "q")
}
