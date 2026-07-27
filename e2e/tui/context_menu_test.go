package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// shiftRightClick sends the SGR mouse report for a shift+right-click at a cell.
// That chord is what opens a context menu; plain right-click is a window resize
// and must keep being one.
func shiftRightClick(t *testing.T, term *tuitest.Terminal, x, y int) {
	t.Helper()
	if err := term.SendMouse(tuitest.MouseEvent{
		Col: x, Row: y,
		Button: tuitest.MouseRight,
		Action: tuitest.MousePress,
		Mods:   tuitest.ModShift,
	}); err != nil {
		t.Fatalf("shift+right-click at (%d,%d): %v", x, y, err)
	}
}

// leftClick sends a plain left button press at a cell.
func leftClick(t *testing.T, term *tuitest.Terminal, x, y int) {
	t.Helper()
	if err := term.SendMouse(tuitest.MouseEvent{
		Col: x, Row: y,
		Button: tuitest.MouseLeft,
		Action: tuitest.MousePress,
	}); err != nil {
		t.Fatalf("left click at (%d,%d): %v", x, y, err)
	}
}

// waitMenu fails unless every marker is on screen together within uiTimeout.
func waitMenu(t *testing.T, term *tuitest.Terminal, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, markers...)
	}, uiTimeout); err != nil {
		t.Fatalf("%s: context menu never showed %v: %v\n%s", what, markers, err, term.Snapshot())
	}
}

// waitMenuGone fails unless the marker leaves the screen.
func waitMenuGone(t *testing.T, term *tuitest.Terminal, marker, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), marker)
	}, uiTimeout); err != nil {
		t.Fatalf("%s: context menu never closed (%q still on screen): %v\n%s",
			what, marker, err, term.Snapshot())
	}
}

// TestContextMenuTargets drives a real tuios and asserts that shift+right-click
// on each target puts up the menu that belongs to that target, and only that
// menu.
//
// The assertions are on rows that are unique to one target, so a menu that
// opened on the wrong thing fails rather than passing on a shared row like
// "Close pane".
func TestContextMenuTargets(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	// Desktop: no window anywhere yet, so the middle of the screen is empty.
	shiftRightClick(t, term, 40, 15)
	waitMenu(t, term, "desktop", "Desktop", "New window", "Command palette")
	// Escape closes without running anything: no window may appear.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Command palette", "after esc on desktop menu")
	if n := countWindows(term.Screen()); n > 0 {
		t.Fatalf("esc on the desktop menu created %d window(s); it must fire nothing\n%s",
			n, term.Snapshot())
	}

	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")
	// A new window floats at a size the layout picks, so tile it: a single tiled
	// window fills the usable area, which makes "inside the pane" and "on its
	// title bar" fixed coordinates rather than a guess.
	enableTiling(t, term)

	// The pane's first row is its title bar; a row well inside it is content.
	shiftRightClick(t, term, 20, 10)
	waitMenu(t, term, "pane content", "Split right", "Copy selection")
	if strings.Contains(term.Screen().Text(), "Command palette") {
		t.Fatalf("the pane content menu is showing desktop rows\n%s", term.Snapshot())
	}
	leftClick(t, term, 70, 30) // click away
	waitMenuGone(t, term, "Split right", "after click-away on content menu")

	shiftRightClick(t, term, 20, 0)
	waitMenu(t, term, "pane title", "Rename", "Minimize")
	if strings.Contains(term.Screen().Text(), "Split right") {
		t.Fatalf("the title bar menu is showing content rows\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Minimize", "after esc on title menu")

	// The dock band is the last row.
	_, rows := term.Screen().Size()
	shiftRightClick(t, term, 60, rows-1)
	waitMenu(t, term, "dock", "Dock", "Restore all")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Restore all", "after esc on dock menu")

	alive(t, term, "after opening every context menu target")
}

// TestContextMenuRunsRegistryAction proves a menu row actually runs the action
// it names, through the same dispatcher a keybinding uses.
//
// "New window" is chosen because its effect is visible in the dock's window
// count, which is real evidence rather than a repainted frame.
func TestContextMenuRunsRegistryAction(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	shiftRightClick(t, term, 40, 15)
	waitMenu(t, term, "desktop", "New window")

	// "New window" is the first row and the menu opens with it selected, so
	// enter runs it.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter: %v", err)
	}
	waitWindowCount(t, term, 1, "after running New window from the context menu")
	waitMenuGone(t, term, "New window", "after activating a row")
	alive(t, term, "after running a context menu action")
}

