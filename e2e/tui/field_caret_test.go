package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRenameEditsAtTheCaret: the rename dialog is a field with a caret, like
// every short field. Typing "abd", pressing left once and typing "c" names the
// window "abcd".
//
// How this could pass wrongly, written down first:
//   - The name could be read off the dialog rather than the window. It is read
//     once the dialog has closed, from the window's title.
//   - The field could start with text in it, so "abcd" lands after it. ctrl+u
//     clears the field first, and the title must be "abcd", not end with it.
//   - "abcd" could be on screen for another reason. No pane holds it: the
//     window runs a fresh shell.
//
// Negative control (NEGATIVE_CONTROLS.md): the R7 5d tip, where the rename
// field appends and left does nothing, names the window "abdc".
func TestRenameEditsAtTheCaret(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	sendKeys(t, term, "r")
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('u'), "a", "b", "d", tuitest.Left, "c", tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !renameDialogUp(s) && (strings.Contains(s.Text(), "abcd") || strings.Contains(s.Text(), "abdc"))
	}, uiTimeout); err != nil {
		t.Fatalf("the rename never committed: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); !strings.Contains(text, "abcd") || strings.Contains(text, "abdc") {
		t.Errorf("ASSERTION: left then c did not type at the caret; the window is not named abcd\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "renamed")
	alive(t, term, "after renaming at the caret")
}

// TestDialogFieldTakesTheComposerKeys: a dialog's field takes home, end and
// alt+d, the keys an agent answer field takes. In the rename dialog, typing
// "owl-lark", home, "z", alt+d, end and "s" names the window "z-larks": home
// puts "z" first, alt+d deletes the word "owl" after the caret, and end puts
// "s" last.
//
// How this could pass wrongly, written down first:
//   - The name could be read off the dialog rather than the window. It is read
//     once the dialog has closed.
//   - The field could start with text in it. ctrl+u clears it first.
//   - A line without these keys could still land on a name that looks right.
//     Without them the same keys name the window "owl-larkzs", which the test
//     rejects by name.
//   - alt+d could reach the shell behind the dialog instead. The dialog holds
//     the keyboard until enter, and the name shows the word went.
//
// Negative control (NEGATIVE_CONTROLS.md): the R7 6b tip, where home, end and
// alt+d do nothing in a dialog field, names the window "owl-larkzs".
func TestDialogFieldTakesTheComposerKeys(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	sendKeys(t, term, "r")
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('u'), "owl-lark", tuitest.Home, "z", tuitest.Alt('d'), tuitest.End, "s", tuitest.Enter)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return !renameDialogUp(s) && (strings.Contains(text, "z-larks") || strings.Contains(text, "owl-lark"))
	}, uiTimeout); err != nil {
		t.Fatalf("the rename never committed: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); !strings.Contains(text, "z-larks") || strings.Contains(text, "owl-lark") {
		t.Errorf("ASSERTION: home, alt+d and end did not edit the field; the window is not named z-larks\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "renamed")
	alive(t, term, "after the composer keys in a dialog field")
}

// TestCopyModeSearchEditsAtTheCaret: copy mode's search line is a field with a
// caret too. Typing "kstrel", moving left five times and typing "e" searches
// for "kestrel", which the pane holds once.
//
// How this could pass wrongly, written down first:
//   - The query could be read off the shell's echo of the command. The text is
//     printed with its letters split in the command, so only the output holds
//     "kestrel", and the test reads the prompt's "/kestrel" and its "[1/1]".
//   - The search could match because the letters arrived in order anyway. On a
//     line that only appends, the same keys search for "kstrele", which
//     matches nothing.
//
// Negative control (NEGATIVE_CONTROLS.md): the R7 6a tip, where the copy mode
// search appends and left does nothing, searches for "kstrele".
func TestCopyModeSearchEditsAtTheCaret(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	term := startIn(t, base, copyColorOpts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf 'kes''trel-wing\n'; echo 'fil''led'`, "filled", shellTimeout)
	enterCopyModeHere(t, term, "copy mode for the search")

	sendKeys(t, term, "/")
	sendKeys(t, term, "k", "s", "t", "r", "e", "l")
	waitScreen(t, term, "the search prompt never read kstrel", "/kstrel")
	sendKeys(t, term, tuitest.Left, tuitest.Left, tuitest.Left, tuitest.Left, tuitest.Left)
	sendKeys(t, term, "e")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "/kestrel") && strings.Contains(s.Text(), "[1/1]")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: left then e did not type at the caret; the search is not for kestrel: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "copy-search")
	sendKeys(t, term, tuitest.Esc)
	alive(t, term, "after editing the copy mode search at the caret")
}
