package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestWindowCreateAndClose walks window creation and closing and asserts the
// dock's window count follows, so a window that was drawn but never registered
// (or closed visually but left behind) is caught.
func TestWindowCreateAndClose(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	for i := 1; i <= 3; i++ {
		newWindow(t, term)
		waitWindowCount(t, term, i, fmt.Sprintf("after creating window %d", i))
	}
	for i := 2; i >= 0; i-- {
		if err := term.SendKeys("x"); err != nil {
			t.Fatalf("close window: %v", err)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after closing down to %d", i))
	}
	// With every window gone the welcome screen must come back.
	if err := term.WaitForText(welcomeHint, uiTimeout); err != nil {
		t.Fatalf("welcome screen did not return after closing every window: %v\n%s",
			err, term.Snapshot())
	}
	alive(t, term, "after create/close cycle")
}

// TestRenameWindow renames a window and asserts the new name reaches both the
// window title bar and the dock, which are rendered from separate paths.
func TestRenameWindow(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("open rename: %v", err)
	}
	// The rename editor replaces the title bar with a prompt; wait for the
	// cursor to appear there rather than sleeping.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "_")
	}, uiTimeout); err != nil {
		t.Fatalf("rename editor never opened: %v\n%s", err, term.Snapshot())
	}

	const name = "RENAMEDWIN"
	if err := term.SendKeys(name, tuitest.Enter); err != nil {
		t.Fatalf("type name: %v", err)
	}
	if err := term.WaitForText(name, uiTimeout); err != nil {
		t.Fatalf("renamed title never rendered: %v\n%s", err, term.Snapshot())
	}

	// Minimizing puts the window in the dock, where the custom name must also
	// appear: that is a different render path from the title bar.
	if err := term.SendKeys("m"); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	if err := term.WaitForText(":"+name, uiTimeout); err != nil {
		t.Fatalf("renamed window never appeared in the dock: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after rename")
}

// TestFocusCycleWithRapidKeyRepeat cycles focus across several windows, first at
// a human pace and then as a burst of key repeats with no waiting in between.
//
// The burst is the interesting half. Focus changes invalidate render caches and,
// in daemon mode, trigger a state sync that resizes emulators; a burst of them
// lands those on top of each other. The assertion is that after the storm every
// window's content is intact and the UI still responds, not merely that tuios
// is alive.
func TestFocusCycleWithRapidKeyRepeat(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--shared-borders"}})
	waitBoot(t, term)

	markers := make([]string, 0, 3)
	for i := 1; i <= 3; i++ {
		newWindow(t, term)
		waitWindowCount(t, term, i, fmt.Sprintf("creating window %d", i))
		enterTerminalMode(t, term)
		marker := fmt.Sprintf("WINMARK-%d", i)
		runInShell(t, term, fmt.Sprintf("echo WINMARK-$((%d))", i), marker, 20*time.Second)
		markers = append(markers, marker)
		leaveTerminalMode(t, term)
	}

	enableTiling(t, term, markers...)

	// Paced cycling: a full trip around must return every pane intact.
	for i := range 6 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatalf("paced tab %d: %v", i, err)
		}
		waitForAll(t, term, uiTimeout, fmt.Sprintf("after paced focus change %d", i+1), markers...)
	}

	// Rapid repeat: 40 focus changes with no waiting, the shape of a held-down
	// Tab key.
	for range 40 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatalf("rapid tab: %v", err)
		}
	}
	alive(t, term, "during rapid focus cycling")
	waitForAll(t, term, 15*time.Second, "after 40 rapid focus changes", markers...)

	// Backwards too, since previous-window is a separate action.
	for range 20 {
		if err := term.SendKeys(tuitest.Ctrl('b'), "p"); err != nil {
			t.Fatalf("rapid prev: %v", err)
		}
	}
	waitForAll(t, term, 15*time.Second, "after 20 rapid previous-window changes", markers...)

	// The UI must still take input after the storm.
	leaveTerminalMode(t, term)
}

