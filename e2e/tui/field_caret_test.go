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
