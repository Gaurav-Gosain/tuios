package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const windowDot = "●"

// TestAFirstRunIsDaemonBackedAndTiledWithDotsOnTheLeft drives a bare "tuios"
// against an empty home and asserts the four shipped defaults at once. They are
// one test because they are one experience: what somebody sees the first time
// they run tuios.
func TestAFirstRunIsDaemonBackedAndTiledWithDotsOnTheLeft(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, daemonDefault: true, shippedLooks: true})
	killDaemon(t, base)

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

	waitWindowCount(t, term, 0, "the session a bare tuios opened")
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "after two 'n' presses")

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

	assertDotsOnTheLeft(t, term)
}

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
	const leftEnd = 6
	if dots > leftEnd {
		t.Errorf("the controls start at column %d, which is not the left end of the bar: %q", dots, row)
	}
}

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
