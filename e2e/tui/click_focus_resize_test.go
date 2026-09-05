package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Clicking a pane to focus it must not resize the shell inside it.
//
// With shared borders on, a tiled pane is borderless and the whole rectangle
// belongs to the guest. The drag setup took that allowance away on the press,
// which is two rows and two columns of real estate, and the retile on release
// gave it back. Each of those is a SIGWINCH, and a shell repaints its prompt on
// a SIGWINCH, so picking a pane with the mouse left a new line in it. The end
// state is identical either way, which is why this counts what the pane
// received rather than comparing sizes afterwards.
//
// The pane counts the signals itself: its shell prints one marker line per
// SIGWINCH, so the assertion is on rows in the grid, which is exactly the
// symptom. The marker is spelled with a runtime substitution so the echoed
// command line does not contain the string being counted.
//
// appearance.click_to_type is "double" because that is the policy that leaves a
// single click in window-management mode, which is where the drag setup runs.
// It is the maintainer's own setting and the one the report came from.

// clickFocusPanes builds two tiled borderless panes on a daemon session and
// arms a SIGWINCH counter in the second one, which tiling puts on the right.
func clickFocusPanes(t *testing.T) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nshared_borders = true\nclick_to_type = 'double'\n")
	term := startIn(t, base, startOpts{
		cols: 160, rows: 45,
		args: []string{"new", "clickfocus"},
		env:  []string{"SHELL=/bin/bash"},
	})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "click-focus setup")
	enableTiling(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`trap 'printf "WI%sCH\n" N' WINCH; clear; printf "AR%sED\n" M`,
		"ARMED", shellTimeout)
	// The trap is armed once the marker is on screen, and the clear has left
	// the command echo behind it, so every WINCH row after this came from a
	// signal.
	time.Sleep(2 * time.Second)
	leaveTerminalMode(t, term)
	time.Sleep(time.Second)
	return term
}

// countRows is how many rows of the screen contain marker.
func countRows(s tuitest.Screen, marker string) int {
	n := 0
	_, rows := s.Size()
	for r := range rows {
		if strings.Contains(s.Line(r), marker) {
			n++
		}
	}
	return n
}

// TestClickToFocusAddsNoLineToThePane is the regression.
func TestClickToFocusAddsNoLineToThePane(t *testing.T) {
	term := clickFocusPanes(t)

	// Three round trips: away to the left tile, back to the armed one.
	for range 3 {
		mouseClick(t, term, 20, 20, tuitest.MouseLeft, 0)
		time.Sleep(700 * time.Millisecond)
		mouseClick(t, term, 100, 20, tuitest.MouseLeft, 0)
		time.Sleep(700 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)

	if n := countRows(term.Screen(), "WINCH"); n != 0 {
		t.Errorf("the pane took %d resizes across three click-to-focus round trips, "+
			"and printed a line for each; a click that only picks a pane must resize nothing\n%s",
			n, term.Snapshot())
	}
	alive(t, term, "after click-to-focus round trips")
}

// TestDraggingTheDividerResizesOnce is the positive half of the test above, in
// the same fixture. Dragging the shared border between the two panes really does
// change how many columns each of them has, so the guest has to be told - once,
// for the width it settled at, not once per column the pointer crossed.
//
// Without this row the test above could pass because the fixture never resizes
// anything at all. Moving the pane is no longer that control: a pane dropped in
// a slot the same size as the one it left is told nothing now, which is what
// TestDragIntoASameSizeSlotResizesNothing requires.
func TestDraggingTheDividerResizesOnce(t *testing.T) {
	term := clickFocusPanes(t)

	// The divider between the two tiles, dragged twelve columns to the left.
	mouseDrag(t, term, 80, 20, 68, 20, tuitest.MouseLeft, 0)
	time.Sleep(3 * time.Second)

	if n := countRows(term.Screen(), "WINCH"); n != 1 {
		t.Errorf("dragging the divider across twelve columns announced %d resizes, want 1; "+
			"either the fixture cannot see a resize at all, in which case "+
			"TestClickToFocusAddsNoLineToThePane proves nothing, or the columns the "+
			"pointer crossed on the way are reaching the guest\n%s", n, term.Snapshot())
	}
	alive(t, term, "after dragging the divider")
}
