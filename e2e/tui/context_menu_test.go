package tuie2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func shiftRightClick(t *testing.T, term *tuitest.Terminal, x, y int) {
	t.Helper()
	mouseClick(t, term, x, y, tuitest.MouseRight, tuitest.ModShift)
}

func leftClick(t *testing.T, term *tuitest.Terminal, x, y int) {
	t.Helper()
	mouseClick(t, term, x, y, tuitest.MouseLeft, 0)
}

func waitMenu(t *testing.T, term *tuitest.Terminal, what string, markers ...string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, markers...)
	}, uiTimeout); err != nil {
		t.Fatalf("%s: context menu never showed %v: %v\n%s", what, markers, err, term.Snapshot())
	}
}

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

	shiftRightClick(t, term, 40, 15)
	waitMenu(t, term, "desktop", "Desktop", "New window", "Command palette")
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
	enableTiling(t, term)

	shiftRightClick(t, term, 20, 10)
	waitMenu(t, term, "pane", "Split right", "Copy selection", "Rename")
	if strings.Contains(term.Screen().Text(), "Command palette") {
		t.Fatalf("the pane menu is showing desktop rows\n%s", term.Snapshot())
	}
	leftClick(t, term, 70, 30)
	waitMenuGone(t, term, "Split right", "after click-away on the pane menu")

	shiftRightClick(t, term, 20, 0)
	waitMenu(t, term, "pane top row", "Split right", "Rename", "Minimize")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Minimize", "after esc on the pane menu")

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
// The lap below is the menu's live rows in order, so adding a row to the pane
// menu means adding it here. That is the point: a row nobody listed is a row
// nobody checked was reachable.
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
	waitMenu(t, term, "pane", "Copy selection", "Split right", "Zoom")

	lap := []string{
		"Split right", "Split down", "Rename", "Zoom", "Screenshot this window",
		"Minimize", "Close pane",
		"Split right",
	}
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

