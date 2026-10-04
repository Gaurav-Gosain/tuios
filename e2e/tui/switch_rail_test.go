package tuie2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail is session state, so a session switch can move it: the session
// being shown has its own. The box the panes are laid out in has to follow
// the rail of the session on screen after every switch, in the daemon's
// rectangles and on the screen, and a toggle after a switch has to move the
// panes as it does before one.

// switchToNext moves the client to the other of the two sessions and waits
// for the switch to land.
func switchToNext(t *testing.T, term *tuitest.Terminal, to string) {
	t.Helper()
	sendKeys(t, term, tuitest.Alt("N"))
	if err := term.WaitForText("Session: "+to, uiTimeout); err != nil {
		t.Fatalf("the switch to %s never landed: %v\n%s", to, err, term.Snapshot())
	}
	// The first keys after a switch are dropped without this. See
	// switchSession.
	time.Sleep(insertGuard + 150*time.Millisecond)
}

// TestSwitchedSessionTakesItsOwnRail is the report: after the rail was
// toggled and the session switched, the panes did not grow or shrink to take
// the space the rail gave back or took.
//
// The first session's rail is toggled several times, so its layout
// generation runs ahead of the second session's. Then the client switches,
// toggles the rail in the second session, and switches back.
//
// NEGATIVE CONTROL: fails on main (524e2f46) at "the daemon after the rail
// is hidden in bravo". The switch's attach reply did not reset the layout
// generation the client had taken, so every resize answer from the second
// session, numbered lower than the first session's, was dropped as stale and
// the panes kept the box of the rail. With the generation reset cut from
// attachWhileReading it fails at the same step.
func TestSwitchedSessionTakesItsOwnRail(t *testing.T) {
	base := t.TempDir()
	term := railSession(t, base, "alpha", "bsp", startOpts{})
	if out, err := tuiosCLI(t, base, "new", "-d", "bravo"); err != nil {
		t.Fatalf("create bravo: %v\n%s", err, out)
	}
	// A client of bravo applies the config's startup tiling there, and
	// leaves.
	maker := attachIn(t, base, "bravo", startOpts{cols: bigCols, rows: bigRows})
	waitForSettledGeometryIn(t, base, "bravo", 1)
	if err := maker.Close(); err != nil {
		t.Fatalf("close the client of bravo: %v", err)
	}
	rail := spanOf(waitSpan(t, base, "alpha", "alpha with the rail shown", func(s paneSpan) bool {
		return s.left > 0 && s.right == bigCols
	})).left
	dir := artifactDir(t)

	// Four toggles: the rail ends shown, and alpha's generation is ahead.
	for i := range 4 {
		toggleRail(t, term)
		waitRail(t, term, i%2 == 1, map[bool]int{true: rail, false: 0}[i%2 == 1], fmt.Sprintf("alpha after toggle %d", i+1))
	}

	// bravo: one pane, and the rail shown, as its maker had it.
	bravoSpan := func(what string, left int) {
		t.Helper()
		waitForShape(t, base, "bravo", 1, what, func(rects []winRect) error {
			if r := rects[0]; r.X != left || r.X+r.Width != bigCols {
				return fmt.Errorf("the pane spans %d..%d, want %d..%d", r.X, r.X+r.Width, left, bigCols)
			}
			return nil
		})
	}
	switchToNext(t, term, "bravo")
	waitRail(t, term, true, rail, "the client in bravo")
	bravoSpan("the daemon with the rail shown in bravo", rail)
	saveArtifact(t, term, dir, "bravo-shown")

	toggleRail(t, term)
	waitRail(t, term, false, 0, "the client after the rail is hidden in bravo")
	bravoSpan("the daemon after the rail is hidden in bravo", 0)
	saveArtifact(t, term, dir, "bravo-hidden")

	// Back in alpha, whose rail is still shown.
	switchToNext(t, term, "alpha")
	waitRail(t, term, true, rail, "the client back in alpha")
	waitSpan(t, base, "alpha", "the daemon back in alpha", func(s paneSpan) bool {
		return s.left == rail && s.right == bigCols
	})
	saveArtifact(t, term, dir, "alpha-back")

	toggleRail(t, term)
	waitRail(t, term, false, 0, "the client after the rail is hidden back in alpha")
	waitSpan(t, base, "alpha", "the daemon after the rail is hidden back in alpha", func(s paneSpan) bool {
		return s.left == 0 && s.right == bigCols
	})
	saveArtifact(t, term, dir, "alpha-hidden")

	// And in bravo again, whose rail is hidden.
	switchToNext(t, term, "bravo")
	waitRail(t, term, false, 0, "the client in bravo again")
	bravoSpan("the daemon in bravo again", 0)
	saveArtifact(t, term, dir, "bravo-again")
}

// TestSwitchIntoASmallerSessionKeepsItsSize switches a wide client into a
// session a narrow client holds. Under smallest the session stays the narrow
// client's size, so the wide client has to draw its panes in those columns
// and lay them out there: the shells are the same shells.
//
// NEGATIVE CONTROL: fails on main (524e2f46) at "the wide client after the
// switch". The switch took the wide client's own terminal as the session's
// size, and the daemon, whose size did not move, had nothing to announce, so
// the wide client drew the panes 120 columns wide over a session of 80. With
// the box in rebuildForSession taken from the client's own size again it
// fails at the same step.
func TestSwitchIntoASmallerSessionKeepsItsSize(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	narrow := attachSmall(t, base, "bravo", startOpts{cols: 80, rows: 24})
	wide := attachIn(t, base, "alpha", startOpts{cols: bigCols, rows: bigRows})
	waitSpanRight(t, wide, bigCols, "the wide client alone in alpha")

	switchToNext(t, wide, "bravo")
	waitSpanRight(t, wide, 80, "the wide client after the switch")
	waitForShape(t, base, "bravo", 1, "the daemon after the switch", func(rects []winRect) error {
		if r := rects[0]; r.X+r.Width > 80 {
			return fmt.Errorf("the pane reaches column %d, past the session's 80", r.X+r.Width)
		}
		return nil
	})
	dir := artifactDir(t)
	saveArtifact(t, wide, dir, "wide-in-bravo")
	saveArtifact(t, narrow, dir, "narrow-in-bravo")
	if w, h, _ := sessionPolicySize(t, base, "bravo"); w != 80 || h != 24 {
		t.Fatalf("bravo is %dx%d, want 80x24", w, h)
	}

	// Back in alpha, alone again: the whole terminal.
	switchToNext(t, wide, "alpha")
	waitSpanRight(t, wide, bigCols, "the wide client back in alpha")
}
