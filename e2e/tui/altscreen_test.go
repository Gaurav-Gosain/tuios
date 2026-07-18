package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// setupAltScreenLeftTile boots tuios with shared borders, puts an idle
// alt-screen application whose first row is blank into the first window, adds a
// second window, and tiles them. The alt-screen pane ends up as the leftmost
// tile at x=0 and unfocused, which is every precondition the clip bug needed.
//
// It returns the markers the fixture painted.
func setupAltScreenLeftTile(t *testing.T, term *tuitest.Terminal) []string {
	t.Helper()
	markers := []string{"ALTMARK-ALPHA", "ALTMARK-BETA"}

	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	if err := term.SendKeys(altScreenCmd(markers...), tuitest.Enter); err != nil {
		t.Fatalf("start alt-screen fixture: %v", err)
	}
	waitForAll(t, term, 15*time.Second, "alt-screen fixture never painted", markers...)
	leaveTerminalMode(t, term)

	// A second window steals focus, so the alt-screen pane renders through the
	// unfocused fast path that trims trailing spaces off every line.
	newWindow(t, term)
	waitWindowCount(t, term, 2, "after second window")

	// Tile them side by side. The first window becomes the leftmost tile at
	// x=0, and with shared borders its content is composited raw.
	enableTiling(t, term, markers...)
	return markers
}

// TestAltScreenPaneSurvivesFocusSwitch is the regression test for a tiled pane
// compositing as bare background while its terminal renderer was producing
// perfectly good content.
//
// An idle alt-screen application in the leftmost tile went blank the moment
// focus moved elsewhere, and came back on refocus. clipWindowContent measured
// the window width from lines[0] alone; the unfocused fast path right-trims
// every line and shared borders hand the compositor raw content, so an
// application with a blank top row measured zero columns wide. The guard
// x+windowWidth <= 0 was then true for the leftmost tile at x=0 and the whole
// frame was discarded, and the empty layer was cached and re-served forever.
//
// The assertion that matters is that content is present WHILE THE PANE IS
// UNFOCUSED. An earlier version of this test only checked that the markers were
// on screen at some point during the cycle, and it passed against the broken
// binary, because focusing the pane rebuilds its layer and makes it correct
// again. Sampling only after a focus change is what makes this test evidence.
//
// Negative control: verified to fail against a binary with commit b9f770b's
// change to internal/app/render_helpers.go reverted. See
// NEGATIVE_CONTROLS.md.
func TestAltScreenPaneSurvivesFocusSwitch(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--shared-borders"}})
	markers := setupAltScreenLeftTile(t, term)

	// Cycle focus back and forth. Every other Tab leaves the alt-screen pane
	// unfocused, which is the state the bug blanked.
	for i := range 6 {
		if err := term.SendKeys(tuitest.Tab); err != nil {
			t.Fatalf("tab %d: %v", i, err)
		}
		// Wait for the markers rather than sleeping a fixed amount, so a slow
		// machine cannot turn a real blank into a passing sample or vice versa.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return screenHas(s, markers...)
		}, uiTimeout); err != nil {
			t.Fatalf("alt-screen pane went blank after focus switch %d/6. "+
				"The pane's content was discarded by the clip path and the empty "+
				"layer cached.\nerr: %v\n%s", i+1, err, term.Snapshot())
		}
		alive(t, term, "during focus cycling")
	}

	// The fixture emits nothing at all, so surviving this long proves the
	// cached layer stayed correct rather than being repaired by fresh output.
	time.Sleep(2 * time.Second)
	if !screenHas(term.Screen(), markers...) {
		t.Fatalf("alt-screen pane blanked while idle after focus cycling\n%s", term.Snapshot())
	}
}

// TestLeftmostTileWithBlankFirstLineIsNotDiscarded pins the clip bug directly,
// at the geometry that triggered it: the leftmost tile at x=0, whose content's
// first line is empty.
//
// Where the focus-switch test above asserts the pane survives an interaction,
// this one asserts the specific spatial fact the bug destroyed: the content is
// rendered in the LEFT half of the screen. The distinction matters because the
// broken code discarded the leftmost tile's frame entirely while leaving the
// right-hand tile and all the surrounding chrome intact, so a test that only
// looked for the text anywhere on screen could be satisfied by the wrong pane.
//
// Negative control: verified to fail against the reverted-b9f770b binary.
func TestLeftmostTileWithBlankFirstLineIsNotDiscarded(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--shared-borders"}})
	markers := setupAltScreenLeftTile(t, term)

	// Move focus to the other tile and keep it there: unfocused is the state
	// the leftmost tile was discarded in.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return leftHalfHas(s, markers...)
	}, uiTimeout); err != nil {
		t.Fatalf("leftmost tile at x=0 was discarded: %v\n%s", err, term.Snapshot())
	}

	s := term.Screen()
	cols, _ := s.Size()
	if !leftHalfHas(s, markers...) {
		t.Fatalf("markers %v are not in the left half (cols 0..%d) of the screen; "+
			"the leftmost tile rendered as bare background\n%s",
			markers, cols/2, term.Snapshot())
	}

	// The fixture's first row really is blank; if it were not, the test would
	// not be exercising the bug at all. Assert it, so a future change to
	// altScreenCmd cannot silently defang this test.
	if row, ok := markerRow(s, markers[0]); !ok {
		t.Fatal("could not locate the first marker row")
	} else if row == 0 {
		t.Fatal("the fixture painted its first marker on row 0: the blank-first-line " +
			"precondition is gone and this test no longer covers the clip bug")
	} else if got := strings.TrimSpace(s.Line(row - 1)[:cols/2]); got != "" {
		t.Fatalf("the row above the first marker is %q, want blank: the "+
			"blank-first-line precondition is gone", got)
	}
}

// leftHalfHas reports whether every marker appears within the left half of the
// screen, which under a two-tile layout is the leftmost tile.
func leftHalfHas(s tuitest.Screen, markers ...string) bool {
	cols, rows := s.Size()
	half := cols / 2
	var b strings.Builder
	for r := range rows {
		line := s.Line(r)
		if len(line) > half {
			line = line[:half]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	text := b.String()
	for _, m := range markers {
		if !strings.Contains(text, m) {
			return false
		}
	}
	return true
}

// markerRow returns the first screen row containing marker.
func markerRow(s tuitest.Screen, marker string) (int, bool) {
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), marker) {
			return r, true
		}
	}
	return 0, false
}