func markedRow(s tuitest.Screen) string {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if _, after, ok := strings.Cut(line, "› "); ok {
			return strings.TrimSpace(after)
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

			screen := term.Screen()
			if !rowHasHint(screen, "Command palette") {
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

func assertNoLineOverflow(t *testing.T, s tuitest.Screen, cols int, what string) {
	t.Helper()
	_, rows := s.Size()
	for r := range rows {
		if w := len([]rune(strings.TrimRight(s.Line(r), " "))); w > cols {
			t.Errorf("%s: row %d is %d cells wide, screen is %d", what, r, w, cols)
		}
	}
}

// TestPlainRightClickOpensMenuAndRightDragResizes pins the right button's
// click-vs-drag split over a pane running a plain shell. A right-CLICK opens the
// pane menu, decided on release; a right-DRAG resizes without ever showing one.
// A drag that goes past the threshold and comes back to where it started is a
// drag too: it opened the menu on release, because the release read only its
// own distance from the press. Ctrl+right-click opens the menu on the press,
// the path that also works over an app holding the mouse.
//
// Negative control, confirmed red: without the RightClickPending reset in
// handleMouseMotion, the drag that comes back opens the menu and the new pane
// never appears.
func TestPlainRightClickOpensMenuAndRightDragResizes(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")
	enableTiling(t, term)

	mousePress(t, term, 20, 10, tuitest.MouseRight, 0)
	mouseRelease(t, term, 20, 10, tuitest.MouseRight, 0)
	if err := term.WaitForText("Close pane", uiTimeout); err != nil {
		t.Fatalf("a plain right-click did not open the pane menu: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Close pane", "after esc on the plain right-click menu")

	mouseClick(t, term, 20, 10, tuitest.MouseRight, tuitest.ModCtrl)
	if err := term.WaitForText("Close pane", uiTimeout); err != nil {
		t.Fatalf("ctrl+right-click did not open the pane menu: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Close pane", "after esc on the ctrl+right-click menu")

	noMenuAfter := func(what string) {
		t.Helper()
		before := countWindows(term.Screen())
		if err := term.SendKeys("n"); err != nil {
			t.Fatalf("send n: %v", err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return countWindows(s) == before+1
		}, uiTimeout); err != nil || strings.Contains(term.Screen().Text(), "Close pane") {
			copiesFrame(t, term, "right-drag-menu-failed")
			t.Fatalf("ASSERTION: %s opened a context menu; a drag is a resize: %v\n%s", what, err, term.Snapshot())
		}
	}
	mousePress(t, term, 20, 10, tuitest.MouseRight, 0)
	mouseMotion(t, term, 30, 16, tuitest.MouseRight, 0)
	mouseRelease(t, term, 30, 16, tuitest.MouseRight, 0)
	noMenuAfter("a right-drag")

	mousePress(t, term, 20, 10, tuitest.MouseRight, 0)
	mouseMotion(t, term, 30, 16, tuitest.MouseRight, 0)
	mouseMotion(t, term, 20, 10, tuitest.MouseRight, 0)
	mouseRelease(t, term, 20, 10, tuitest.MouseRight, 0)
	noMenuAfter("a right-drag that came back to its start")
	copiesFrame(t, term, "right-drag-back-no-menu")
	alive(t, term, "after a right-drag resize")
}

var sgrRightPressReport = regexp.MustCompile(`(?:^|[^0-9;])2;\d+;\d+M`)

// TestPlainRightClickOpensMenuOverAMouseApp covers a pane whose program tracks
// the mouse, as agent harnesses and editors do. In terminal mode the right
// button is that program's: the click reaches it and opens no menu. In
// window-management mode no click reaches the program at all, so a plain
// right-click there opens the pane menu as it does over a shell.
//
// How this could pass wrongly, written down first:
//   - The terminal-mode click could open the menu and also reach the program.
//     The menu must be absent while the report arrives.
//   - The window-mode menu could come from the click reaching the program and
//     something else opening a menu. The program must see no second report.
func TestPlainRightClickOpensMenuOverAMouseApp(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "after first window")
	enableTiling(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033[?1000h\033[?1006h'; echo MOUSE""ON`, "MOUSEON", shellTimeout)

	mousePress(t, term, 20, 10, tuitest.MouseRight, 0)
	mouseRelease(t, term, 20, 10, tuitest.MouseRight, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return sgrRightPressReport.MatchString(s.Text())
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: in terminal mode the right-click did not reach the program tracking the mouse: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Close pane") {
		t.Fatalf("ASSERTION: in terminal mode a right-click over a program tracking the mouse opened the pane menu\n%s", term.Snapshot())
	}
	reports := len(sgrRightPressReport.FindAllString(term.Screen().Text(), -1))

	leaveTerminalMode(t, term)
	mousePress(t, term, 20, 10, tuitest.MouseRight, 0)
	mouseRelease(t, term, 20, 10, tuitest.MouseRight, 0)
	if err := term.WaitForText("Close pane", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: in window-management mode a plain right-click over a program tracking the mouse opened no pane menu: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "window-mode-menu")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	waitMenuGone(t, term, "Close pane", "after esc on the window-mode menu")
	if got := len(sgrRightPressReport.FindAllString(term.Screen().Text(), -1)); got != reports {
		t.Fatalf("ASSERTION: the window-mode right-click reached the program (%d reports, %d before)\n%s", got, reports, term.Snapshot())
	}
	alive(t, term, "after right-clicks over a program tracking the mouse")
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

	renameFocused(t, term, "logs")
	if err := term.SendKeys("m"); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	if err := term.WaitForText("1:logs", uiTimeout); err != nil {
		t.Fatalf("the minimized window never appeared in the dock: %v\n%s", err, term.Snapshot())
	}

	_, rows := term.Screen().Size()
	dockRow := term.Screen().Line(rows - 1)
	before, _, ok := strings.Cut(dockRow, "logs")
	if !ok {
		t.Fatalf("no dock entry on the dock row %q\n%s", dockRow, term.Snapshot())
	}
	x := len([]rune(before))

	shiftRightClick(t, term, x, rows-1)
	waitMenu(t, term, "dock entry", "Restore")

	screen := term.Screen().Text()
	if strings.Contains(screen, "New window") {
		t.Fatalf("shift+right-click on the dock entry %q opened the dock's general menu\n%s",
			"logs", term.Snapshot())
	}
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

func renameFocused(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	renameWindow(t, term, name)
}

func moveMouse(t *testing.T, term *tuitest.Terminal, x, y int) {
	t.Helper()
	if err := term.SendKeys(fmt.Sprintf("\x1b[<35;%d;%dM", x+1, y+1)); err != nil {
		t.Fatalf("mouse move to (%d,%d): %v", x, y, err)
	}
}

// TestContextMenuHoverFollowsPointer drives hover through the real binary.
//
// This has to be an end-to-end test. The program installs a mouse-motion filter
// (filterMouseMotion in cmd/tuios/run.go) as a bubbletea option, and that filter
// is a whitelist: it drops every motion event that does not match one of a
// handful of conditions. The filter exists only in the assembled program, so a
// unit test of the motion handler passes whether or not the event can ever reach
// it. The context menu's hover shipped broken for exactly that reason.
//
// The assertion is on the selection marker, which is the only thing on screen
// that says which row enter would run.
func TestContextMenuHoverFollowsPointer(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)

	shiftRightClick(t, term, 40, 15)
	waitMenu(t, term, "desktop", "New window", "Command palette")

	if got := markedRow(term.Screen()); !strings.Contains(got, "New window") {
		t.Fatalf("menu opened with the marker on %q, want New window\n%s", got, term.Snapshot())
	}

	target := "Command palette"
	row := rowContaining(term.Screen(), target)
	if row < 0 {
		t.Fatalf("%q is not on screen\n%s", target, term.Snapshot())
	}

	moveMouse(t, term, 42, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(markedRow(s), target)
	}, uiTimeout); err != nil {
		t.Fatalf("hovering row %d (%q) never moved the selection marker, which is still on %q. "+
			"The motion event is most likely being dropped by filterMouseMotion in "+
			"cmd/tuios/run.go before it reaches the handler: %v\n%s",
			row, target, markedRow(term.Screen()), err, term.Snapshot())
	}

	back := rowContaining(term.Screen(), "Toggle tiling")
	if back < 0 {
		t.Fatalf("Toggle tiling is not on screen\n%s", term.Snapshot())
	}
	moveMouse(t, term, 42, back)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(markedRow(s), "Toggle tiling")
	}, uiTimeout); err != nil {
		t.Fatalf("hovering back up the menu did not move the marker (still on %q): %v\n%s",
			markedRow(term.Screen()), err, term.Snapshot())
	}

	alive(t, term, "after hovering the context menu")
}

func rowContaining(s tuitest.Screen, text string) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), text) {
			return r
		}
	}
	return -1
}

