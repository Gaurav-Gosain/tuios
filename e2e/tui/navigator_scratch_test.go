package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
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

// TestNavigatorCountsAScratchPaneOnAHost leaves session work on the linked
// machine alpha with only its hidden scratch pane. The navigator of a client
// on this machine must count it apart and name it from the host listing,
// before its detail load reads the session. The test holds that load, so the
// row on screen is the one the listing built.
//
// The way it can go wrong, an assertion: the row counts the scratch pane as
// a plain pane and says 1 until the load lands.
func TestNavigatorCountsAScratchPaneOnAHost(t *testing.T) {
	// alpha's client draws the rail pane, so work also holds the rail pane.
	// It is in the session's window count and is no pane of the layout.
	f := startGlobalFleetWith(t, map[string]string{"beta": "someone@betabox"},
		"\n[appearance.sidebar]\nenabled = true\nsections = \"sessions,terminals,pane\"\n\n"+
			"[appearance.sidebar.pane]\ntitle = \"railtv\"\nheight = 6\ncommand = \"sleep 600\"\n")
	far := startIn(t, f.alpha, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitBoot(t, far)
	newWindow(t, far)
	normal := paneIDs(t, f.alpha, "work")
	if len(normal) != 1 {
		t.Fatalf("ordinary panes of work = %v, want one", normal)
	}
	toggleScratch(t, far)
	scratch := waitScratch(t, far, f.alpha, false, false, "shown on alpha")
	toggleScratch(t, far)
	assertHiddenEverywhere(t, far, f.alpha, scratch.ID)
	if out, err := tuiosCLI(t, f.alpha, "close-window", "--session", "work", normal[0]); err != nil {
		t.Fatalf("close the ordinary pane on alpha: %v\n%s", err, out)
	}
	waitCount(t, far, 0, "with only the hidden scratch terminal left on alpha")
	waitUntil(t, "work on alpha holds the rail pane", uiTimeout, func() (bool, string) {
		out, err := tuiosCLI(t, f.alpha, "list-windows", "--json", "--rail", "--session", "work")
		return err == nil && strings.Contains(out, `"rail": "sleep 600"`), out
	})

	hold := filepath.Join(f.here, "hold-nav")
	if err := os.WriteFile(hold, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(hold) })
	term := startIn(t, f.here, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44,
		env: append([]string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0", knob("hold_nav=" + hold)}, f.env...)})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached home: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "/")
	waitScreen(t, term, "the navigator never showed alpha's work", "Panes", "work")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "work @ alpha", "0 + 1 scratch", "Reading the other sessions")
	}, uiTimeout); err != nil {
		saveFrame(t, term, "navigator-host-scratch-missing")
		t.Fatalf("ASSERTION: the navigator does not count work's scratch pane on alpha: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "navigator-host-scratch")

	// tuios ls counts the same panes: on alpha, and from here across the link.
	if ls, err := tuiosCLI(t, f.alpha, "ls"); err != nil || !strings.Contains(ls, "1 scratch pane") {
		t.Errorf("ASSERTION: tuios ls on alpha does not count work as 1 scratch pane: %v\n%s", err, ls)
	}
	all, err := tuiosCLIEnv(t, f.here, f.env, "ls", "--all-hosts")
	// Each host is a header line and then its own table.
	workRow, host := "", ""
	for line := range strings.SplitSeq(all, "\n") {
		if name := strings.TrimSpace(line); name == "local" || name == "alpha" || name == "beta" {
			host = name
		}
		if host == "alpha" && strings.Contains(line, "work") {
			workRow = line
		}
	}
	if err != nil || !strings.Contains(workRow, "1 scratch pane") {
		t.Errorf("ASSERTION: tuios ls --all-hosts does not count work on alpha as 1 scratch pane: %v\n%s", err, all)
	}
	alive(t, term, "after the navigator")
}
