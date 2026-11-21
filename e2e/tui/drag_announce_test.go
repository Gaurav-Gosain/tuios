package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func dragAnnouncePanes(t *testing.T) *tuitest.Terminal {
	t.Helper()
	term, _ := dragAnnounceBoard(t)
	return term
}

func dragAnnounceBoard(t *testing.T) (*tuitest.Terminal, string) {
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
		`PS1='$ '; trap 'printf "WI%sCH\n" N' WINCH; clear; printf "AR%sED\n" M`,
		"ARMED", shellTimeout)
	time.Sleep(2 * time.Second)
	leaveTerminalMode(t, term)
	time.Sleep(time.Second)
	return term, base
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
// the swap really does change its size and the guest has to be told once, for
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
