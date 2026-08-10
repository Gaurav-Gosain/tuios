package tuie2e

import (
	"strings"
	"testing"

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
