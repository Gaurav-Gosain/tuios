package tuie2e

import (
	"fmt"
	"testing"
	"time"
)

// TestDoubleClickIntervalIsAnOption: mouse.double_click_ms sets how long a
// second click may follow the first and still select the word. Two clicks 600
// ms apart are two single clicks at the default 300 ms, and a double click at
// 1000 ms, so the same gesture copies nothing in one run and the word in the
// other.
//
// Negative control: reading the fixed 300 ms again makes the 1000 ms run copy
// nothing and fail.
func TestDoubleClickIntervalIsAnOption(t *testing.T) {
	const word = "/opt/dblms/word.txt"
	for _, tc := range []struct {
		name, config string
		copies       bool
	}{
		{"default", "", false},
		{"1000 ms", "[mouse]\ndouble_click_ms = 1000\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			out := &lockedBuffer{}
			term := startIn(t, base, startOpts{out: out})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, `P=/opt; echo "$P/dblms/word.txt"`, word, shellTimeout)
			row, col := findText(t, term, word)

			clickAt(t, term, col+6, row, 1)
			// The gap is past 300 ms and inside 1000 ms.
			pause(t, "the gap between two clicks, past 300 ms and inside 1000 ms", 600*time.Millisecond)
			clickAt(t, term, col+6, row, 1)

			if tc.copies {
				waitClipboardSequence(t, term, out, 0, word)
				return
			}
			// The copy is deferred past the interval, so a copy that was going to
			// happen has happened well inside this.
			stayTrue(t, "no copy for two clicks 600 ms apart at the default interval", 1500*time.Millisecond, func() (bool, string) {
				got := clipboardSince(out, 0)
				return len(got) == 0, fmt.Sprintf("copied %q", got)
			})
		})
	}
}