// TestContextMenuArrowsSkipDimmedRows checks on screen that arrow navigation
// steps over an unavailable action instead of landing on it.
//
// The pane menu's first two rows are Copy selection and Paste. With no
// selection made and the pane not in terminal mode, both are dimmed, and the
// four rows below them are live. Two things then have to be true, and both are
// read off the selection marker rather than off internal state:
//
//   - the menu opens with the marker on Split right, not on the dimmed Copy
//     selection that is physically first;
//   - arrowing down off the last row wraps past both dimmed rows straight back
//     to Split right.
//
// The wrap is what makes this evidence. A menu that merely started lower down
// would pass the first check while still letting an arrow land on a dimmed row.
func TestContextMenuArrowsSkipDimmedRows(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")
	enableTiling(t, term)

	shiftRightClick(t, term, 20, 10)
	waitMenu(t, term, "pane content", "Copy selection", "Split right", "Zoom")

	// One full lap of the runnable rows, ending back where it started.
	lap := []string{"Split right", "Split down", "Zoom", "Close pane", "Split right"}
	for i, want := range lap {
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(markedRow(s), want)
		}, uiTimeout); err != nil {
			t.Fatalf("step %d: selection never reached %q (marker is on %q); "+
				"a dimmed row is reachable by arrow navigation: %v\n%s",
				i, want, markedRow(term.Screen()), err, term.Snapshot())
		}
		if i < len(lap)-1 {
			if err := term.SendKeys(tuitest.Down); err != nil {
				t.Fatalf("down: %v", err)
			}
		}
	}
	alive(t, term, "after arrowing through the context menu")
}

// markedRow returns the text of the row carrying the selection marker, or "".
// The marker is the only thing on screen that says which row enter would run,
// so navigation assertions read it rather than trusting internal state.
func markedRow(s tuitest.Screen) string {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if idx := strings.Index(line, "› "); idx >= 0 {
			return strings.TrimSpace(line[idx+len("› "):])
		}
	}
	return ""
}

// TestContextMenuFitsAtScreenEdges opens the menu hard against the right and
// bottom edges of a small screen and checks that every row of it is on screen.
//
// A menu anchored near an edge has to flip to the other side of the pointer. If
// it did not, the rows would be drawn past the edge, where the terminal simply
// discards them, and the user would see a menu with its right-hand side missing.
func TestContextMenuFitsAtScreenEdges(t *testing.T) {
	const cols, rows = 60, 20
	term, _ := start(t, startOpts{cols: cols, rows: rows})
	waitBoot(t, term)

	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"bottom-right", cols - 1, rows - 3},
		{"top-right", cols - 1, 1},
		{"bottom-left", 0, rows - 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shiftRightClick(t, term, tc.x, tc.y)
			waitMenu(t, term, tc.name, "New window", "Command palette")

			// Every row of the menu has to be complete. "Command palette" is the
			// longest label plus the longest hint, so it is the row that goes
			// missing first if the menu is hanging off an edge.
			screen := term.Screen()
			if !strings.Contains(screen.Text(), "ctrl+b") {
				t.Fatalf("%s: the menu's key hints are drawn off the screen edge\n%s",
					tc.name, term.Snapshot())
			}
			assertNoLineOverflow(t, screen, cols, tc.name)

			if err := term.SendKeys(tuitest.Esc); err != nil {
				t.Fatalf("esc: %v", err)
			}
			waitMenuGone(t, term, "Command palette", tc.name)
		})
	}
	alive(t, term, "after opening the menu at every screen edge")
}

// assertNoLineOverflow checks no rendered line is wider than the terminal.
func assertNoLineOverflow(t *testing.T, s tuitest.Screen, cols int, what string) {
	t.Helper()
	_, rows := s.Size()
	for r := range rows {
		if w := len([]rune(strings.TrimRight(s.Line(r), " "))); w > cols {
			t.Errorf("%s: row %d is %d cells wide, screen is %d", what, r, w, cols)
		}
	}
}

