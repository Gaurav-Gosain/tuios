package tuie2e

import (
	"fmt"
	"testing"
)

// TestStartupTilingOnADaemonBuiltSession is the reported bug, end to end with
// the real binary: with startup.tiled on, attaching to a session the daemon
// already built comes up untiled.
//
// The session is made the way a user makes one before attaching to it, so it
// holds the window the daemon opened for it. That window is what used to make
// the [startup] settings look inapplicable: the session was not empty, so every
// one of them was skipped. Scrolling mode failed for a second reason on top of
// that - the client turned tiling on without telling the daemon, and the
// daemon's next state took it away again - which is why all three modes are
// driven here.
func TestStartupTilingOnADaemonBuiltSession(t *testing.T) {
	for _, mode := range []string{"bsp", "master-stack", "scrolling"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, fmt.Sprintf(
				"[startup]\nopen_default_window = true\ntiled = true\nlayout = %q\n", mode))

			if out, err := tuiosCLI(t, base, "new", "-d", "startup"); err != nil {
				t.Fatalf("create the detached session: %v\n%s", err, out)
			}
			term := attachIn(t, base, "startup", startOpts{cols: 120, rows: 40})

			// The daemon opened the session's window already, so the option that
			// opens one has nothing to do: one pane, not two.
			rects := waitForSettledGeometryIn(t, base, "startup", 1)
			r := rects[0]

			info, err := daemonInfo(base, "startup")
			if err != nil {
				t.Fatalf("session-info: %v", err)
			}
			if info.TilingMode != "tiling" {
				t.Errorf("the session is %q, not tiling, in %s mode", info.TilingMode, mode)
			}

			// A tiled pane starts at the top left of the layout box and fills its
			// height. The floating box a pane keeps when nothing tiles it is half
			// the screen and inset from both edges, which is what the bug looked
			// like on screen.
			if r.X != 0 || r.Y != 0 || r.Height < 30 {
				t.Errorf("the pane was never tiled in %s mode: (%d,%d) %dx%d",
					mode, r.X, r.Y, r.Width, r.Height)
			}
			t.Logf("tiled pane in %s mode: (%d,%d) %dx%d", mode, r.X, r.Y, r.Width, r.Height)
			t.Logf("rendered screen on attach (%s mode, started tiled):\n%s", mode, term.Snapshot())
			alive(t, term, "after attaching to a daemon-built session")
		})
	}
}

// TestStartupTilingOnANewSession is the reported bug in the shape the owner hit
// it: a session started fresh, which opens its default window through the
// client rather than the daemon.
//
// The window the client asks for is created by the daemon, and the daemon
// broadcasts the session back when it has made it. That broadcast carries
// AutoTiling as the daemon holds it, so a client that turned tiling on without
// telling the daemon has it turned straight off again a moment later. Only
// scrolling mode did that, because its branch of the toggle returned before the
// sync; the other two are here to show the shape is not mode-specific.
func TestStartupTilingOnANewSession(t *testing.T) {
	for _, mode := range []string{"bsp", "master-stack", "scrolling"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, fmt.Sprintf(
				"[startup]\nopen_default_window = true\ntiled = true\nlayout = %q\n", mode))

			term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "startup"}})
			waitWindowCount(t, term, 1, "after starting a new session")

			rects := waitForSettledGeometryIn(t, base, "startup", 1)
			r := rects[0]

			info, err := daemonInfo(base, "startup")
			if err != nil {
				t.Fatalf("session-info: %v", err)
			}
			if info.TilingMode != "tiling" {
				t.Errorf("the session is %q, not tiling, in %s mode", info.TilingMode, mode)
			}
			if r.X != 0 || r.Y != 0 || r.Height < 30 {
				t.Errorf("the startup window was never tiled in %s mode: (%d,%d) %dx%d",
					mode, r.X, r.Y, r.Width, r.Height)
			}
			t.Logf("tiled pane in %s mode: (%d,%d) %dx%d", mode, r.X, r.Y, r.Width, r.Height)
			t.Logf("rendered screen at boot (%s mode, started tiled):\n%s", mode, term.Snapshot())
			alive(t, term, "after starting tiled")
		})
	}
}
