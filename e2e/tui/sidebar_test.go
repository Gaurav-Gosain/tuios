package tuie2e

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

const sidebarHeader = "sessions"

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

	enableTiling(t, term, "ALPHA", "BRAVO")

	if strings.Contains(term.Screen().Text(), sidebarHeader) {
		t.Fatalf("sidebar header %q on screen before it was enabled\n%s", sidebarHeader, term.Snapshot())
	}

	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, sidebarHeader, "ALPHA")
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar did not render with its header and a window row: %v\n%s", err, term.Snapshot())
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "ALPHA", "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("both panes not visible beside the sidebar: %v\n%s", err, term.Snapshot())
	}

	waitWindowCount(t, term, 2, "after enabling the sidebar")

	toggleSidebarViaPalette(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), sidebarHeader) && screenHas(s, "ALPHA", "BRAVO")
	}, uiTimeout); err != nil {
		t.Fatalf("sidebar did not toggle back off: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after toggling the sidebar off")
}

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

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	enterTerminalMode(t, term)

	col, row := findOnScreen(t, term, "bravo")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitForText("Session: bravo", uiTimeout); err != nil {
		t.Fatalf("click on the bravo row did not switch: %v\n%s", err, term.Snapshot())
	}

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

	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "sidebar with both session rows", sidebarHeader, "bravo")

	s := term.Screen()
	alphaRow, bravoRow := screenRowOf(s, "alpha"), screenRowOf(s, "bravo")
	if alphaRow < 0 || bravoRow < 0 || alphaRow >= bravoRow {
		t.Fatalf("expected alpha above bravo before the drag (alpha=%d bravo=%d)\n%s",
			alphaRow, bravoRow, term.Snapshot())
	}

	col, _ := findOnScreen(t, term, "bravo")
	mouseDrag(t, term, col, bravoRow, col, alphaRow, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, b := screenRowOf(s, "alpha"), screenRowOf(s, "bravo")
		return a >= 0 && b >= 0 && b < a
	}, uiTimeout); err != nil {
		t.Fatalf("drag did not reorder bravo above alpha: %v\n%s", err, term.Snapshot())
	}

	if strings.Contains(term.Screen().Text(), "Session: bravo") {
		t.Fatalf("drag release switched sessions\n%s", term.Snapshot())
	}

	alive(t, term, "after dragging a session row")
	sidebarDragMovesGroups(t, base, repo, away, term)
}

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

	drag("f/two", "f/one")
	wantOrder("a worktree row dragged over its sibling", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	drag("f/one", "zulu")
	wantOrder("a worktree row dragged past its group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	drag("f/two", "alpha")
	wantOrder("a worktree row dragged above its group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")

	drag("mainsess", "zulu")
	wantOrder("the group's row dragged down", "bravo", "alpha", "zulu", "mainsess", "f/two", "f/one")
	railShot(t, term, "rail-drag-group-down")
	drag("zulu", "f/one")
	wantOrder("a session dragged past a group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
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
	wantOrder("a session stopped over the middle of a group", "bravo", "alpha", "mainsess", "f/two", "f/one", "zulu")
	drag("mainsess", "bravo")
	wantOrder("the group's row dragged up", "mainsess", "f/two", "f/one", "bravo", "alpha", "zulu")
	if strings.Contains(term.Screen().Text(), "Session: mainsess") {
		t.Errorf("dragging the group's row opened its session\n%s", term.Snapshot())
	}

	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("enter the rail: %v", err)
	}
	if err := term.WaitForText(railPill, uiTimeout); err != nil {
		t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
	}
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
	wantOrder("J on the last worktree row", "bravo", "mainsess", "f/one", "f/two", "alpha", "zulu")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("leave the rail: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), railPill) }, uiTimeout); err != nil {
		t.Fatalf("esc did not leave the rail: %v\n%s", err, term.Snapshot())
	}

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
