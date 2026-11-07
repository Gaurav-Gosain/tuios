package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// kittyAltS is s struck while the left Option key is held: s is focus_sidebar
// in window mode, so it gives the rail the keyboard during a hold.
const kittyAltS = "\x1b[115;3u"

// TestHoldThenRailHandsBackTerminalMode: the hold key and the rail both
// borrow and hand back. In terminal mode the person holds the key, opens the
// rail with s, lets go, and leaves the rail with esc. The rail saved the
// mode it saw, which was the hold's borrowed window mode, and esc handed that
// back, so the next line typed ran as window actions. Both orders of release
// and esc must end in terminal mode, and the shell must answer.
//
// The negative control is in NEGATIVE_CONTROLS.md, "Hold, then the rail".
func TestHoldThenRailHandsBackTerminalMode(t *testing.T) {
	orders := map[string][]string{
		"release then esc": {kittyLeftAltPress, kittyAltS, kittyLeftAltRelease, "\x1b"},
		"esc then release": {kittyLeftAltPress, kittyAltS, "\x1b", kittyLeftAltRelease},
	}
	for name, keys := range orders {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, holdModeConfig)
			term := startIn(t, base, startOpts{cols: 120, rows: 40})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "a pane to type into")
			enterTerminalMode(t, term)
			runInShell(t, term, "echo ready-$((1+1))", "ready-2", shellTimeout)
			art := artifactDir(t)
			for i, k := range keys {
				if err := term.SendKeys(tuitest.Key(k)); err != nil {
					t.Fatal(err)
				}
				// Each key is a separate event the client must see on its own.
				time.Sleep(300 * time.Millisecond)
				saveArtifact(t, term, art, "step-"+string(rune('1'+i)))
			}
			time.Sleep(insertGuard)
			runInShell(t, term, "echo back-$((3+4))", "back-7", shellTimeout)
		})
	}
}
