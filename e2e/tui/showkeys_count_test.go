package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestShowKeyCountIsAnOption: debug.show_key_count is how many recent
// keypresses the keycast overlay keeps. The same keys are typed in both runs;
// the overlay holds the last five at the default and the last two at 2.
//
// Negative control: keeping the fixed five makes the count-2 run show five and
// fail.
func TestShowKeyCountIsAnOption(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         int
	}{
		{"default", "", 5},
		{"2", "[debug]\nshow_key_count = 2\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			term := startIn(t, base, startOpts{args: []string{"--show-keys"}})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			for _, k := range []string{"A", "B", "C", "D"} {
				if err := term.SendKeys(k); err != nil {
					t.Fatalf("send %s: %v", k, err)
				}
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				row := showkeysRow(s)
				return strings.Count(row, pillLeft) == tc.want && strings.Contains(row, "D")
			}, uiTimeout); err != nil {
				t.Fatalf("the keycast never held %d keys ending in D: %v\n%s", tc.want, err, term.Snapshot())
			}
		})
	}
}
