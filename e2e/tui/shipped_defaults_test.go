package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// This file is the only place in the suite that runs tuios the way it ships.
// Every other test sets TUIOS_NO_DAEMON=1 in startIn, because the assertions
// there are about the standalone TUI, and pins the looks from before v0.8.0
// (see pinPreV080Looks). Here nothing is set: no config file, no daemon, no
// state directory, which is the first run of a new install.

// windowDot is the disc the dots style draws each control as, config's
// WindowButtonDot. Spelled out rather than imported because this module does
// not depend on the one under test.
const windowDot = "●"

// TestAFirstRunIsDaemonBackedAndTiledWithDotsOnTheLeft drives a bare "tuios"
// against an empty home and asserts the four shipped defaults at once. They are
// one test because they are one experience: what somebody sees the first time
// they run tuios.
func TestAFirstRunIsDaemonBackedAndTiledWithDotsOnTheLeft(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, daemonDefault: true, shippedLooks: true})
	killDaemon(t, base)

	// Daemon-backed. A bare "tuios" started one and attached to it, so the
	// control socket answers and names the session this client is in.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("a bare tuios never reached a session: %v\n%s", err, term.Snapshot())
	}
	out, err := tuiosCLI(t, base, "ls")
	if err != nil {
		t.Fatalf("no daemon answered a bare tuios: %v\n%s", err, out)
	}
	t.Logf("tuios ls:\n%s", out)

	// The first run lands on the welcome screen with no window, because
	// startup.open_default_window is still off. Two windows, so tiling has
	// something to divide.
	waitWindowCount(t, term, 0, "the session a bare tuios opened")
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "after two 'n' presses")

	// Tiled, without anybody turning tiling on. Two windows that share the
	// screen and do not overlap is what tiled means here.
	rects := waitForSettledGeometry(t, base, 2)
	for _, r := range rects {
		if r.Width >= 120 {
			t.Errorf("window %s spans the full width (%d): the session is floating, not tiled", r.ID, r.Width)
		}
	}
	for a := range rects {
		for b := a + 1; b < len(rects); b++ {
			if geomOverlap(rects[a], rects[b]) {
				t.Errorf("windows overlap, so nothing tiled them: %s (%d,%d %dx%d) and %s (%d,%d %dx%d)",
					rects[a].ID, rects[a].X, rects[a].Y, rects[a].Width, rects[a].Height,
					rects[b].ID, rects[b].X, rects[b].Y, rects[b].Width, rects[b].Height)
			}
		}
	}
	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}

	// Dots, on the left. The controls are three discs, and they sit before the
	// window's title rather than after it.
	assertDotsOnTheLeft(t, term)
}

// assertDotsOnTheLeft finds the title bar row carrying the window controls and
// checks both halves of the change: the glyph is the disc, and the run of discs
// starts left of the title.
func assertDotsOnTheLeft(t *testing.T, term *tuitest.Terminal) {
	t.Helper()

	var row string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if row = titleRowWithDots(term.Screen()); row != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if row == "" {
		t.Fatalf("no title bar drew the %q controls; the pill style is still shipping\n%s",
			windowDot, term.Snapshot())
	}
	t.Logf("title bar: %q", row)

	dots := strings.Index(row, windowDot)
	// The controls sit at the start of the bar, in the first few cells: past
	// the border corner and the one-cell gap the dots style draws before the
	// first disc. Anything further in is the right-hand end.
	const leftEnd = 6
	if dots > leftEnd {
		t.Errorf("the controls start at column %d, which is not the left end of the bar: %q", dots, row)
	}
}

// titleRowWithDots returns the first screen row holding three window control
// discs, or "" when none does.
func titleRowWithDots(s tuitest.Screen) string {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if strings.Count(line, windowDot) >= 3 {
			return line
		}
	}
	return ""
}

// TestTheDockChipTracksTiling pins tilingModeIcon against the real binary.
// Every enableTiling and disableTiling in this package decides whether to press
// a key by searching the screen for that glyph, so a glyph that moved or
// changed would turn a hundred assertions into silent no-ops rather than into
// failures. This is the one test that would fail instead.
func TestTheDockChipTracksTiling(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "one window")

	// It ships tiled, so the chip is there before anything is pressed.
	if !settledTiling(t, term) {
		t.Fatalf("the dock chip does not report the tiled session tuios ships\n%s", term.Snapshot())
	}
	disableTiling(t, term)
	if settledTiling(t, term) {
		t.Errorf("the chip still reports tiling after it was turned off\n%s", term.Snapshot())
	}
	enableTiling(t, term)
	if !settledTiling(t, term) {
		t.Errorf("the chip does not report tiling after it was turned back on\n%s", term.Snapshot())
	}
}
