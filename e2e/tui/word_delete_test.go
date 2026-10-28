package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The text prompts took backspace and ctrl+u and nothing else, so alt+backspace
// and ctrl+w, which every shell and editor reads as "delete the word before the
// cursor", did nothing at all. On macOS with the Option key sent as Alt the
// terminal writes ESC DEL for Option+Backspace, which is tuitest's
// Alt(Backspace), so that is the chord these tests press.
//
// The rename editor and the filtering overlays are two code paths that each had
// their own copy of the edit rule, so each gets its own test: a fix to one would
// not show the other still broken.

// TestOptionBackspaceDeletesAWordInTheRenameEditor types a two-word name, takes
// the last word off with Option+Backspace, types another and commits. The
// committed name is the assertion, not the field: a key the editor ignores
// leaves the field showing what was typed, and only the result tells "ignored"
// from "deleted".
func TestOptionBackspaceDeletesAWordInTheRenameEditor(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename editor: %v", err)
	}
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}

	// The editor may open on the window's current name, so start from nothing.
	if err := term.SendKeys(tuitest.Ctrl('u'), "one two"); err != nil {
		t.Fatalf("type the name: %v", err)
	}
	if err := term.WaitForText("one two", uiTimeout); err != nil {
		t.Fatalf("the editor did not take the typed name: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Alt(tuitest.Backspace)); err != nil {
		t.Fatalf("press Option+Backspace: %v", err)
	}
	// The word is gone and the space before it is not: a rule that took the
	// space too would run the next word into the first.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "one two")
	}, uiTimeout); err != nil {
		t.Fatalf("Option+Backspace left the last word in the editor: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("three", tuitest.Enter); err != nil {
		t.Fatalf("finish the name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "one three") && !renameDialogUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the window was not named %q: %v\n%s", "one three", err, term.Snapshot())
	}
}

// TestControlWDeletesAWordInTheRenameEditor is the same rule on the readline
// spelling of the chord, with trailing spaces: the word before the cursor is
// the one to delete even when blanks stand between it and the cursor.
func TestControlWDeletesAWordInTheRenameEditor(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename editor: %v", err)
	}
	if err := term.WaitFor(renameDialogUp, uiTimeout); err != nil {
		t.Fatalf("the rename dialog never opened: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('u'), "alpha beta  "); err != nil {
		t.Fatalf("type the name: %v", err)
	}
	if err := term.WaitForText("alpha beta", uiTimeout); err != nil {
		t.Fatalf("the editor did not take the typed name: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('w'), "gamma", tuitest.Enter); err != nil {
		t.Fatalf("delete a word and finish the name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "alpha gamma") && !renameDialogUp(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the window was not named %q: %v\n%s", "alpha gamma", err, term.Snapshot())
	}
}

// TestOptionBackspaceDeletesAWordInTheCommandPalette covers the filtering
// overlays, which share one edit rule. The palette stands for them. The query
// matches nothing, so no result row can carry the text the assertions read.
func TestOptionBackspaceDeletesAWordInTheCommandPalette(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "on ctrl+p")

	if err := term.SendKeys("zqx1 zqx2 zqx3"); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	if err := term.WaitForText("zqx1 zqx2 zqx3", uiTimeout); err != nil {
		t.Fatalf("the palette did not take the typed query: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Alt(tuitest.Backspace)); err != nil {
		t.Fatalf("press Option+Backspace: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "zqx1 zqx2") && !strings.Contains(text, "zqx3")
	}, uiTimeout); err != nil {
		t.Fatalf("Option+Backspace did not take the last word off the query: %v\n%s", err, term.Snapshot())
	}
	// It is an edit, not a way out: the palette is still the thing on screen.
	if !strings.Contains(term.Screen().Text(), paletteTitle) {
		t.Fatalf("the palette closed on Option+Backspace\n%s", term.Snapshot())
	}
}
