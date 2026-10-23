package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPerWorkspaceLayoutModesSurviveSwitchAndReattach: a layout mode belongs to
// the workspace that chose it, across switches and across a detach.
//
// Scrolling is picked on workspace 1 and BSP on workspace 2, the client
// switches back and forth, and session-info must report the workspace's own
// mode at every step rather than the last mode on screen. Then the client
// detaches and reattaches, and the modes must come back with the session.
//
// The bugs this watches for were all in the sync paths, where a unit test on
// one OS cannot see them: an empty-session adopt that kept the map of the
// session just left, a daemon merge that let a push without the field wipe
// every choice, a fingerprint that skipped the map so a map-only push was
// dropped as a repeat, and a fallback that retiled an unchosen workspace to
// the attaching client's [startup] layout.
func TestPerWorkspaceLayoutModesSurviveSwitchAndReattach(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"bsp\"\n")
	if out, err := tuiosCLI(t, base, "new", "e2e-perws", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}

	term := attachIn(t, base, "e2e-perws", startOpts{cols: 120, rows: 40})
	clientShows(t, base, "e2e-perws")
	waitForSettledGeometryIn(t, base, "e2e-perws", 1)
	waitLayoutMode(t, base, "e2e-perws", "bsp")

	// Workspace 1 chooses scrolling.
	runPaletteEntry(t, term, "Layout: scrolling (niri)")
	waitLayoutMode(t, base, "e2e-perws", "scrolling")

	// Workspace 2 chooses BSP, which is also the [startup] default: the map
	// entry is the only thing that says the choice was made, so a push that
	// drops the map is invisible here unless the fingerprint carries it.
	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	waitWorkspace(t, base, "e2e-perws", 2)
	runPaletteEntry(t, term, "Layout: BSP tiling")
	waitLayoutMode(t, base, "e2e-perws", "bsp")

	// Back and forth: each workspace takes its own mode, not the one the last
	// switch left on screen.
	if err := term.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("press alt+1: %v", err)
	}
	waitWorkspace(t, base, "e2e-perws", 1)
	waitLayoutMode(t, base, "e2e-perws", "scrolling")

	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	waitWorkspace(t, base, "e2e-perws", 2)
	waitLayoutMode(t, base, "e2e-perws", "bsp")

	// Detach from workspace 1, where the pane is, and reattach: the session
	// keeps the map while no client is attached, and the reattached client must
	// adopt it rather than the startup default.
	if err := term.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("press alt+1: %v", err)
	}
	waitWorkspace(t, base, "e2e-perws", 1)
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, term, "after leader d")
	if !sessionListed(t, base, "e2e-perws") {
		t.Fatalf("the session did not survive the detach")
	}

	second := attachIn(t, base, "e2e-perws", startOpts{cols: 120, rows: 40})
	clientShows(t, base, "e2e-perws")
	waitForSettledGeometryIn(t, base, "e2e-perws", 1)
	time.Sleep(insertGuard)
	waitLayoutMode(t, base, "e2e-perws", "scrolling")

	if err := second.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2 after reattach: %v", err)
	}
	waitWorkspace(t, base, "e2e-perws", 2)
	waitLayoutMode(t, base, "e2e-perws", "bsp")

	if err := second.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("press alt+1 after reattach: %v", err)
	}
	waitWorkspace(t, base, "e2e-perws", 1)
	waitLayoutMode(t, base, "e2e-perws", "scrolling")

	saveFrame(t, second, "per-workspace-layout-modes")
	alive(t, second, "after the reattached switches")
}

// TestLayoutModeMovesWithItsWorkspace: a workspace that moves takes its layout
// mode to its new number. EDIT picks scrolling and REVW picks BSP, then EDIT
// moves right: workspace 2 must be scrolling and workspace 1 BSP, both on the
// client that moved them and in the daemon's session-info.
func TestLayoutModeMovesWithItsWorkspace(t *testing.T) {
	term, base := renumberSetup(t)
	runPaletteEntry(t, term, "Layout: scrolling (niri)")
	waitLayoutMode(t, base, renumberSession, "scrolling")
	altReaches(t, term, '2', "REVW")
	runPaletteEntry(t, term, "Layout: BSP tiling")
	waitLayoutMode(t, base, renumberSession, "bsp")
	altReaches(t, term, '1', "EDIT")
	waitLayoutMode(t, base, renumberSession, "scrolling")

	moveWorkspaceKey(t, term, ">")
	waitPills(t, term, "1·REVW", "2·EDIT", "3·DPLY")
	waitLayoutMode(t, base, renumberSession, "scrolling")
	altReaches(t, term, '1', "REVW")
	waitLayoutMode(t, base, renumberSession, "bsp")
	altReaches(t, term, '2', "EDIT")
	waitLayoutMode(t, base, renumberSession, "scrolling")
	alive(t, term, "after moving a workspace with a layout mode")
}
