package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// writeConfig drops a config.toml into the isolation root's XDG_CONFIG_HOME so
// the tuios process started against that root loads it at boot.
func writeConfig(t *testing.T, base, body string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("writeConfig: mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writeConfig: write: %v", err)
	}
}

// TestStartupAllThreeSettings is the full combined behavior the [startup]
// section exists for: with open_default_window, tiled and start_in_terminal_mode
// all on, launching tuios drops the user straight into a session that already
// has one terminal open, is tiled, and is focused in terminal mode so typing
// reaches the shell. Windows opened afterwards tile without ever toggling tiling.
//
// It runs against a daemon session so the placed geometry can be read back with
// `list-windows --json`, which is what makes "the panes are tiled" a hard,
// non-visual assertion (no overlap, none left at the full-screen box) on top of
// the rendered snapshot. Terminal mode is proved behaviorally: a shell command
// is typed at boot with no manual mode switch, and its output must appear.
func TestStartupAllThreeSettings(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nstart_in_terminal_mode = true\n")

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e"}})
	killDaemon(t, base)

	// Setting 1: a window is already open, so the empty-session welcome hint is
	// gone and the dock reports one window without any keystroke.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && !strings.Contains(s.Text(), welcomeHint)
	}, bootTimeout); err != nil {
		t.Fatalf("startup did not open a default window: %v\n%s", err, term.Snapshot())
	}

	// Setting 2: with one window and tiling on, that window fills the screen
	// (a tiled single pane), rather than sitting at the half-size floating box.
	boot := waitForSettledGeometry(t, base, 1)
	if w := boot[0]; w.Width < 100 {
		t.Fatalf("the startup window is not tiled to full width: (%d,%d) %dx%d",
			w.X, w.Y, w.Width, w.Height)
	}
	t.Logf("rendered screen at boot (one terminal open, tiled full-screen):\n%s", term.Snapshot())

	// Setting 3: we are already in terminal mode, so a command typed WITHOUT any
	// manual mode switch reaches the shell. In window-management mode the same
	// keys would be swallowed as window-manager bindings and never run, so the
	// command's computed output appearing proves input landed in the terminal.
	if err := term.SendKeys("echo STARTUP_TERMINAL_OK"); err != nil {
		t.Fatalf("type shell command at boot: %v", err)
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send Enter: %v", err)
	}
	if err := term.WaitForText("STARTUP_TERMINAL_OK", uiTimeout); err != nil {
		t.Fatalf("typed input did not reach the shell at boot (not in terminal mode): %v\n%s",
			err, term.Snapshot())
	}
	t.Logf("rendered screen after typing at boot (terminal mode, no manual switch):\n%s", term.Snapshot())

	// Open two more windows WITHOUT ever toggling tiling. If the session started
	// tiled, they join the layout and partition the screen; if it had started
	// floating they would stack at the same half-size box.
	for i := 2; i <= 3; i++ {
		out, err := tuiosCLI(t, base, "run-command", "NewWindow")
		if err != nil {
			t.Fatalf("run-command NewWindow #%d: %v\n%s", i, err, out)
		}
		waitWindowCount(t, term, i, "after opening window")
	}

	rects := waitForSettledGeometry(t, base, 3)
	for _, r := range rects {
		if r.Width >= 120 {
			t.Errorf("window %s spans the full width (%d): it never tiled", r.ID, r.Width)
		}
	}
	for a := 0; a < len(rects); a++ {
		for b := a + 1; b < len(rects); b++ {
			if geomOverlap(rects[a], rects[b]) {
				t.Errorf("windows overlap, so they are floating not tiled: %s (%d,%d %dx%d) and %s (%d,%d %dx%d)",
					rects[a].ID, rects[a].X, rects[a].Y, rects[a].Width, rects[a].Height,
					rects[b].ID, rects[b].X, rects[b].Y, rects[b].Width, rects[b].Height)
			}
		}
	}
	for _, r := range rects {
		t.Logf("tiled window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}

	// Rendered capture of the three tiled panes for the record.
	t.Logf("rendered screen with three panes tiled on start (tiling never toggled by hand):\n%s", term.Snapshot())
	alive(t, term, "after verifying startup tiling")
}

// TestStartupDefaultsPreserved confirms the opt-in nature: with no [startup]
// section, tuios still boots to the empty-session welcome screen exactly as
// before, so the new settings default off.
func TestStartupDefaultsPreserved(t *testing.T) {
	term, _ := start(t, startOpts{})

	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("default boot no longer shows the welcome screen: %v\n%s", err, term.Snapshot())
	}
	if n := countWindows(term.Screen()); n > 0 {
		t.Fatalf("default boot opened %d windows, expected an empty session", n)
	}
	// Give it a moment and re-check nothing auto-opened late.
	time.Sleep(500 * time.Millisecond)
	if !strings.Contains(term.Screen().Text(), welcomeHint) {
		t.Fatalf("welcome screen vanished without a window being opened\n%s", term.Snapshot())
	}
	alive(t, term, "after default boot")
}