// TestPlainRightClickStillResizes is the regression guard for the behaviour the
// context menu was added alongside.
//
// Right-click on a pane has always started a corner resize. Adding a menu on
// shift+right-click must not take that away, so this checks that a plain
// right-click puts up no menu at all.
func TestPlainRightClickStillResizes(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")

	if err := term.SendMouse(tuitest.MouseEvent{
		Col: 20, Row: 10,
		Button: tuitest.MouseRight,
		Action: tuitest.MousePress,
	}); err != nil {
		t.Fatalf("plain right-click: %v", err)
	}
	// Give the frame a beat to show a menu if it were going to.
	time.Sleep(500 * time.Millisecond)

	if text := term.Screen().Text(); strings.Contains(text, "Split right") ||
		strings.Contains(text, "Close pane") {
		t.Fatalf("a plain right-click opened a context menu; it must still start a resize\n%s",
			term.Snapshot())
	}
	if err := term.SendMouse(tuitest.MouseEvent{
		Col: 20, Row: 10,
		Button: tuitest.MouseRight,
		Action: tuitest.MouseRelease,
	}); err != nil {
		t.Fatalf("right-click release: %v", err)
	}
	alive(t, term, "after a plain right-click")
}

// TestContextMenuOnDockEntry checks the dock's own entries get a menu about
// that window, not the dock's general one.
//
// The entry is found by its label on the dock row and clicked at that column.
// The dock row is full of multi-byte powerline glyphs, so the byte offset of
// the label is not its column and the runes before it have to be counted.
func TestContextMenuOnDockEntry(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")

	// Name the window so the dock entry is identifiable, then minimize it: only
	// minimized windows appear in the dock.
	renameFocused(t, term, "logs")
	if err := term.SendKeys("m"); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	if err := term.WaitForText("1:logs", uiTimeout); err != nil {
		t.Fatalf("the minimized window never appeared in the dock: %v\n%s", err, term.Snapshot())
	}

	_, rows := term.Screen().Size()
	dockRow := term.Screen().Line(rows - 1)
	b := strings.Index(dockRow, "logs")
	if b < 0 {
		t.Fatalf("no dock entry on the dock row %q\n%s", dockRow, term.Snapshot())
	}
	x := len([]rune(dockRow[:b]))

	shiftRightClick(t, term, x, rows-1)
	waitMenu(t, term, "dock entry", "Restore")

	screen := term.Screen().Text()
	// The dock entry's menu is titled after the window and offers Restore. The
	// dock's own menu offers New window, so seeing that row means the click
	// resolved to the dock background instead of to the entry on it.
	if strings.Contains(screen, "New window") {
		t.Fatalf("shift+right-click on the dock entry %q opened the dock's general menu\n%s",
			"logs", term.Snapshot())
	}
	// The hint has to be the key the registry actually binds for restoring the
	// first minimized window.
	if !strings.Contains(screen, "shift+1") {
		t.Fatalf("the Restore row does not show its registry keybinding\n%s", term.Snapshot())
	}
	alive(t, term, "after opening a dock entry's context menu")
}

// TestContextMenuDrawsOverZoomedPane guards the render fast path.
//
// A lone fullscreen pane is drawn by a path that skips the compositor entirely,
// and every overlay has to disqualify it or it is never drawn. A context menu
// that only appeared over tiled panes would be a subtle and very confusing bug.
func TestContextMenuDrawsOverZoomedPane(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")

	if err := term.SendKeys("z"); err != nil {
		t.Fatalf("zoom: %v", err)
	}
	if err := term.WaitForText("ZOOM", uiTimeout); err != nil {
		t.Fatalf("the pane never zoomed: %v\n%s", err, term.Snapshot())
	}

	shiftRightClick(t, term, 20, 10)
	waitMenu(t, term, "over a zoomed pane", "Close pane", "Zoom")
	alive(t, term, "after opening a menu over a zoomed pane")
}

// renameFocused gives the focused window a name through the rename keybinding.
func renameFocused(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.SendKeys("r"); err != nil {
		t.Fatalf("start rename: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(name, tuitest.Enter); err != nil {
		t.Fatalf("type name: %v", err)
	}
	if err := term.WaitForText(name, uiTimeout); err != nil {
		t.Fatalf("window never took the name %q: %v\n%s", name, err, term.Snapshot())
	}
}
