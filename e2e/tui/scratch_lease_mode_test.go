package tuie2e

import (
	"testing"
	"time"
)

// kittyCtrlAltB is the leader, ctrl+b, struck while the left Option key is
// held; kittyAltG is g then. The hold takes Option off both, so they are the
// leader and the scratch key.
const (
	kittyCtrlAltB = "\x1b[98;7u"
	kittyAltG     = "\x1b[103;3u"
)

// TestScratchShownDuringAHoldHandsBackTerminalMode: in terminal mode the
// person holds the hold key, shows the scratch pane with the leader and g,
// lets go, and hides the scratch pane again. The scratch pane saved the mode
// it saw, which was the hold's borrowed window management, and hiding it
// handed that back, so the person typing in a pane before ended in window
// management. Hiding must hand back terminal mode, and the shell must
// answer.
//
// Negative control (NEGATIVE_CONTROLS.md, "The scratch pane beneath a
// hold"): the branch's parent.
func TestScratchShownDuringAHoldHandsBackTerminalMode(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, holdModeConfig)
	term := startScratchOuter(t, base)
	waitWindowCount(t, term, 1, "a pane to type into")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
	holdKeys(t, term, "1-show", kittyLeftAltPress, kittyCtrlAltB, kittyAltG, kittyLeftAltRelease)
	waitScratch(t, term, base, false, false, "shown during a hold")
	time.Sleep(insertGuard)
	runInShell(t, term, "echo scratch-$((2+3))", "scratch-5", shellTimeout)
	toggleScratch(t, term)
	time.Sleep(500 * time.Millisecond)
	saveArtifact(t, term, artifactDir(t), "2-hidden")
	time.Sleep(insertGuard)
	runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
}
