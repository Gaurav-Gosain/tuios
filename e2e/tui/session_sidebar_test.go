package tuie2e

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Whether the sidebar rail is shown is session state (see
// internal/session/sidebar_visibility.go). Hiding it on one client hides it on
// every client of the session, the panes grow into the columns it gave back on
// every screen, and the daemon keeps the grown rectangles. Each test here
// reads the rectangles back from the daemon with list-windows and reads the
// frame each client draws, and saves the frames it checks under artifactDir.

// sessionRailConfig is a config with the rail on the left and the dock at the top,
// tiled in layout, so the rail is the only thing to the left of the panes.
func sessionRailConfig(layout string, shown bool) string {
	return fmt.Sprintf("[startup]\nopen_default_window = true\ntiled = true\nlayout = %q\n"+
		"[appearance]\ndockbar_position = \"top\"\n"+
		"[appearance.sidebar]\nenabled = %v\nposition = \"left\"\n", layout, shown)
}

// railSession makes a detached session with config written under base, attaches
// one client with o and opens a second window. It returns the client.
func railSession(t *testing.T, base, name, layout string, o startOpts) *tuitest.Terminal {
	t.Helper()
	writeConfig(t, base, sessionRailConfig(layout, true))
	if out, err := tuiosCLI(t, base, "new", "-d", name); err != nil {
		t.Fatalf("create the detached session: %v\n%s", err, out)
	}
	if o.cols == 0 {
		o.cols, o.rows = bigCols, bigRows
	}
	term := attachIn(t, base, name, o)
	waitForSettledGeometryIn(t, base, name, 1)
	if out, err := tuiosCLI(t, base, "run-command", "-s", name, "NewWindow"); err != nil {
		t.Fatalf("open the second window: %v\n%s", err, out)
	}
	waitForSettledGeometryIn(t, base, name, 2)
	return term
}

// paneSpan is the box the daemon's rectangles cover: the leftmost and the
// rightmost column, and the summed width of the panes.
type paneSpan struct{ left, right, width int }

func spanOf(rects []winRect) paneSpan {
	s := paneSpan{left: 1 << 30}
	for _, r := range rects {
		s.left = min(s.left, r.X)
		s.right = max(s.right, r.X+r.Width)
		s.width += r.Width
	}
	return s
}

// waitSpan polls the daemon's rectangles until want accepts their span. The
// daemon is read without any input in between, so a client that laid its
// panes out and never told the daemon fails here.
func waitSpan(t *testing.T, base, name, what string, want func(paneSpan) bool) []winRect {
	t.Helper()
	return waitForShape(t, base, name, 2, what, func(rects []winRect) error {
		if s := spanOf(rects); !want(s) {
			return fmt.Errorf("the panes span %+v", s)
		}
		return nil
	})
}

// railShown reports whether a client draws the rail: its header and the
// first pane starting to the right of it.
func railShown(s tuitest.Screen) bool {
	return strings.Contains(s.Text(), sidebarHeader)
}

// waitRail waits for a client to show or hide the rail, and for its first pane
// to start at col.
func waitRail(t *testing.T, term *tuitest.Terminal, shown bool, col int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		starts := paneStarts(s)
		return railShown(s) == shown && len(starts) > 0 && strings.HasSuffix(starts[0], fmt.Sprintf(",%d", col))
	}, uiTimeout); err != nil {
		t.Fatalf("%s: want the rail shown=%v and the first pane at column %d, got shown=%v panes %v\n%s",
			what, shown, col, railShown(term.Screen()), paneStarts(term.Screen()), term.Snapshot())
	}
}

// toggleRail presses the sidebar key: the leader, then b.
func toggleRail(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, tuitest.Ctrl('b'), "b")
}

