package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A copy keeps the whitespace the program printed (#516). Leading spaces,
// interior runs, the columns a tab moved over, the indent of every line of a
// multi-line copy, and the spaces at the edge of a soft-wrapped line all reach
// the clipboard exactly. Each case reads the OSC 52 write off the wire and
// compares it untrimmed.
//
// How these could pass wrongly, written down first:
//   - The harness helper clipboardSince trims each write, which is the very
//     fault under test. These cases read clipboardWrites directly.
//   - A stale write could match. Each case counts the writes before its
//     gesture and reads only the one after.
//   - A search could land on the typed command rather than its output. The
//     commands spell every marker with a gap, so only the output holds it.
//   - The wrapped line could happen to wrap only between words. Its words are
//     three columns apart and it spans more than three rows, so at least one
//     row ends on a space whatever the pane width is.
//   - The trailing blanks of a row could leak in. Every want ends on printed
//     text, and the single-line want would gain the row's padding.

// whitespaceLines are the three indented lines the line cases copy. printf
// prints them, with a tab in front of the middle one.
const whitespaceCmd = `printf '    LEAD%sone  two   x\n\tLEAD%stab\n  LEAD%sthree\n' - - -`

var whitespaceLines = []string{
	"    LEAD-one  two   x",
	"        LEAD-tab",
	"  LEAD-three",
}

// rawWrite waits for exactly one clipboard write after the first from, and
// returns it untrimmed.
func rawWrite(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, from int, what string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for len(clipboardWrites(out)) <= from {
		if time.Now().After(deadline) {
			t.Fatalf("%s: nothing reached the clipboard\n%s", what, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(clipboardSettle)
	writes := clipboardWrites(out)[from:]
	if len(writes) != 1 {
		t.Fatalf("%s: %d clipboard writes, want 1: %q", what, len(writes), writes)
	}
	return writes[0]
}

// sendEach sends keys one at a time, with a beat between, as a person types.
func sendEach(t *testing.T, term *tuitest.Terminal, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %q: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// copySearch enters copy mode and searches backwards for needle, then waits
// for the copy cursor to reach row.
func copySearch(t *testing.T, term *tuitest.Terminal, needle string, row int) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	if err := term.SendKeys("?"+needle, tuitest.Enter); err != nil {
		t.Fatalf("search %q: %v", needle, err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r == row
	}, uiTimeout); err != nil {
		t.Fatalf("the search for %q did not land on row %d\n%s", needle, row, term.Snapshot())
	}
}

// startWhitespaceClient starts tuios with one pane in terminal mode, alone or
// attached to a daemon, with its host output captured.
func startWhitespaceClient(t *testing.T, daemon bool) (*tuitest.Terminal, *lockedBuffer) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	out := &lockedBuffer{}
	opts := startOpts{out: out, env: copyColorOpts.env}
	if !daemon {
		term := startIn(t, base, opts)
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)
		return term, out
	}
	killDaemon(t, base)
	if o, err := tuiosCLI(t, base, "new", "e2e-space", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, o)
	}
	opts.args = []string{"attach", "e2e-space"}
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	return term, out
}

// TestCopyKeepsPrintedWhitespace copies indented lines with the copy-mode keys
// and with the mouse, and requires the clipboard to hold them exactly.
func TestCopyKeepsPrintedWhitespace(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, out := startWhitespaceClient(t, daemon)
			runInShell(t, term, "clear; "+whitespaceCmd, "LEAD-three", shellTimeout)
			time.Sleep(300 * time.Millisecond)
			s := term.Screen()
			one := lastRowWith(s, "LEAD-one")
			three := lastRowWith(s, "LEAD-three")

			// V on one line: the whole line, indent first.
			copySearch(t, term, "LEAD-one", one)
			from := len(clipboardWrites(out))
			sendEach(t, term, "V", "y")
			if got, want := rawWrite(t, term, out, from, "V y"), whitespaceLines[0]; got != want {
				t.Fatalf("V y copied %q, want %q", got, want)
			}

			// v from the start of the line to its end.
			copySearch(t, term, "LEAD-one", one)
			from = len(clipboardWrites(out))
			sendEach(t, term, "0", "v", "$", "y")
			if got, want := rawWrite(t, term, out, from, "0 v $ y"), whitespaceLines[0]; got != want {
				t.Fatalf("0 v $ y copied %q, want %q", got, want)
			}

			// V over three lines: every line keeps its own indent.
			copySearch(t, term, "LEAD-one", one)
			from = len(clipboardWrites(out))
			sendEach(t, term, "V", "j", "j", "y")
			if got, want := rawWrite(t, term, out, from, "V j j y"), strings.Join(whitespaceLines, "\n"); got != want {
				t.Fatalf("V j j y copied %q, want %q", got, want)
			}
			sendEach(t, term, "q")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				_, _, ok := copyCursorCell(s)
				return !ok
			}, uiTimeout); err != nil {
				t.Fatalf("q did not leave copy mode\n%s", term.Snapshot())
			}

			// The mouse: a triple click takes the line, and a drag from the
			// pane's first column to the last letter takes all three.
			_, colOne := findText(t, term, "LEAD-one")
			left := colOne - 4
			time.Sleep(gestureGap)
			from = len(clipboardWrites(out))
			clickAt(t, term, colOne+2, one, 3)
			if got, want := rawWrite(t, term, out, from, "triple click"), whitespaceLines[0]; got != want {
				t.Fatalf("a triple click copied %q, want %q", got, want)
			}
			time.Sleep(gestureGap)
			from = len(clipboardWrites(out))
			mouseDrag(t, term, left, one, left+len(whitespaceLines[2])-1, three, tuitest.MouseLeft, 0)
			if got, want := rawWrite(t, term, out, from, "drag"), strings.Join(whitespaceLines, "\n"); got != want {
				t.Fatalf("a drag over three lines copied %q, want %q", got, want)
			}
			alive(t, term, "after the whitespace copies")
		})
	}
}

// TestCopyKeepsSpacesAtASoftWrap copies one long line that the pane wraps over
// several rows. The copy is the line as printed: one line, its indent kept,
// and the spaces that fell on a row's last column kept.
func TestCopyKeepsSpacesAtASoftWrap(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, out := startWhitespaceClient(t, daemon)
			const words = 200
			want := "   WRAP-S0" + strings.Repeat(" ab", words) + " WRAP-E9"
			cmd := fmt.Sprintf(`clear; printf '   WRAP%%sS0%%s WRAP%%sE9\n' - "$(printf ' ab%%.0s' $(seq %d))" -`, words)
			runInShell(t, term, cmd, "WRAP-E9", shellTimeout)
			time.Sleep(300 * time.Millisecond)
			s := term.Screen()
			first := lastRowWith(s, "WRAP-S0")
			last := lastRowWith(s, "WRAP-E9")
			if last-first < 3 {
				t.Fatalf("the line spans rows %d to %d, want more than three rows\n%s", first, last, term.Snapshot())
			}

			copySearch(t, term, "WRAP-S0", first)
			from := len(clipboardWrites(out))
			keys := []string{"V"}
			for range last - first {
				keys = append(keys, "j")
			}
			sendEach(t, term, append(keys, "y")...)
			if got := rawWrite(t, term, out, from, "V over the wrapped line"); got != want {
				t.Fatalf("the wrapped line copied as\n%q\nwant\n%q", got, want)
			}
			alive(t, term, "after the wrapped copy")
		})
	}
}
