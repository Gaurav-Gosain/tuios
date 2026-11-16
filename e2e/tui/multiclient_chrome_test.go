package tuie2e

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Helpers that read the pane layout off a client's grid, so a test can check
// that two clients of one session draw the same layout. They read the grid
// rather than the model, because a fault of this kind is two clients that
// disagree.

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
// left corner of each one. It is the layout itself: where the splits fall ("the
// old ratio" in the report) and where the box begins and ends. Two clients of
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
