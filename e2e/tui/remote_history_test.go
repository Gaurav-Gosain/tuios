package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// A client that attaches gets the newest thousand lines of each pane's
// history in the snapshot, and the daemon keeps up to scrollback_lines. The
// older lines are read from the daemon by number when copy mode reaches them.
//
// How this could pass wrongly, written down first:
//   - The pane could hold fewer lines than a snapshot carries, so the client
//     holds them all and reads nothing from the daemon. The pane prints 3000
//     lines, and a snapshot carries 1000.
//   - The top of the history could be the oldest line the client holds and
//     still look right. The top row must be HIST1, the first line printed.
//   - The fetched lines could be the wrong ones: off by a page, or the
//     client's own lines again. The rows under HIST1 must be HIST2 to HIST5.
//   - The fallback could show blank rows for lines it cannot read, or hang.
//     With the feature off, the top must be a line the snapshot carried, and
//     no line older than it.
//
// Negative control: without the client reading from the daemon, the top row
// is HIST2001 or later.
func TestCopyModeReadsOldHistoryFromTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		name   string
		knobs  []string
		remote bool
	}{
		{name: "remote", remote: true},
		{name: "fallback", knobs: []string{"remote_history=off"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.knobs) > 0 {
				setKnobs(t, tc.knobs...)
			}
			base := t.TempDir()
			writeConfig(t, base, copyCursorConfig+"[appearance]\nscrollback_lines = 5000\n")
			killDaemon(t, base)
			const sess = "remhist"
			if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
				t.Fatalf("create the session: %v: %s", err, out)
			}
			if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "0", "printf 'HIST%d\\n' $(seq 1 3000)\n"); err != nil {
				t.Fatalf("send-text: %v: %s", err, out)
			}
			if out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", "0", "--pattern", `HIST3000\b`, "--timeout", "20000"); err != nil {
				t.Fatalf("the pane never printed its last line: %v: %s", err, out)
			}

			opts := copyColorOpts
			opts.args = []string{"attach", sess}
			term := startIn(t, base, opts)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			enterCopyModeHere(t, term, "copy mode on the attached pane")
			sendKeys(t, term, "g", "g")

			if tc.remote {
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return hasHistRun(s.Text(), 1, 5)
				}, uiTimeout); err != nil {
					t.Fatalf("ASSERTION: the top of the history is not HIST1 to HIST5\n%s", term.Snapshot())
				}
				return
			}
			// The fallback: the oldest line the snapshot carried is on top,
			// and nothing older is shown.
			var top int
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				top = firstHist(s.Text())
				return top > 0
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: no history line at the top in the fallback\n%s", term.Snapshot())
			}
			if top <= 2000 {
				t.Fatalf("ASSERTION: the fallback shows HIST%d, older than any line the snapshot carried\n%s", top, term.Snapshot())
			}
			if !hasHistRun(term.Snapshot(), top, top+4) {
				t.Fatalf("ASSERTION: the rows under HIST%d are not the lines after it\n%s", top, term.Snapshot())
			}
		})
	}
}

// hasHistRun reports whether text holds the lines HISTfrom to HISTto, one
// under the other.
func hasHistRun(text string, from, to int) bool {
	var want []string
	for i := from; i <= to; i++ {
		want = append(want, fmt.Sprintf("HIST%d", i))
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		ok := true
		for k, w := range want {
			if i+k >= len(lines) || !histOn(lines[i+k], w) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// histOn reports whether a screen line shows the pane line w whole.
func histOn(line, w string) bool {
	for _, f := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '│' || r == '|' }) {
		if f == w {
			return true
		}
	}
	return false
}

// firstHist is the number of the first HIST line on screen, or 0.
func firstHist(text string) int {
	for _, line := range strings.Split(text, "\n") {
		for _, f := range strings.Fields(line) {
			var n int
			if _, err := fmt.Sscanf(strings.Trim(f, "│|"), "HIST%d", &n); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}
