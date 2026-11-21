package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailNewSessionComesUpTiled is the reported repro: with [startup] tiled on,
// making a session from the rail's "+ new" used to land on a single pane
// floating at the mouse, because the daemon creates a session with AutoTiling
// false and only the one-shot boot path ever consulted the user's setting.
//
// The assertion is geometry, not pixels: the created session's only pane must
// fill the content area rather than sit at the half-size placement box
// NewWindowPlacement hands an untiled window.
func TestRailNewSessionComesUpTiled(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\n")

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "origin"}})
	killDaemon(t, base)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("the first session never opened its window: %v\n%s", err, term.Snapshot())
	}

	toggleSidebarViaPalette(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sidebarHasRow(s, "origin")
	}, uiTimeout); err != nil {
		t.Fatalf("rail never listed the attached session: %v\n%s", err, term.Snapshot())
	}

	_, row := findOnScreen(t, term, "sessions")
	mouseClick(t, term, sidebarBand-3, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: session-0", uiTimeout); err != nil {
		t.Fatalf("the rail's new-session control did not create and switch: %v\n%s", err, term.Snapshot())
	}

	content := 120 - sidebarBand
	rects := waitForSettledGeometryIn(t, base, "session-0", 1)
	pane := rects[0]
	t.Logf("created session's pane: (%d,%d) %dx%d, content area %d wide",
		pane.X, pane.Y, pane.Width, pane.Height, content)
	if pane.Width < content-4 {
		t.Errorf("the created session's pane is %d wide, want it filling the %d-column content area: it came up floating",
			pane.Width, content)
	}

	if pane.Y > 4 {
		t.Errorf("the created session's pane starts at y=%d, want it at the top of the content area", pane.Y)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "1:1")
	}, uiTimeout); err != nil {
		t.Logf("dock never showed the created session's counts:\n%s", term.Snapshot())
	}

	alive(t, term, "after creating a session from the rail")
}