// TestHidingTheRailGrowsThePanes is the single-client report: in tiling mode,
// hiding the rail must give its columns back to the panes, on the screen and
// in the daemon, in each tiled layout. Showing it again takes them back.
//
// NEGATIVE CONTROL: fails on main at "the daemon after the rail is hidden".
// The client retiled on the daemon's resize answer and never pushed that
// layout, so the daemon kept the rectangles from before the rail moved until
// the next key. With the push after the resize answer removed (reserveOwed in
// internal/app) it fails at the same step.
func TestHidingTheRailGrowsThePanes(t *testing.T) {
	for _, layout := range []string{"bsp", "master-stack", "scrolling"} {
		t.Run(layout, func(t *testing.T) {
			base := t.TempDir()
			const name = "rail"
			term := railSession(t, base, name, layout, startOpts{})
			// The scrolling strip is longer than the screen by design, so its
			// far edge says nothing. Its near edge is where the box starts.
			reaches := func(s paneSpan) bool { return layout == "scrolling" || s.right == bigCols }
			before := spanOf(waitSpan(t, base, name, "the daemon with the rail shown", func(s paneSpan) bool {
				return s.left > 0 && reaches(s)
			}))
			rail := before.left
			waitRail(t, term, true, rail, "the client with the rail shown")
			dir := artifactDir(t)
			saveArtifact(t, term, dir, "rail-shown")

			toggleRail(t, term)
			waitRail(t, term, false, 0, "the client after the rail is hidden")
			after := spanOf(waitSpan(t, base, name, "the daemon after the rail is hidden", func(s paneSpan) bool {
				return s.left == 0 && reaches(s)
			}))
			saveArtifact(t, term, dir, "rail-hidden")
			// The scrolling strip keeps its columns' width and moves the strip,
			// so only the tiled layouts that partition the box grow each pane.
			if layout != "scrolling" && after.width != before.width+rail {
				t.Errorf("the panes are %d columns wide in all after the rail is hidden, want %d (%d plus the rail's %d)",
					after.width, before.width+rail, before.width, rail)
			}

			toggleRail(t, term)
			waitRail(t, term, true, rail, "the client after the rail is shown again")
			back := spanOf(waitSpan(t, base, name, "the daemon after the rail is shown again", func(s paneSpan) bool {
				return s.left == rail && reaches(s)
			}))
			saveArtifact(t, term, dir, "rail-shown-again")
			if layout != "scrolling" && back.width != before.width {
				t.Errorf("the panes are %d columns wide after the rail is back, want %d as before", back.width, before.width)
			}
		})
	}
}

// TestHidingTheRailHidesItOnEveryClient is two clients on one session, each
// with its own config. Hiding the rail on one hides it on the other, and both
// draw the same, wider panes. Showing it on the other brings it back on both.
//
// The second client's config has the rail off, and it attaches after the
// first client settled the session's rail as shown, so it shows the rail too:
// the first client to attach settles it.
//
// NEGATIVE CONTROL: fails on main at "the second client attaching", which
// reads its own config and draws no rail. With the adoption cut from the state
// sync it fails at "the first client after it hid the rail": the second client
// keeps its rail, so the session keeps the rail's columns and the first
// client's panes do not grow.
func TestHidingTheRailHidesItOnEveryClient(t *testing.T) {
	base := t.TempDir()
	const name = "rails"
	a := railSession(t, base, name, "bsp", startOpts{})
	before := spanOf(waitSpan(t, base, name, "the daemon with the rail shown", func(s paneSpan) bool {
		return s.left > 0 && s.right == bigCols
	}))
	rail := before.left

	home := t.TempDir()
	writeConfigIn(t, home, sessionRailConfig("bsp", false))
	b := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows, env: []string{"XDG_CONFIG_HOME=" + home}})
	waitRail(t, b, true, rail, "the second client attaching")
	dir := artifactDir(t)
	saveArtifact(t, a, dir, "a-shown")
	saveArtifact(t, b, dir, "b-shown")

	toggleRail(t, a)
	waitRail(t, a, false, 0, "the first client after it hid the rail")
	waitRail(t, b, false, 0, "the second client after the first hid the rail")
	waitSpan(t, base, name, "the daemon after the rail is hidden", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols && s.width == before.width+rail
	})
	waitPaneBox(t, b, paneBox(a.Screen()), "the second client's panes against the first's")
	saveArtifact(t, a, dir, "a-hidden")
	saveArtifact(t, b, dir, "b-hidden")

	toggleRail(t, b)
	waitRail(t, b, true, rail, "the second client after it showed the rail")
	waitRail(t, a, true, rail, "the first client after the second showed the rail")
	waitSpan(t, base, name, "the daemon after the rail is shown again", func(s paneSpan) bool {
		return s.left == rail && s.right == bigCols && s.width == before.width
	})
	waitPaneBox(t, a, paneBox(b.Screen()), "the first client's panes against the second's")
	saveArtifact(t, a, dir, "a-shown-again")
	saveArtifact(t, b, dir, "b-shown-again")
}

