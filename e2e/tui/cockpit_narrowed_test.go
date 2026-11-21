package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitNarrowedListSaysSoAndEscShowsAll narrows the agents view with a
// selector term, name:scratch. The header says the list is narrowed, with
// how many of the fleet it shows, and how to show the rest. esc, which the
// key bar names "clear search", brings every agent back, and the header no
// longer says it is narrowed. Then an empty search after a second selector
// brings them back too.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with the esc clear
// emptying the selector's text and not the parsed selector, as before, the
// list stays on one agent with the search box empty and esc naming close;
// with the chip not drawn, the wait for it fails.
func TestCockpitNarrowedListSaysSoAndEscShowsAll(t *testing.T) {
	term, _, _, _ := startCockpitFleet(t, 200, 55, nil)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "scratch") >= 0 && fleetRowWith(s, "docs-cli") >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("the fleet never showed: %v\n%s", err, term.Snapshot())
	}
	search := func(text string) {
		t.Helper()
		if err := term.SendKeys("/"); err != nil {
			t.Fatal(err)
		}
		if text != "" {
			if err := term.Type(text); err != nil {
				t.Fatal(err)
			}
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatal(err)
		}
	}
	search("name:scratch")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "docs-cli") < 0 && fleetRowWith(s, "scratch") >= 0 &&
			screenHas(s, "1 of 12 agents", "esc shows all", "esc clear search")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the narrowed list does not say how much it shows: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-narrowed")

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "docs-cli") >= 0 && fleetRowWith(s, "scratch") >= 0 &&
			!strings.Contains(s.Text(), "esc shows all")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not bring every agent back: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "cockpit-esc-shows-all")

	search("name:docs-cli")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "scratch") < 0 && screenHas(s, "1 of 12 agents")
	}, uiTimeout); err != nil {
		t.Fatalf("the second selector did not narrow the list: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("/"); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys(tuitest.Ctrl('u'), tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return fleetRowWith(s, "scratch") >= 0 && fleetRowWith(s, "docs-cli") >= 0 && !strings.Contains(s.Text(), "esc shows all")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: an empty search did not bring every agent back: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the list was narrowed and shown whole again")
}
