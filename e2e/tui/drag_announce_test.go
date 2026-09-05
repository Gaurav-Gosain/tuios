package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Dragging a tiled pane about must not resize the shell inside it.
//
// A drag is a gesture, and the sizes it passes through on the way are not sizes
// the guest was ever meant to live at: a borderless pane gives its shared-border
// allowance up on the first motion, and the retile on the drop gives it back. In
// a tiled layout the pane usually lands in a slot the same size as the one it
// left, so both of those were avoidable, and each of them is a SIGWINCH that a
// shell answers by repainting its prompt. Rearranging two panes left a new line
// in each of them.
//
// The pane counts the signals itself: its shell prints one marker line per
// SIGWINCH, so the assertion is on rows in the grid, which is exactly the
// symptom. The marker is spelled with a runtime substitution so the echoed
// command line does not contain the string being counted.

// dragAnnouncePanes builds three tiled borderless panes on a daemon session and
// arms a SIGWINCH counter in the last one. Tiling puts the first on the left at
// full height and splits the right-hand half between the other two, so the
// armed pane has a same-size neighbour under it and a different-size one beside
// it, which is what the two tests below need.
func dragAnnouncePanes(t *testing.T) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nshared_borders = true\nclick_to_type = 'double'\n")
	term := startIn(t, base, startOpts{
		cols: 160, rows: 45,
		args: []string{"new", "draganno"},
		env:  []string{"SHELL=/bin/bash"},
	})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 3 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 3, "drag-announce setup")
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

// TestDragIntoASameSizeSlotResizesNothing is the regression. The armed pane is
// dragged onto the pane below it, which tiling gave the same rectangle, so the
// two swap and the pane ends the gesture exactly the size it started.
func TestDragIntoASameSizeSlotResizesNothing(t *testing.T) {
	term := dragAnnouncePanes(t)

	mouseDrag(t, term, 120, 32, 120, 10, tuitest.MouseLeft, 0)
	time.Sleep(3 * time.Second)

	if n := countRows(term.Screen(), "WINCH"); n != 0 {
		t.Errorf("the pane took %d resizes across a drag that returned it to the same size, "+
			"and printed a line for each; a drag that changes no size must announce nothing\n%s",
			n, term.Snapshot())
	}
	alive(t, term, "after a same-size drag")
}

// TestDragIntoADifferentSizeSlotResizesOnce is the positive half, in the same
// fixture. The armed pane is dragged onto the full-height pane on the left, so
// the swap really does change its size and the guest has to be told - once, for
// the size it settled at, not once per size the gesture passed through.
//
// Without this row the test above could pass because the fixture never resizes
// anything at all.
func TestDragIntoADifferentSizeSlotResizesOnce(t *testing.T) {
	term := dragAnnouncePanes(t)

	mouseDrag(t, term, 120, 32, 40, 20, tuitest.MouseLeft, 0)
	time.Sleep(3 * time.Second)

	if n := countRows(term.Screen(), "WINCH"); n != 1 {
		t.Errorf("a drag that really did change the pane's size announced %d of them, want 1\n%s",
			n, term.Snapshot())
	}
	alive(t, term, "after a different-size drag")
}
