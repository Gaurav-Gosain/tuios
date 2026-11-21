package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const noMatchQuery = "zzznomatch"

func requireStillOpen(t *testing.T, term *tuitest.Terminal, title, what string) {
	t.Helper()
	if txt := term.Screen().Text(); !strings.Contains(txt, title) {
		t.Fatalf("%s dismissed the panel and threw the query away:\n%s", what, term.Snapshot())
	}
}

// TestPaletteEnterOnNoMatch is the command palette's copy of the bug.
func TestPaletteEnterOnNoMatch(t *testing.T) {
	term, _ := start(t, startOpts{cols: 160, rows: 45})
	waitBoot(t, term)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "for the command palette")
	if err := term.SendKeys(noMatchQuery); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitForText("No matching commands", uiTimeout); err != nil {
		t.Fatalf("the empty line never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := term.WaitForText("Nothing to run: no command matches", uiTimeout); err != nil {
		t.Fatalf("nothing said why the key did nothing: %v\n%s", err, term.Snapshot())
	}
	requireStillOpen(t, term, paletteTitle, "enter on a query matching no command")
}

// TestThemePickerEnterOnNoMatch is the theme picker's copy, which was the worst
// of them: it closed through the path that keeps the applied theme, so the live
// preview from the last query that did match rows stayed on screen unpersisted,
// with the picker gone and the Esc that reverts no longer there to press.
func TestThemePickerEnterOnNoMatch(t *testing.T) {
	term, _ := start(t, startOpts{cols: 160, rows: 45})
	waitBoot(t, term)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "for the command palette")
	if err := term.SendKeys("Theme picker"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitForText("Theme picker", uiTimeout); err != nil {
		t.Fatalf("palette never filtered: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the theme picker: %v", err)
	}
	if err := term.WaitForText("themes", uiTimeout); err != nil {
		t.Fatalf("the theme picker did not open: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("catppuccin"); err != nil {
		t.Fatalf("type a matching query: %v", err)
	}
	if err := term.WaitForText("catppuccin", uiTimeout); err != nil {
		t.Fatalf("no theme matched a query that should: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(noMatchQuery); err != nil {
		t.Fatalf("type a non-matching query: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := term.WaitForText("Nothing to apply: no theme matches", uiTimeout); err != nil {
		t.Fatalf("nothing said why the key did nothing: %v\n%s", err, term.Snapshot())
	}
	requireStillOpen(t, term, "No matching themes", "enter on a query matching no theme")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "No matching themes")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close the picker: %v\n%s", err, term.Snapshot())
	}
}

// TestWorkspaceSwitcherEnterOnNoMatch is the third copy. Its Enter and its
// mouse click were the same three lines written twice; they now share one
// entry point, so this covers both.
func TestWorkspaceSwitcherEnterOnNoMatch(t *testing.T) {
	term, _ := start(t, startOpts{cols: 160, rows: 45})
	waitBoot(t, term)

	if err := term.SendKeys(tuitest.Ctrl('b'), "W"); err != nil {
		t.Fatalf("open the workspace switcher: %v", err)
	}
	if err := term.WaitForText("Go to", uiTimeout); err != nil {
		t.Fatalf("the workspace list did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(noMatchQuery); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := term.WaitForText("Nothing to go to: no place matches", uiTimeout); err != nil {
		t.Fatalf("nothing said why the key did nothing: %v\n%s", err, term.Snapshot())
	}
	requireStillOpen(t, term, "Go to", "enter on a query matching no workspace")
}
