package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestNavigatorCountsAHiddenScratchPane leaves session work with only its
// hidden scratch terminal (#2328). tuios ls counts that pane, so the
// navigator must count it too, and name it a scratch pane: on work's own
// screen, and from another session, where the navigator reads work from the
// daemon.
//
// The ways it can go wrong, each an assertion: the row says 0 and the card
// says "0 panes", so the pane reads as a ghost; the count adds the scratch
// pane as a plain pane, so nothing says what it is.
func TestNavigatorCountsAHiddenScratchPane(t *testing.T) {
	base := t.TempDir()
	term := startScratchOuter(t, base)
	normal := paneIDs(t, base, "work")
	if len(normal) != 1 {
		t.Fatalf("ordinary panes at startup = %v, want one", normal)
	}
	toggleScratch(t, term)
	scratch := waitScratch(t, term, base, false, false, "shown")
	toggleScratch(t, term)
	assertHiddenEverywhere(t, term, base, scratch.ID)
	if out, err := tuiosCLI(t, base, "close-window", "--session", "work", normal[0]); err != nil {
		t.Fatalf("close the ordinary pane: %v\n%s", err, out)
	}
	waitCount(t, term, 0, "with only the hidden scratch terminal left")

	// want is what the navigator must show for work: its row's count, and on
	// work's own screen also the card line under the row.
	wantRow := func(where string, want ...string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return containsAll(s.Text(), want...)
		}, uiTimeout); err != nil {
			saveFrame(t, term, "navigator-scratch-missing")
			t.Fatalf("ASSERTION: %s, the navigator does not count work's scratch pane: %v\n%s", where, err, term.Snapshot())
		}
	}
	sendKeys(t, term, tuitest.Ctrl('b'), "/")
	waitScreen(t, term, "the navigator never showed", "Panes", "work")
	wantRow("on work", "0 + 1 scratch", "1 scratch pane")
	saveCockpitFrames(t, term, "navigator-scratch-current")
	sendKeys(t, term, tuitest.Esc)

	if out, err := tuiosCLI(t, base, "new", "other", "--detach"); err != nil {
		t.Fatalf("make session other: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "switch-session", "other"); err != nil {
		t.Fatalf("switch to other: %v\n%s", err, out)
	}
	waitCount(t, term, 1, "on session other")
	sendKeys(t, term, tuitest.Ctrl('b'), "/")
	waitScreen(t, term, "the navigator never showed", "Panes", "work", "other")
	wantRow("from other", "0 + 1 scratch")
	saveCockpitFrames(t, term, "navigator-scratch-other")
	alive(t, term, "after the navigator")
}
