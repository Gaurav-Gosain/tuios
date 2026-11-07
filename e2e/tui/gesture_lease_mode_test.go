package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The pointer gestures borrow window management and hand back the mode they
// began in. These are the guards for the gesture and ctrl-drag leases
// (mode.go): each begins in terminal mode, ends the gesture, and asks the
// shell to answer.
//
// Negative control (NEGATIVE_CONTROLS.md, "Pointer gestures as mode
// leases"): EndPointerGesture and EndCtrlDrag made to do nothing.

// dividerPanes is two tiled panes with a shared divider at column 80, in
// terminal mode at a shell prompt.
func dividerPanes(t *testing.T, config string) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, config+"[appearance]\nshared_borders = true\n")
	term := startIn(t, base, startOpts{cols: 160, rows: 45})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes with a divider")
	enableTiling(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
	return term
}

// TestDividerDragHandsBackTerminalMode: a divider drag begun while typing
// hands typing back when it drops.
func TestDividerDragHandsBackTerminalMode(t *testing.T) {
	term := dividerPanes(t, "")
	mouseDrag(t, term, 80, 20, 68, 20, tuitest.MouseLeft, 0)
	time.Sleep(insertGuard)
	saveArtifact(t, term, artifactDir(t), "dropped")
	runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
}

// TestHoldReleasedDuringADividerDragHandsBackTerminalMode: the hold key is
// pressed, a divider drag begins, the key is let go while the drag goes on,
// and the drag drops. Both borrows end, in the other order from
// TestHoldOutlastsADividerDrag, and terminal mode comes back.
func TestHoldReleasedDuringADividerDragHandsBackTerminalMode(t *testing.T) {
	term := dividerPanes(t, holdModeConfig)
	holdKeys(t, term, "1-held", kittyLeftAltPress)
	mousePress(t, term, 80, 20, tuitest.MouseLeft, 0)
	mouseMotion(t, term, 76, 20, tuitest.MouseLeft, 0)
	holdKeys(t, term, "2-released", kittyLeftAltRelease)
	mouseMotion(t, term, 68, 20, tuitest.MouseLeft, 0)
	mouseRelease(t, term, 68, 20, tuitest.MouseLeft, 0)
	time.Sleep(insertGuard)
	saveArtifact(t, term, artifactDir(t), "3-dropped")
	runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
}

// TestCtrlDragHandsBackTerminalMode: a tiled pane moved with ctrl and the
// left button onto the other one while typing hands typing back when it
// lands.
func TestCtrlDragHandsBackTerminalMode(t *testing.T) {
	term := dividerPanes(t, "")
	before := term.Screen().Text()
	mouseDrag(t, term, 120, 12, 40, 12, tuitest.MouseLeft, tuitest.ModCtrl)
	time.Sleep(insertGuard)
	saveArtifact(t, term, artifactDir(t), "dropped")
	if term.Screen().Text() == before {
		t.Fatalf("the ctrl-drag moved nothing, so it proves nothing about the mode\n%s", term.Snapshot())
	}
	runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
}