// TestHiddenRailSurvivesADaemonRestart hides the rail, kills the server and
// attaches to the restored session with a config that has the rail on. The
// session keeps the rail hidden: the daemon saves it with the session and puts
// it back on restore.
//
// NEGATIVE CONTROL: fails on main at "the daemon before the restart", the
// stale rectangles TestHidingTheRailGrowsThePanes is about. With RestoreSidebar
// removed from the restore it fails at "the client after the restart", which
// shows the rail its config asks for.
func TestHiddenRailSurvivesADaemonRestart(t *testing.T) {
	base := t.TempDir()
	const name = "railrs"
	first := railSession(t, base, name, "bsp", startOpts{})
	toggleRail(t, first)
	waitRail(t, first, false, 0, "the client after the rail is hidden")
	waitSpan(t, base, name, "the daemon before the restart", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols
	})

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	waitExit(t, first, "after kill-server")
	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "railrs-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}
	waitForSessionInfo(t, base, name)

	second := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows})
	waitRail(t, second, false, 0, "the client after the restart")
	waitSpan(t, base, name, "the daemon after the restart", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols
	})
	saveArtifact(t, second, artifactDir(t), "hidden-after-restart")
}

// TestOldClientKeepsItsOwnRail runs a client that does not take the rail as
// session state (TUIOS_SIDEBAR_LEGACY=1 makes this build behave as one from
// before it) beside a current client. Each keeps its own rail, as before: a
// toggle on either reaches only that client, and both still draw one box.
//
// NEGATIVE CONTROL: with the capability gate removed (the client taking
// SidebarOps from the welcome whatever TUIOS_SIDEBAR_LEGACY says), it fails at
// "the old client after the new one hid its rail": the old client hides its
// rail too. On main it fails at "the daemon with both rails hidden", the stale
// rectangles TestHidingTheRailGrowsThePanes is about.
func TestOldClientKeepsItsOwnRail(t *testing.T) {
	base := t.TempDir()
	const name = "railold"
	cur := railSession(t, base, name, "bsp", startOpts{})
	rail := spanOf(waitSpan(t, base, name, "the daemon with the rail shown", func(s paneSpan) bool {
		return s.left > 0 && s.right == bigCols
	})).left

	home := t.TempDir()
	writeConfigIn(t, home, sessionRailConfig("bsp", true))
	old := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows,
		env: []string{"XDG_CONFIG_HOME=" + home, "TUIOS_SIDEBAR_LEGACY=1"}})
	waitRail(t, old, true, rail, "the old client attaching")

	toggleRail(t, cur)
	if err := cur.WaitFor(func(s tuitest.Screen) bool { return !railShown(s) }, uiTimeout); err != nil {
		t.Fatalf("the current client never hid its rail\n%s", cur.Snapshot())
	}
	time.Sleep(2 * time.Second)
	// The old client still draws its rail, so the session keeps the rail's
	// columns, and the current client leaves them blank.
	waitRail(t, old, true, rail, "the old client after the new one hid its rail")
	waitPaneBox(t, cur, paneBox(old.Screen()), "the current client's panes against the old client's")
	dir := artifactDir(t)
	saveArtifact(t, cur, dir, "current-hidden")
	saveArtifact(t, old, dir, "old-shown")

	// The old client's own toggle still works, and reaches only it. Both rails
	// are now hidden, so the panes take the whole width.
	toggleRail(t, old)
	waitRail(t, old, false, 0, "the old client after it hid its own rail")
	waitRail(t, cur, false, 0, "the current client after the old one hid its rail")
	waitSpan(t, base, name, "the daemon with both rails hidden", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols
	})
	saveArtifact(t, old, dir, "old-hidden")
}

// TestChromeSetFromTheCommandLineRetilesThePanes moves the chrome with
// set-config, which reaches the client as a command rather than as a key, so
// no input path notices it. Hiding the dock gives its rows to the panes, and
// turning the rail off gives its columns, in the daemon's rectangles as well
// as on the screen.
//
// NEGATIVE CONTROL: fails on main at "the daemon after the dock is hidden":
// the dock went away and the panes stayed where they were, because nothing
// on that path laid them out again or told the daemon. With settleChrome's
// call removed from Update it fails at the same step.
func TestChromeSetFromTheCommandLineRetilesThePanes(t *testing.T) {
	base := t.TempDir()
	const name = "railcli"
	term := railSession(t, base, name, "bsp", startOpts{})
	before := waitSpan(t, base, name, "the daemon with the rail and the dock shown", func(s paneSpan) bool {
		return s.left > 0 && s.right == bigCols
	})
	rail, top, height := before[0].X, before[0].Y, before[0].Height
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "dock-top")

	if out, err := tuiosCLI(t, base, "set-config", "-s", name, "dockbar_position", "hidden"); err != nil {
		t.Fatalf("set-config dockbar_position hidden: %v\n%s", err, out)
	}
	waitForShape(t, base, name, 2, "the daemon after the dock is hidden", func(rects []winRect) error {
		for _, r := range rects {
			if r.Y != 0 || r.Height != height+top {
				return fmt.Errorf("a pane is at row %d and %d rows tall, want row 0 and %d rows", r.Y, r.Height, height+top)
			}
		}
		return nil
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		starts := paneStarts(s)
		return len(starts) > 0 && strings.HasPrefix(starts[0], "0,")
	}, uiTimeout); err != nil {
		t.Fatalf("the client never drew its panes from the top row: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "dock-hidden")

	if out, err := tuiosCLI(t, base, "set-config", "-s", name, "appearance.sidebar.enabled", "false"); err != nil {
		t.Fatalf("set-config appearance.sidebar.enabled false: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		starts := paneStarts(s)
		return !railShown(s) && len(starts) > 0 && starts[0] == "0,0"
	}, uiTimeout); err != nil {
		t.Fatalf("the client never hid its rail: %v\n%s", err, term.Snapshot())
	}
	waitSpan(t, base, name, "the daemon after the rail is turned off", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols && s.width == spanOf(before).width+rail
	})
	saveArtifact(t, term, dir, "rail-off")
}

