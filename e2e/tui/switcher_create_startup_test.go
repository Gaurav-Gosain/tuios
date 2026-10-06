package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSwitcherCreateAppliesStartup is issue #488. With [startup] tiled on, a
// session made from the session switcher (prefix S, a name no session has,
// Enter) came up floating. The switcher called SwitchToSession directly, and
// nothing applied [startup] to the empty session the daemon made. Its first
// window floated.
//
// The palette's "New session" is checked in the same fixture. It already came
// up tiled, so it is the positive half: the config, the client and the check
// of the tiling work. It also covers the create handler, which now reaches
// [startup] through the same rule as the switcher.
//
// How this could pass wrongly: session-info could report the tiling of the
// session the client left, if the check read the wrong session. Each check
// names the new session, and a window opened there must fill the content area.
func TestSwitcherCreateAppliesStartup(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\n")

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "home"}})
	killDaemon(t, base)
	waitWindowCount(t, term, 1, "home's first pane")
	waitTilingMode(t, base, "home", "tiling")

	// start_in_terminal_mode is off, so the client is in window mode already.
	time.Sleep(insertGuard)

	// The switcher, with a name that matches no session.
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open the session switcher: %v", err)
	}
	if err := term.WaitForText("Sessions", uiTimeout); err != nil {
		t.Fatalf("the session switcher did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("fresh"); err != nil {
		t.Fatalf("type the new name: %v", err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("create the session: %v", err)
	}
	if err := term.WaitForText("Session: fresh", uiTimeout); err != nil {
		t.Fatalf("the switcher did not create and switch to fresh: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "fresh")
	waitTilingMode(t, base, "fresh", "tiling")
	newWindowFills(t, base, "fresh")
	saveFrame(t, term, "switcher-create-tiled")

	// The palette's "New session", from the session the switcher made.
	runPaletteEntry(t, term, "New session")
	if err := term.WaitForText("Session: session-0", uiTimeout); err != nil {
		t.Fatalf("the palette did not create and switch to session-0: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "session-0")
	waitTilingMode(t, base, "session-0", "tiling")
	saveFrame(t, term, "palette-create-tiled")
	alive(t, term, "after creating sessions from the switcher and the palette")
}

// waitTilingMode waits for session-info to report the tiling mode want for
// session. The client sends [startup] tiling to the daemon after the switch, so
// the first read can come before it.
func waitTilingMode(t *testing.T, base, session, want string) {
	t.Helper()
	var info daemonSessionInfo
	deadline := time.Now().Add(uiTimeout)
	for {
		info = sessionInfoOf(t, base, session)
		if info.TilingMode == want || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if info.TilingMode != want {
		t.Fatalf("ASSERTION: %s is %q with %d windows, want %q from [startup] tiled",
			session, info.TilingMode, info.WindowCount, want)
	}
}

// newWindowFills opens a window in session, its first, and fails unless it
// fills most of the session. A floating first window is a box half the size.
func newWindowFills(t *testing.T, base, session string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "new-window", "-s", session); err != nil {
		t.Fatalf("new-window in %s: %v\n%s", session, err, out)
	}
	panes := waitForSettledGeometryIn(t, base, session, 1)
	info := sessionInfoOf(t, base, session)
	pane := panes[0]
	t.Logf("%s: %s, session %dx%d, pane (%d,%d) %dx%d",
		session, info.TilingMode, info.Width, info.Height, pane.X, pane.Y, pane.Width, pane.Height)
	if pane.Width < info.Width*3/4 {
		t.Fatalf("ASSERTION: the first window in %s is %d wide in a %d-wide session: it came up floating",
			session, pane.Width, info.Width)
	}
}
