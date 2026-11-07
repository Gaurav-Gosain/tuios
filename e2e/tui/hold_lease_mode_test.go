package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// kittyAltX is x struck while the left Option key is held: x is close_window
// in window mode.
const kittyAltX = "\x1b[120;3u"

// kittyN is a plain n under the report-all-keys flag the hold key turns on.
const kittyN = "\x1b[110u"

// holdKeys sends each key as its own event, with a pause the client needs to
// see them apart, and saves the frame after each one.
func holdKeys(t *testing.T, term *tuitest.Terminal, frame string, keys ...string) {
	t.Helper()
	art := artifactDir(t)
	for i, k := range keys {
		if err := term.SendKeys(tuitest.Key(k)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		saveArtifact(t, term, art, frame+"-"+string(rune('1'+i)))
	}
}

// TestHoldReleaseAfterTheLastPaneClosedStaysInWindowMode: in terminal mode
// the person holds the hold key, closes the only pane with x, and lets go.
// Closing the last pane is a choice of window management: there is nothing
// left to type into. The release used to hand back the terminal mode the hold
// began in, with no pane to reach. The next key, n for a new pane, went to the
// terminal key path, which found no pane and only switched to window mode, so
// the key did nothing.
//
// Negative control (NEGATIVE_CONTROLS.md, "The hold key as a mode lease"):
// the branch's parent, where the hold saves and restores the mode itself.
func TestHoldReleaseAfterTheLastPaneClosedStaysInWindowMode(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, holdModeConfig)
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "a pane to close")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
	holdKeys(t, term, "1-close", kittyLeftAltPress, kittyAltX)
	waitWindowCount(t, term, 0, "the pane closed while held")
	holdKeys(t, term, "2-release", kittyLeftAltRelease, kittyN)
	waitWindowCount(t, term, 1, "n after the release opens a pane")
}

// kittyAltN is n struck while the left Option key is held. Held, it is n in
// window mode, new_window. In terminal mode alt+n moves to the next pane.
const kittyAltN = "\x1b[110;3u"

// TestHoldOutlastsADividerDrag: in terminal mode the person drags the divider
// between two tiled panes, which borrows window management, presses the hold
// key, and lets go of the mouse while still holding. The drag handed back
// terminal mode at once, under a held key whose pill still said HOLD, so n
// struck while held reached terminal mode as alt+n and moved to the next pane.
// It must stay window management until the hold key is let go, then hand back
// terminal mode.
//
// Negative control (NEGATIVE_CONTROLS.md, "The hold key as a mode lease"):
// the branch's parent.
func TestHoldOutlastsADividerDrag(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, holdModeConfig+"[appearance]\nshared_borders = true\n")
	term := startIn(t, base, startOpts{cols: 160, rows: 45})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes with a divider")
	enableTiling(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
	mousePress(t, term, 80, 20, tuitest.MouseLeft, 0)
	mouseMotion(t, term, 74, 20, tuitest.MouseLeft, 0)
	holdKeys(t, term, "1-held", kittyLeftAltPress)
	mouseRelease(t, term, 74, 20, tuitest.MouseLeft, 0)
	time.Sleep(300 * time.Millisecond)
	holdKeys(t, term, "2-n-held", kittyAltN)
	waitWindowCount(t, term, 3, "n struck while held after the drag ended")
	holdKeys(t, term, "3-release", kittyLeftAltRelease)
	time.Sleep(insertGuard)
	runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
}