// TestContextMenuReachesTopWindowRowWithTopDock is the regression test for the
// defect that made a pane unreachable at the top of the screen.
//
// With the dock at the top, the layout starts below it, so the topmost window's
// first row is the row immediately after the dock. The dock band test used to be
// inclusive of that row, so every shift+right-click on it opened the dock's menu
// and the window under the pointer was never consulted.
//
// This test does not use the shared window-count helpers: they read the dock's
// status field off the bottom rows, which is not where the dock is here.
func TestContextMenuReachesTopWindowRowWithTopDock(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--dockbar-position", "top"}})
	waitBoot(t, term)

	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("new window: %v", err)
	}
	enableTiling(t, term)
	time.Sleep(500 * time.Millisecond)

	top := -1
	_, rows := term.Screen().Size()
	for r := range rows {
		if strings.Contains(term.Screen().Line(r), "╭") {
			top = r
			break
		}
	}
	if top < 0 {
		t.Fatalf("could not find the tiled window's top border\n%s", term.Snapshot())
	}

	shiftRightClick(t, term, 30, top)
	waitMenu(t, term, "top window row under a top dock", "Split right", "Rename")

	if strings.Contains(term.Screen().Text(), "Toggle tiling") {
		t.Fatalf("row %d is the top window's first row but it opened the dock's menu; "+
			"the dock band is claiming a row the dock does not draw on\n%s",
			top, term.Snapshot())
	}
	alive(t, term, "after opening the pane menu on the top window row")
}

func rowHasHint(s tuitest.Screen, label string) bool {
	_, rows := s.Size()
	for r := range rows {
		_, after, ok := strings.Cut(s.Line(r), label)
		if !ok {
			continue
		}
		after = strings.TrimRight(after, " │")
		return strings.TrimSpace(after) != ""
	}
	return false
}
