package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Keys with modifiers, as xterm sends them: CSI 1 ; mod final, where mod is 1
// plus shift 1, alt 2 and ctrl 4.
const (
	shiftLeft    tuitest.Key = "\x1b[1;2D"
	shiftHome    tuitest.Key = "\x1b[1;2H"
	altShiftLeft tuitest.Key = "\x1b[1;4D"
)

// TestComposerEditsLikeATextField drives the reply editor in the agents view
// with the mouse and the keys a text field takes: a click places the cursor,
// a click past a line's end puts it at the end, a double click selects a word
// and typing replaces it, undo takes the replace and the typing back as one
// step, shift with a word jump selects and backspace deletes the selection, a
// drag selects, ctrl+c copies the selection into the paste buffers, a triple
// click selects the line and ctrl+x cuts it. Frames of the selection drawn are
// saved under artifactDir.
//
// How it could pass wrongly, written down first:
//   - The draft could change through the keys alone and the mouse do nothing.
//     Each mouse step is checked by the text a key typed at the place the
//     mouse put the cursor, which differs from where the keys would.
//   - A click could map to the wrong cell (the gutter not taken off, the
//     frame's line not the draft's row). The X must land right before "beta",
//     not one cell to either side.
//   - Typing over a selection could insert next to it and leave it. The word
//     must be gone from the screen, not only the new word there.
//   - Undo could take back only the typing and leave the deleted word gone.
//     One ctrl+z must bring back "gamma" whole.
//   - ctrl+c with a selection could clear the draft, as it does with none.
//     The draft must still be on screen after it, and the buffer hold the
//     selected text exactly.
//   - The copy could reach the clipboard only. show-buffer reads the paste
//     buffers, which the client fills through the same call as a yank.
//   - A click outside the editor (on the thread above) could move its cursor.
//     A click on the header line must leave the next typed key at the end.
func TestComposerEditsLikeATextField(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 45, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	send := func(what string, keys ...any) {
		t.Helper()
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		time.Sleep(mouseGap)
	}
	// The screen's text drops a line's trailing spaces, so a draft that
	// ends in one is checked by what comes before it.
	draft := func(what, want string, gone ...string) {
		t.Helper()
		waitText(t, term, what, want)
		for _, g := range gone {
			if strings.Contains(term.Screen().Text(), g) {
				t.Errorf("ASSERTION: %s: %q is still on screen\n%s", what, g, term.Snapshot())
			}
		}
	}
	send("open the agents view", tuitest.Ctrl('b'), "i")
	waitText(t, term, "the closed reply area", "Reply to docs-cli", "Write a reply.")
	row, col := findText(t, term, "Write a reply.")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitText(t, term, "the open reply editor", "Type your reply.")

	send("type the draft", "alpha beta gamma")
	draft("the typed draft", "alpha beta gamma")

	// A click on the b of beta, then a key: the X goes right before it.
	row, col = findText(t, term, "alpha beta gamma")
	mouseClick(t, term, col+6, row, tuitest.MouseLeft, 0)
	send("type at the click", "X")
	draft("a key after a click", "alpha Xbeta gamma")

	// A click past the line's end, then a key: it goes at the end.
	mouseClick(t, term, col+60, row, tuitest.MouseLeft, 0)
	send("type at the end", "!")
	draft("a key after a click past the end", "alpha Xbeta gamma!")

	// A click on the header above the editor leaves the cursor where it was.
	hr, hc := findText(t, term, "Reply to docs-cli")
	mouseClick(t, term, hc+2, hr, tuitest.MouseLeft, 0)
	send("type after a click on the header", "?")
	draft("a key after a click outside the editor", "alpha Xbeta gamma!?")
	send("drop the marks", tuitest.Backspace, tuitest.Backspace)
	draft("the draft without the marks", "alpha Xbeta gamma", "gamma!")

	// A double click selects gamma; typing replaces it.
	mouseClick(t, term, col+14, row, tuitest.MouseLeft, 0)
	mouseClick(t, term, col+14, row, tuitest.MouseLeft, 0)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "composer-word-selected-160x45")
	send("type over the word", "delta")
	draft("typing over a double click", "alpha Xbeta delta", "gamma")

	// One undo brings the word back whole.
	send("undo", tuitest.Ctrl('z'))
	draft("undo of a replace", "alpha Xbeta gamma", "delta")

	// Shift with a word jump selects the word before the cursor.
	send("go to the end", tuitest.End)
	send("select a word back", altShiftLeft)
	send("delete the selection", tuitest.Backspace)
	draft("backspace over a selection", "› alpha Xbeta", "gamma")

	// A drag over Xbeta selects it, and ctrl+c copies it without a change.
	mouseDrag(t, term, col+6, row, col+11, row, tuitest.MouseLeft, 0)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "composer-drag-selected-160x45")
	send("copy", tuitest.Ctrl('c'))
	waitBuffer(t, base, "the copied selection", "Xbeta")
	draft("the draft after a copy", "alpha Xbeta")

	// shift+left after the drag shrinks the selection by one; typing
	// replaces what is left.
	send("shrink the selection", shiftLeft)
	send("type over it", "Y")
	draft("typing over a shrunk selection", "› alpha Ya", "Xbet")

	// shift+home selects to the line's start; a triple click selects the
	// whole line, and ctrl+x cuts it.
	send("select to the start", shiftHome)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "composer-shift-home-160x45")
	for range 3 {
		mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	}
	send("cut", tuitest.Ctrl('x'))
	waitBuffer(t, base, "the cut line", "alpha Ya ")
	waitText(t, term, "the empty editor after the cut", "Type your reply.")
}

// waitBuffer waits for the newest paste buffer to hold want.
func waitBuffer(t *testing.T, base, what, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		if got = mustCLIOr(t, base, "show-buffer"); got == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: the paste buffer holds %q, want %q", what, got, want)
}
