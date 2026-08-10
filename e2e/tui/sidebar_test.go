package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// sidebarHeader is the chip the sidebar renders at the top of its column.
// Asserting on it proves the sidebar panel is actually on screen rather than
// that a config flag was flipped.
const sidebarHeader = "Sessions"

// toggleSidebarViaPalette opens the command palette, runs the "Toggle Sidebar"
// entry, and waits for the palette to close.
func toggleSidebarViaPalette(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("Toggle Sidebar"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	if err := term.WaitForText("Toggle Sidebar", uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the sidebar entry: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate palette entry: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("palette did not close after toggling the sidebar: %v\n%s", err, term.Snapshot())
	}
}

// TestSidebarRendersAndPanesTileBesideIt drives the flagship path: two named,
// tiled panes, then the sidebar toggled on. It asserts the sidebar's header is
// on screen with the session and window names, and that both panes are still
// visible beside it (they re-tiled into the reduced content box rather than
// being covered or squeezed off screen).
func TestSidebarRendersAndPanesTileBesideIt(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	newWindow(t, term)
	renameWindow(t, term, "ALPHA")
	newWindow(t, term)
	renameWindow(t, term, "BRAVO")

	// Tiled so both panes show at once; a floating layout would overlap them.
	enableTiling(t, term, "ALPHA", "BRAVO")

	// The sidebar is off by default, so its header must not be on screen yet.
	if strings.Contains(term.Screen().Text(), sidebarHeader) {
		t.Fatalf("sidebar header %q on screen before it was enabled\n%s", sidebarHeader, term.Snapshot())
	}

	toggleSidebarViaPalette(t, term)

	// The sidebar panel is up: its header, plus at least one of the window rows
	// it lists, must be on screen.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, sidebarHeader, "ALPHA")
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar did not render with its header and a window row: %v\n%s", err, term.Snapshot())
	}

	// Panes still tile beside it: both markers remain visible at the same time,
	// so the panes were re-tiled into the reduced content box, not covered by the
	// sidebar or squeezed off screen.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "ALPHA", "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("both panes not visible beside the sidebar: %v\n%s", err, term.Snapshot())
	}

	// The dock still reports both windows, so nothing was lost to the relayout.
	waitWindowCount(t, term, 2, "after enabling the sidebar")

	// Toggling again removes the sidebar; the header leaves the screen and the
	// panes reclaim the space.
	toggleSidebarViaPalette(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), sidebarHeader) && screenHas(s, "ALPHA", "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar did not toggle back off: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after toggling the sidebar off")
}

// findOnScreen returns the leftmost cell of the first row containing needle.
func findOnScreen(t *testing.T, term *tuitest.Terminal, needle string) (col, row int) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := 0; r < rows; r++ {
		if c := strings.Index(s.Line(r), needle); c >= 0 {
			return c, r
		}
	}
	t.Fatalf("%q not found on screen\n%s", needle, term.Snapshot())
	return 0, 0
}

// TestSidebarClickSwitchesSession covers the sidebar's core mouse promise
// against a real daemon: a single left click on a non-current session row
// switches the client to that session, in window-management mode and again in
// terminal mode (the mode the owner lives in).
func TestSidebarClickSwitchesSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "alpha", "--detach"); err != nil {
		t.Fatalf("create alpha: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "bravo", "--detach"); err != nil {
		t.Fatalf("create bravo: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	// Terminal mode is where the owner lives, and the sidebar band must own
	// clicks there too, ahead of any forwarding to the pane underneath.
	enterTerminalMode(t, term)

	// Click the bravo row and land on bravo.
	col, row := findOnScreen(t, term, "bravo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: bravo", uiTimeout); err != nil {
		t.Fatalf("click on the bravo row did not switch: %v\n%s", err, term.Snapshot())
	}

	// And back: from bravo, a click on the alpha row returns to alpha.
	if err := term.WaitForText("alpha", uiTimeout); err != nil {
		t.Fatalf("alpha row not visible after switching: %v\n%s", err, term.Snapshot())
	}
	col, row = findOnScreen(t, term, "alpha")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: alpha", uiTimeout); err != nil {
		t.Fatalf("click on the alpha row did not switch back: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after switching sessions from the sidebar")
}
