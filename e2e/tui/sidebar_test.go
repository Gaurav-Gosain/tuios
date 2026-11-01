package tuie2e

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// sidebarHeader is the label the sidebar renders above its session list.
// Asserting on it proves the sidebar panel is actually on screen rather than
// that a config flag was flipped. Lowercase: the rail's headers are furniture
// and are drawn as such.
const sidebarHeader = "sessions"

// toggleSidebarViaPalette opens the command palette, runs the "Toggle sidebar"
// entry, and waits for the palette to close.
func toggleSidebarViaPalette(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("Toggle sidebar"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	if err := term.WaitForText("Toggle sidebar", uiTimeout); err != nil {
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
	for r := range rows {
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
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
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

// screenRowOf returns the topmost row containing needle, or -1. The sidebar
// sits at the top of the screen, so the topmost occurrence is the sidebar row
// even when the dock repeats the name further down.
func screenRowOf(s tuitest.Screen, needle string) int {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), needle) {
			return r
		}
	}
	return -1
}

// TestSidebarDragReordersSessions drags a session row above another and
// asserts the sidebar's session list follows: bravo, created second and listed
// second, ends up above alpha after the drag.
func TestSidebarDragReordersSessions(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	repo := testutil.GitRepo(t)
	away := t.TempDir()

	if out, err := tuiosCLIIn(t, base, away, "new", "alpha", "--detach"); err != nil {
		t.Fatalf("create alpha: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, away, "new", "bravo", "--detach"); err != nil {
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
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	// Creation order puts alpha above bravo.
	s := term.Screen()
	alphaRow, bravoRow := screenRowOf(s, "alpha"), screenRowOf(s, "bravo")
	if alphaRow < 0 || bravoRow < 0 || alphaRow >= bravoRow {
		t.Fatalf("expected alpha above bravo before the drag (alpha=%d bravo=%d)\n%s",
			alphaRow, bravoRow, term.Snapshot())
	}

	// Drag bravo up onto alpha's row; the rows swap and the release commits.
	col, _ := findOnScreen(t, term, "bravo")
	mouseDrag(t, term, col, bravoRow, col, alphaRow, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, b := screenRowOf(s, "alpha"), screenRowOf(s, "bravo")
		return a >= 0 && b >= 0 && b < a
	}, uiTimeout); err != nil {
		t.Fatalf("drag did not reorder bravo above alpha: %v\n%s", err, term.Snapshot())
	}

	// The drag must not have switched sessions: releasing a drag is not a click.
	if strings.Contains(term.Screen().Text(), "Session: bravo") {
		t.Fatalf("drag release switched sessions\n%s", term.Snapshot())
	}

	alive(t, term, "after dragging a session row")
	sidebarDragMovesGroups(t, base, repo, away, term)
}

// sidebarDragMovesGroups is #1418: a group's row moves the whole group, and a
// worktree row moves only inside its group. It runs in the default layout, then
// with group_main on, where the repository has a row of its own. The controls are
// in NEGATIVE_CONTROLS.md.
func sidebarDragMovesGroups(t *testing.T, base, repo, away string, term *tuitest.Terminal) {
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{repo, []string{"new", "mainsess", "--detach"}},
		{away, []string{"worktree", "new", "f/one", "--repo", repo, "--detach"}},
		{away, []string{"worktree", "new", "f/two", "--repo", repo, "--detach"}},
		{away, []string{"new", "zulu", "--detach"}},
	} {
		if out, err := tuiosCLIIn(t, base, step.dir, step.args...); err != nil {
			t.Fatalf("tuios %v: %v: %s", step.args, err, out)
		}
	}
	waitForAll(t, term, uiTimeout, "the group and the session under it", "mainsess", "f/one", "f/two", "zulu")

	// rail is the rows of the names in the order the rail draws them. The order
	// is the assertion: a block moved whole leaves its rows next to each other.
	rail := func(s tuitest.Screen, names ...string) []string {
		rows := map[int]string{}
		for _, name := range names {
			rows[screenRowOf(s, name)] = name
		}
		keys := slices.Sorted(maps.Keys(rows))
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			out = append(out, rows[k])
		}
		return out
	}
	drag := func(from, to string) {
		t.Helper()
		s := term.Screen()
		col, _ := findOnScreen(t, term, from)
		mouseDrag(t, term, col, screenRowOf(s, from), col, screenRowOf(s, to), tuitest.MouseLeft, 0)
	}
	wantOrder := func(what string, names ...string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return slices.Equal(rail(s, names...), names)
		}, uiTimeout); err != nil {
			t.Fatalf("%s: want the rail in the order %v, got %v: %v\n%s", what, names, rail(term.Screen(), names...), err, term.Snapshot())
		}
	}
	names := []string{"bravo", "alpha", "mainsess", "f/one", "f/two", "zulu"}
	wantOrder("before the drags", names...)

	// A worktree row moves inside its group and stops at the group's edge.
	drag("f/two", "f/one")
	wantOrder("a worktree row dragged over its sibling", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	drag("f/one", "zulu")
	wantOrder("a worktree row dragged past its group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	drag("f/two", "alpha")
	wantOrder("a worktree row dragged above its group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")

	// A main checkout's row moves the group, past a block taller and shorter
	// than itself, and stays put between the two events of a slow drag.
	drag("mainsess", "zulu")
	wantOrder("the group's row dragged down", "bravo", "alpha", "zulu", "mainsess", "f/two", "f/one")
	railShot(t, term, "rail-drag-group-down")
	drag("zulu", "f/one")
	wantOrder("a session dragged past a group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	// A session that stops over the middle of a taller block stays where it is,
	// however the pointer shakes there. The block it would pass is under the
	// pointer on both sides of a swap, so a swap here would flip on every event.
	{
		s := term.Screen()
		col, _ := findOnScreen(t, term, "zulu")
		from, to := screenRowOf(s, "zulu"), screenRowOf(s, "f/two")
		mousePress(t, term, col, from, tuitest.MouseLeft, 0)
		for row := from - 1; row >= to; row-- {
			mouseMotion(t, term, col, row, tuitest.MouseLeft, 0)
		}
		mouseMotion(t, term, col+1, to, tuitest.MouseLeft, 0)
		mouseRelease(t, term, col+1, to, tuitest.MouseLeft, 0)
	}
	time.Sleep(insertGuard)
	wantOrder("a session stopped over the middle of a group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	drag("mainsess", "bravo")
	wantOrder("the group's row dragged up", "mainsess", "f/two", "f/one", "bravo", "alpha", "zulu")
	if strings.Contains(term.Screen().Text(), "Session: mainsess") {
		t.Errorf("dragging the group's row opened its session\n%s", term.Snapshot())
	}

	// The reorder keys follow the same rule, on the main checkout's row and on a
	// worktree row. The attached session is alpha, so k walks up to the row wanted.
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter the rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}
	// alpha, bravo, f/one, f/two, mainsess.
	if err := term.SendKeys("k", "k", "k", "k", "J"); err != nil {
		t.Fatalf("reorder the main checkout's row: %v", err)
	}
	wantOrder("J on the group's row", "bravo", "mainsess", "f/two", "f/one", "alpha", "zulu")
	if err := term.SendKeys("j", "J"); err != nil {
		t.Fatalf("reorder a worktree row: %v", err)
	}
	wantOrder("J on a worktree row", "bravo", "mainsess", "f/one", "f/two", "alpha", "zulu")
	if err := term.SendKeys("J"); err != nil {
		t.Fatalf("reorder a worktree row past its group: %v", err)
	}
	time.Sleep(insertGuard)
	wantOrder("J on the last worktree row", "bravo", "mainsess", "f/one", "f/two", "alpha", "zulu")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("leave the rail: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), railPill) }, uiTimeout); err != nil {
		t.Fatalf("esc did not leave the rail: %v\n%s", err, term.Snapshot())
	}

	// With group_main on, the repository's own row moves the group, and the main
	// checkout's row is one more row inside it.
	if err := term.Close(); err != nil {
		t.Logf("close: %v", err)
	}
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\ngroup_main = true\n")
	term = startIn(t, base, startOpts{args: []string{"attach", "alpha"}})
	const header = "· repo"
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railLineWith(s, header) >= 0 && railLineWith(s, "─ main") >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("the rail never drew the repository's own row: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	names = []string{"bravo", header, "─ main", "f/one", "f/two", "alpha", "zulu"}
	wantOrder("with group_main on", names...)
	drag("─ main", "f/two")
	wantOrder("the main checkout's row inside the group", "bravo", header, "f/one", "f/two", "─ main", "alpha", "zulu")
	drag(header, "alpha")
	wantOrder("the header dragged down", "bravo", "alpha", header, "f/one", "f/two", "─ main", "zulu")
	railShot(t, term, "rail-drag-group-main")
	drag("f/one", "zulu")
	wantOrder("a worktree row stops at its group's edge", "bravo", "alpha", header, "f/two", "─ main", "f/one", "zulu")
	alive(t, term, "after dragging groups")
}
