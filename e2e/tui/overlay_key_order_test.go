package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTheTopOverlayTakesTheKey opens help, then the command palette over it
// from the CLI, and types. The palette is on top, so it takes the letters,
// in terminal mode and in window mode alike. Before, terminal mode gave every
// key to help first, so typing went to the panel underneath.
func TestTheTopOverlayTakesTheKey(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(map[bool]string{true: "terminal", false: "window"}[terminal], func(t *testing.T) {
			term, base := start(t, startOpts{cols: 140, rows: 44, args: []string{"new", "e2e-overlays"}})
			t.Cleanup(func() { killDaemon(t, base) })
			waitBoot(t, term)
			newWindow(t, term)
			if terminal {
				enterTerminalMode(t, term)
				if err := term.SendKeys(tuitest.Ctrl('b'), "?"); err != nil {
					t.Fatal(err)
				}
				if err := term.WaitForText(helpTitle, uiTimeout); err != nil {
					t.Fatalf("leader ? did not open help: %v\n%s", err, term.Snapshot())
				}
			} else {
				openHelp(t, term)
			}
			if out, err := tuiosCLI(t, base, "run-command", "CommandPalette"); err != nil {
				t.Fatalf("run-command CommandPalette: %v\n%s", err, out)
			}
			if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
				t.Fatalf("the palette did not open over help: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys("q", "z", "w", "x"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(s.Text(), "qzwx")
			}, uiTimeout); err != nil {
				t.Fatalf("the palette on top did not get the typed letters: %v\n%s", err, term.Snapshot())
			}
			alive(t, term, "after typing into the top overlay")
		})
	}
}