// TestAnEmptySessionTakesTheRailOfItsMaker hides the rail, then makes a new
// session from the switcher. The new session has no windows. It takes the
// rail as its maker has it, so a second client whose config shows the rail
// attaches to it with the rail hidden, and the first client keeps it hidden.
//
// NEGATIVE CONTROL: fails on 91985c0 (the first round of this change) at
// "the second client attaching to the empty session". A switch to a session
// with no windows skipped RestoreFromState, which is where the rail was taken
// and offered, so the session had no value, the second client settled it from
// its own config, and the first client's rail came back.
func TestAnEmptySessionTakesTheRailOfItsMaker(t *testing.T) {
	base := t.TempDir()
	const name = "railmaker"
	a := railSession(t, base, name, "bsp", startOpts{})
	toggleRail(t, a)
	waitRail(t, a, false, 0, "the first client after it hid the rail")

	sendKeys(t, a, tuitest.Ctrl('b'), "S")
	if err := a.WaitForText(name, uiTimeout); err != nil {
		t.Fatalf("the session switcher never opened: %v\n%s", err, a.Snapshot())
	}
	sendKeys(t, a, "zzspawn")
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, a, tuitest.Enter)
	if err := a.WaitForText("Session: zzspawn", uiTimeout); err != nil {
		t.Fatalf("the first client never switched to the new session: %v\n%s", err, a.Snapshot())
	}
	time.Sleep(time.Second)
	if railShown(a.Screen()) {
		t.Fatalf("the first client shows the rail in the new session\n%s", a.Snapshot())
	}

	home := t.TempDir()
	writeConfigIn(t, home, sessionRailConfig("bsp", true))
	// attachSmall, because a session with no windows shows the welcome
	// screen and no mode banner.
	b := attachSmall(t, base, "zzspawn", startOpts{cols: bigCols, rows: bigRows, env: []string{"XDG_CONFIG_HOME=" + home}})
	time.Sleep(2 * time.Second)
	dir := artifactDir(t)
	saveArtifact(t, a, dir, "maker")
	saveArtifact(t, b, dir, "second")
	if railShown(b.Screen()) {
		t.Fatalf("the second client attaching to the empty session shows the rail\n%s", b.Snapshot())
	}
	if railShown(a.Screen()) {
		t.Fatalf("the first client's rail came back when the second client attached\n%s", a.Snapshot())
	}
}