// TestWorkspaceSwitch moves between workspaces and asserts windows follow the
// workspace they belong to rather than leaking across.
func TestWorkspaceSwitch(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo WS1MARK-$((1+0))", "WS1MARK-1", 20*time.Second)
	leaveTerminalMode(t, term)

	// Workspace 2 must be empty and must not show workspace 1's pane.
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 0 && !strings.Contains(s.Text(), "WS1MARK-1")
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 2 is not empty, or workspace 1's pane leaked into it: %v\n%s",
			err, term.Snapshot())
	}

	// A window created here belongs to workspace 2.
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo WS2MARK-$((1+1))", "WS2MARK-2", 20*time.Second)
	leaveTerminalMode(t, term)

	// Back to workspace 1: its pane must return, and workspace 2's must not.
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "1"); err != nil {
		t.Fatalf("switch to workspace 1: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "WS1MARK-1") && !strings.Contains(s.Text(), "WS2MARK-2")
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 1 did not come back cleanly: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after workspace switching")
}

// TestMinimizeAndRestore minimizes a window and restores it, asserting the pane
// content survives the round trip. A minimized window stops being rendered, so
// this is a cache-invalidation path in the same family as the focus-switch bugs.
func TestMinimizeAndRestore(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo MINMARK-$((20+3))", "MINMARK-23", 20*time.Second)
	leaveTerminalMode(t, term)

	for i := range 3 {
		if err := term.SendKeys("m"); err != nil {
			t.Fatalf("minimize %d: %v", i, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return !strings.Contains(s.Text(), "MINMARK-23")
		}, uiTimeout); err != nil {
			t.Fatalf("window %d did not minimize: %v\n%s", i, err, term.Snapshot())
		}

		if err := term.SendKeys("M"); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
		if err := term.WaitForText("MINMARK-23", uiTimeout); err != nil {
			t.Fatalf("pane content did not survive minimize/restore round %d: %v\n%s",
				i, err, term.Snapshot())
		}
	}
	alive(t, term, "after minimize/restore")
}

// TestZoomToggle zooms a window to fill the screen and back, asserting content
// survives both transitions. Zoom resizes the emulator, which is the operation
// that tore the cell buffer when it ran without the window's I/O lock.
func TestZoomToggle(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ZOOMMARK-$((50+5))", "ZOOMMARK-55", 20*time.Second)
	leaveTerminalMode(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "before zooming")

	// Focus back to the marked window, then zoom it.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if err := term.WaitForText("ZOOMMARK-55", uiTimeout); err != nil {
		t.Fatalf("marked window never refocused: %v\n%s", err, term.Snapshot())
	}

	for i := range 3 {
		if err := term.SendKeys(tuitest.Ctrl('b'), "z"); err != nil {
			t.Fatalf("zoom %d: %v", i, err)
		}
		if err := term.WaitForText("ZOOMMARK-55", uiTimeout); err != nil {
			t.Fatalf("content lost entering zoom, round %d: %v\n%s", i, err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Ctrl('b'), "z"); err != nil {
			t.Fatalf("unzoom %d: %v", i, err)
		}
		if err := term.WaitForText("ZOOMMARK-55", uiTimeout); err != nil {
			t.Fatalf("content lost leaving zoom, round %d: %v\n%s", i, err, term.Snapshot())
		}
	}
	alive(t, term, "after zoom toggling")
}

// TestResizeKeepsPaneContent resizes the terminal underneath tuios, which
// delivers a real SIGWINCH and drives every emulator resize path, and asserts
// the pane's content survives each size.
func TestResizeKeepsPaneContent(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40, args: []string{"--shared-borders"}})
	waitBoot(t, term)

	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo RESIZEMARK-$((3*3))", "RESIZEMARK-9", 20*time.Second)
	leaveTerminalMode(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "before resizing")
	enableTiling(t, term, "RESIZEMARK-9")

	for _, size := range [][2]int{{100, 30}, {140, 50}, {90, 24}, {120, 40}} {
		if err := term.Resize(size[0], size[1]); err != nil {
			t.Fatalf("resize to %dx%d failed (tuios likely died): %v\n%s",
				size[0], size[1], err, term.Snapshot())
		}
		if err := term.WaitForText("RESIZEMARK-9", 15*time.Second); err != nil {
			t.Fatalf("pane content lost at %dx%d: %v\n%s",
				size[0], size[1], err, term.Snapshot())
		}
		alive(t, term, fmt.Sprintf("after resize to %dx%d", size[0], size[1]))
	}
}

// TestTwoClientsSeeConsistentState attaches two clients to one daemon session
// and asserts a change made through one is observed by the other.
//
// The daemon broadcasts state back to every client and each applies it through
// updateWindowFromState, which is the path that resized emulators without the
// window's I/O lock and blanked panes. Two clients means twice the syncs.
func TestTwoClientsSeeConsistentState(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-shared", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}

	c1 := startIn(t, base, startOpts{args: []string{"attach", "e2e-shared"}})
	if err := c1.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, c1.Snapshot())
	}

	c2 := startIn(t, base, startOpts{args: []string{"attach", "e2e-shared"}})
	// tuios reports the client count in a toast, which is the daemon's own
	// statement that both clients are attached.
	if err := c2.WaitForText("2 clients", bootTimeout); err != nil {
		t.Fatalf("second client never attached: %v\n%s", err, c2.Snapshot())
	}

	// An attached client boots straight into terminal mode when the session
	// already has a window, so the pane is reachable immediately. Exercise the
	// content path first, while that is still true: driving output through one
	// client must reach the other.
	time.Sleep(insertGuard + 150*time.Millisecond)
	if err := c1.SendKeys("echo SHAREDMARK-$((7*8))", tuitest.Enter); err != nil {
		t.Fatalf("c1 echo: %v", err)
	}
	for name, c := range map[string]*tuitest.Terminal{"client1": c1, "client2": c2} {
		if err := c.WaitForText("SHAREDMARK-56", 20*time.Second); err != nil {
			t.Fatalf("%s never saw pane output produced through client1: %v\n%s",
				name, err, c.Snapshot())
		}
	}

	// Now the window-list path. Leaving terminal mode is required first, or "n"
	// would go to the shell rather than the window manager.
	if err := c1.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("c1 to window mode: %v", err)
	}
	if err := c1.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("c1 never entered window management mode: %v\n%s", err, c1.Snapshot())
	}
	time.Sleep(insertGuard)

	// A window created on client one must show up on client two.
	if err := c1.SendKeys("n"); err != nil {
		t.Fatalf("c1 new window: %v", err)
	}
	for name, c := range map[string]*tuitest.Terminal{"client1": c1, "client2": c2} {
		if err := c.WaitFor(func(s tuitest.Screen) bool {
			return countWindows(s) == 2
		}, 20*time.Second); err != nil {
			t.Fatalf("%s never saw the window created on client1 (count %d): %v\n%s",
				name, countWindows(c.Screen()), err, c.Snapshot())
		}
	}

	alive(t, c1, "at end of multi-client test")
	alive(t, c2, "at end of multi-client test")
}
