package tuie2e

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The report this file exists for: with a split open and a browser attached
// beside the terminal, switching panes made the session resize itself, settle
// back on the old ratio, and take a line of scrollback with it each time.
//
// The cause is that a pane's rectangle was synced as an absolute number while
// the box it was measured in was worked out privately by each client, out of
// that client's own chrome. Two clients with different chrome therefore
// partitioned different boxes and each dragged the shared PTYs to its own
// answer, twice per push. Chrome that differs is ordinary: the sidebar is a
// rail one client can have and another can be running without.
//
// Asserted on the grid rather than on the model, because the whole failure was
// the two disagreeing.

// isPaneCorner reports whether a rune is a pane's top-left corner. The corner
// is what makes this measurable: a rail and a pane both draw a vertical rule,
// and on the client that has a rail the two stand in adjacent columns, so
// nothing that counts rules can tell them apart. Only a pane draws a corner.
func isPaneCorner(r rune) bool {
	switch r {
	case '╭', '┌', '┏', '╔':
		return true
	}
	return false
}

// paneStarts is where every pane begins, as row,column pairs read off the top
// left corner of each one. It is the layout itself: where the splits fall - "the
// old ratio" in the report - and where the box begins and ends. Two clients of
// one session have to agree on it exactly, because the shells behind those
// columns are the same shells.
func paneStarts(s tuitest.Screen) []string {
	cols, rows := s.Size()
	var starts []string
	for r := range rows {
		line := []rune(s.Line(r))
		for c := range min(len(line), cols) {
			if isPaneCorner(line[c]) {
				starts = append(starts, fmt.Sprintf("%d,%d", r, c))
			}
		}
	}
	slices.Sort(starts)
	return starts
}

// paneBox is what two clients have to agree about: where each pane starts and
// where the panes stop.
func paneBox(s tuitest.Screen) string {
	return fmt.Sprintf("panes %v right %d", paneStarts(s), paneSpanRight(s))
}

// waitPaneBox waits for a client to draw its panes in the columns wanted.
func waitPaneBox(t *testing.T, term *tuitest.Terminal, want, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneBox(s) == want
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the panes are at %s, want %s\n%s", what, paneBox(term.Screen()), want, term.Snapshot())
	}
}

// TestOneClientsRailDoesNotMoveAnotherClientsPanes is the agreement the report
// is about, on the grid.
//
// Two clients on one session, the same size, and one of them turns its sidebar
// on. The rail is chrome and chrome belongs to the client that draws it, so the
// panes have to end up in the same columns on both screens: a pane's box is
// shared, because the shells inside it are. The client without a rail leaves
// the band blank rather than filling it, and switching between panes moves
// nothing.
//
// NEGATIVE CONTROL: none. This is a deliberate passes-both-ways control, and
// saying so is the point of writing it down.
//
// It was written expecting to fail on the unfixed tree and does not, and the
// reason is worth keeping: on the unfixed tree the two clients reach the same
// frame anyway, by fighting to it. The client with the rail reads the other's
// rectangles as a layout for somebody else's screen, works out its own and
// pushes it; the client without one reads what comes back as settled, because a
// layout that sits inside a wider box and still reaches its far edges is
// indistinguishable from one that belongs there. So the wider client always
// yields and the frames converge - after both have dragged the shared PTYs to
// their own answer and back. The cost is real and it is not on the grid: it is
// a pair of resizes per push, one of them narrowing, which is what damages
// scrollback under a reflowing emulator.
//
// The tests that fail on the unfixed tree therefore count resizes rather than
// read frames: TestFocusSwitchResizesNothing and TestTwoClientsAgreeOnEveryPaneSize
// in internal/app. What this one is for is the property those cannot see - that
// what the two people are looking at is the same layout - and it would catch a
// change that settled the resizes by letting the two frames drift apart.
func TestOneClientsRailDoesNotMoveAnotherClientsPanes(t *testing.T) {
	railed, base := twoClientSession(t, "chrome", bigCols, bigRows)
	newWindow(t, railed)
	waitWindowCount(t, railed, 2, "two windows on the first client")
	enableTiling(t, railed)
	waitSpanRight(t, railed, bigCols, "before the sidebar is on")

	// The second client keeps its own config directory and shares only the
	// daemon. Without that the two share a config file and a watcher, so the
	// toggle below reaches the second client by the back door - which is not
	// the situation in the report, where the browser is a separate process with
	// its own configuration that nothing propagates to.
	bare := attachIn(t, base, "chrome", startOpts{
		cols: bigCols, rows: bigRows,
		env: []string{"XDG_CONFIG_HOME=" + t.TempDir()},
	})
	before := paneBox(railed.Screen())
	if len(paneStarts(railed.Screen())) != 2 {
		t.Fatalf("the fixture is not two panes side by side, so there is no split to watch\n%s", railed.Snapshot())
	}
	waitPaneBox(t, bare, before, "the second client before any chrome differs")

	// One client turns its rail on. Nothing tells the other: the sidebar is a
	// config switch, not session state, and the second client is a separate
	// process that already read its own config.
	toggleSidebarViaPalette(t, railed)
	if err := railed.WaitFor(func(s tuitest.Screen) bool {
		return paneBox(s) != before
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never took any columns off the panes: %v\n%s", err, railed.Snapshot())
	}
	// Read once it has stopped moving, so what the other client is held to is
	// the settled box rather than a frame from the middle of the re-layout.
	time.Sleep(time.Second)
	want := paneBox(railed.Screen())

	// The client with no rail draws its panes in the same columns. It has the
	// space and does not take it, which is the whole rule: a client absorbs its
	// own chrome and never moves the panes to make room.
	waitPaneBox(t, bare, want, "the client with no rail of its own")

	// A pane switch moves nothing. It is the gesture in the report, and every
	// rectangle it moves costs a live shell a resize.
	//
	// Pressed on the client with no rail, deliberately. That is the direction
	// that used to fight: its rectangles begin at a column left of where the
	// railed client's panes may, so the railed client reads them as a layout for
	// somebody else's screen and works out its own. The other direction settles
	// without an argument, because a layout that sits inside a wider box and
	// still reaches its far edges cannot be told from one that belongs there.
	for range 6 {
		if err := bare.SendKeys("\t"); err != nil {
			t.Fatalf("focus cycle: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Second)

	if got := paneBox(railed.Screen()); got != want {
		t.Errorf("switching panes moved the railed client's panes to %s, want %s\n%s",
			got, want, railed.Snapshot())
	}
	if got := paneBox(bare.Screen()); got != want {
		t.Errorf("switching panes on the other client moved this one's panes to %s, want %s\n%s",
			got, want, bare.Snapshot())
	}
}
