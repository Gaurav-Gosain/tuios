package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCopySearchMatchLimitIsAnOption: appearance.copy_search_max_matches is how
// many matches a copy mode search keeps. A pane holding 150 lines of the same
// word reports its full count at the default limit of 1000, and "100+ matches"
// with the limit at 100.
//
// Negative control: reading the fixed 1000 again reports the full count in the
// limited run and fails it.
func TestCopySearchMatchLimitIsAnOption(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		capped       bool
	}{
		{"default", "", false},
		{"limit 100", "[appearance]\ncopy_search_max_matches = 100\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			term := startIn(t, base, startOpts{})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, `i=0; while [ $i -lt 150 ]; do echo CAPHIT; i=$((i+1)); done; echo CAPDONE-$((1+1))`, "CAPDONE-2", shellTimeout)
			sendKeys(t, term, tuitest.Ctrl('b'), "[")
			if err := term.WaitForText("hjkl move", uiTimeout); err != nil {
				t.Fatalf("copy mode never opened: %v\n%s", err, term.Snapshot())
			}
			sendKeys(t, term, "/")
			sendKeys(t, term, "C", "A", "P", "H", "I", "T")
			sendKeys(t, term, tuitest.Enter)
			// The count reads "(N matches)", or "(100+ matches)" when the search
			// stopped at its limit. The command line itself holds the word once
			// more, so the uncapped count is 151, not 150.
			if err := term.WaitFor(func(sc tuitest.Screen) bool {
				text := sc.Text()
				if tc.capped {
					return strings.Contains(text, "(100+ matches)")
				}
				return strings.Contains(text, " matches)") && !strings.Contains(text, "+ matches)")
			}, uiTimeout); err != nil {
				t.Fatalf("the search count was not as wanted (capped = %v): %v\n%s", tc.capped, err, term.Snapshot())
			}
		})
	}
}