// TestRailOpenedForTheKeyboardMovesNoPanes hides the rail on a session with
// two clients, then opens the rail's keyboard scope on one of them. The rail
// opens on that client only, drawn over its panes. No pane moves, on either
// client or in the daemon, and the other client shows no blank band.
//
// NEGATIVE CONTROL: fails on 91985c0 (the first round of this change) at
// "the daemon with the scope open": the rail opened for the scope counted in
// the client's reserve, so the session's panes moved right by the rail's
// width and the other client drew a blank band. With the check of
// SidebarRevealedForFocus removed from OwnLayoutReserve it fails at the same
// step.
func TestRailOpenedForTheKeyboardMovesNoPanes(t *testing.T) {
	base := t.TempDir()
	const name = "railscope"
	a := railSession(t, base, name, "bsp", startOpts{})
	toggleRail(t, a)
	waitRail(t, a, false, 0, "the first client after it hid the rail")
	home := t.TempDir()
	writeConfigIn(t, home, sessionRailConfig("bsp", true))
	b := attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows, env: []string{"XDG_CONFIG_HOME=" + home}})
	waitRail(t, b, false, 0, "the second client attaching")
	before := spanOf(waitSpan(t, base, name, "the daemon with the rail hidden", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols
	}))

	sendKeys(t, a, tuitest.Ctrl('b'), "e")
	if err := a.WaitFor(railShown, uiTimeout); err != nil {
		t.Fatalf("the scope never opened the rail: %v\n%s", err, a.Snapshot())
	}
	time.Sleep(2 * time.Second)
	dir := artifactDir(t)
	saveArtifact(t, a, dir, "a-scope-open")
	saveArtifact(t, b, dir, "b-scope-open")
	waitSpan(t, base, name, "the daemon with the scope open", func(s paneSpan) bool {
		return s == before
	})
	if got := paneSpanRight(a.Screen()); got != bigCols {
		t.Errorf("the client with the scope open draws its panes to column %d, want %d\n%s", got, bigCols, a.Snapshot())
	}
	waitRail(t, b, false, 0, "the other client with the scope open")

	sendKeys(t, a, tuitest.Esc)
	if err := a.WaitFor(func(s tuitest.Screen) bool { return !railShown(s) }, uiTimeout); err != nil {
		t.Fatalf("leaving the scope never hid the rail: %v\n%s", err, a.Snapshot())
	}
	waitRail(t, a, false, 0, "the first client after the scope closed")
	waitSpan(t, base, name, "the daemon after the scope closed", func(s paneSpan) bool {
		return s == before
	})
}

// rectStarts is where the daemon's rectangles begin, in the form paneStarts
// reads a frame in: row,column of each top-left corner, sorted.
func rectStarts(rects []winRect) []string {
	starts := make([]string, 0, len(rects))
	for _, r := range rects {
		starts = append(starts, fmt.Sprintf("%d,%d", r.Y, r.X))
	}
	slices.Sort(starts)
	return starts
}

// TestDaemonKeepsTheLayoutTheClientsDraw runs a 200-column and a 75-column
// client under window_size latest and toggles the rail on each in turn. Each
// toggle is input, so it also hands the session to the client that toggled,
// and the session's size, the wide client's rail width and the reserve all
// move together: the daemon answers several times in a row. After every
// toggle the daemon's rectangles must settle on what the clients draw.
//
// NEGATIVE CONTROL: fails 3 of 3 on 91985c0 (the first round of this change):
// the client pushed only the layout of the first answer after its own
// announce, which could be a middle one, and the daemon kept rectangles
// neither client drew. With the push after a newer generation cut out it fails
// 2 of 2. With the daemon's stale-generation check removed it passes 5 of 5:
// each client pushes again for every newer generation, so the last push to
// land is a current one. The check is covered by
// TestAPushTiledInAnOlderLayoutKeepsTheSessionsRectangles in internal/session.
func TestDaemonKeepsTheLayoutTheClientsDraw(t *testing.T) {
	base := t.TempDir()
	const name = "railgen"
	writeConfig(t, base, "[daemon]\nwindow_size = \"latest\"\n"+sessionRailConfig("bsp", true))
	if out, err := tuiosCLI(t, base, "new", "-d", name); err != nil {
		t.Fatalf("create the detached session: %v\n%s", err, out)
	}
	wide := attachIn(t, base, name, startOpts{cols: 200, rows: 50})
	waitForSettledGeometryIn(t, base, name, 1)
	if out, err := tuiosCLI(t, base, "run-command", "-s", name, "NewWindow"); err != nil {
		t.Fatalf("open the second window: %v\n%s", err, out)
	}
	waitForSettledGeometryIn(t, base, name, 2)
	narrow := attachSmall(t, base, name, startOpts{cols: 75, rows: 24})
	dir := artifactDir(t)

	const toggles = 20
	for i := range toggles {
		term, who := wide, "wide"
		if i%2 == 1 {
			term, who = narrow, "narrow"
		}
		toggleRail(t, term)
		// The session follows the client that toggled, so that client draws
		// the whole session and its frame is the layout to compare with.
		var last []string
		deadline := time.Now().Add(shellTimeout)
		for {
			rects := waitForSettledGeometryIn(t, base, name, 2)
			want := rectStarts(rects)
			got := paneStarts(term.Screen())
			if slices.Equal(want, got) {
				break
			}
			last = got
			if time.Now().After(deadline) {
				saveArtifact(t, wide, dir, fmt.Sprintf("wide-%02d", i))
				saveArtifact(t, narrow, dir, fmt.Sprintf("narrow-%02d", i))
				t.Fatalf("toggle %d on the %s client: the daemon holds panes at %v, the client draws them at %v\n%s",
					i, who, want, last, term.Snapshot())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	saveArtifact(t, wide, dir, "wide-last")
	saveArtifact(t, narrow, dir, "narrow-last")
}
