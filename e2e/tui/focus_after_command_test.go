package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestFocusKeyRightAfterACommandFocus moves the focus with focus-window, waits
// for the client to draw it, and presses a focus key at once. The key must
// move the focus from the pane the client shows.
//
// focus-window moves the daemon's focus and then routes a command to the
// client to reveal the pane. The client drew the new focus from that command
// while it still held the session's version from before the move, and the
// push the key made carried that version. The daemon took its focus for a
// stale one and refused it: the key did nothing. TestDirectionalFocusWrapBSP
// failed so on a loaded CI runner.
//
// The ways it can go wrong, each an assertion here:
//   - the daemon writes the routed command before the state push queued
//     ahead of it, so the client has the command first;
//   - the client reads both in order and takes the command first in Update,
//     because each comes through a listener of its own;
//   - a command that waits for its state never runs.
//
// TUIOS_TEST_STATE_PUSH_HOLD_MS holds each state push in the daemon, and
// TUIOS_TEST_STATE_SYNC_HOLD_MS holds each state in the client before Update
// takes it, so each of the first two goes wrong on every round, not only on a
// loaded machine. The rounds go under artifactDir as rounds.txt.
func TestFocusKeyRightAfterACommandFocus(t *testing.T) {
	const session = "cmdfocus"
	base := t.TempDir()
	term := startIn(t, base, startOpts{cols: 160, rows: 48, args: []string{"new", session},
		env: []string{"TUIOS_TEST_STATE_PUSH_HOLD_MS=300", "TUIOS_TEST_STATE_SYNC_HOLD_MS=700"}})
	killDaemon(t, base)
	waitBoot(t, term)
	for range 4 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 4, "command focus setup")
	enableTiling(t, term)
	waitForSettledGeometryIn(t, base, session, 4)
	layout, _ := focusedLayout(t, base, session)

	// The top right pane, and the pane left of it.
	var from focusedRect
	for _, r := range layout {
		if r.Y == 0 && r.X > 0 {
			from = r
		}
	}
	left := touchingNeighbours(from.winRect, layout, "left")
	if from.ID == "" || len(left) == 0 {
		t.Fatalf("the test is wrong: no top right pane with a pane on its left in %+v", layout)
	}

	var rounds []string
	defer func() {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "rounds.txt"), []byte(strings.Join(rounds, "\n")+"\n"), 0o644)
	}()
	for i := range 5 {
		if out, err := tuiosCLI(t, base, "focus-window", "-s", session, from.ID); err != nil {
			t.Fatalf("focus-window: %v\n%s", err, out)
		}
		waitClientFocus(t, term, layout, from.ID)
		send(t, term, tuitest.Ctrl('b'), tuitest.Left)
		got := waitFocusChange(t, base, session, from.ID)
		rounds = append(rounds, fmt.Sprintf("round %d: focus-window %s, then ctrl+b left: the daemon's focus is %s", i, from.ID[:8], got[:8]))
		if !left[got] {
			t.Fatalf("ASSERTION: round %d: ctrl+b left right after focus-window %s left the focus on %s, want the pane on its left\n%s",
				i, from.ID[:8], got[:8], term.Snapshot())
		}
		waitClientFocus(t, term, layout, got)
	}
}
