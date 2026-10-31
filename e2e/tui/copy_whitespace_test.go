package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
// returns it untrimmed. The copy-mode tests share it.
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
func sendEach(t *testing.T, term *tuitest.Terminal, keys ...any) {
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
			keys := []any{"V"}
			for range last - first {
				keys = append(keys, "j")
			}
			sendEach(t, term, append(keys, "y")...)
			if got := rawWrite(t, term, out, from, "V over the wrapped line"); got != want {
				t.Fatalf("the wrapped line copied as\n%q\nwant\n%q", got, want)
			}

			// A wide character that does not fit in the last column wraps
			// early and leaves that column as padding. The pad is not text.
			sendEach(t, term, "q")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				_, _, ok := copyCursorCell(s)
				return !ok
			}, uiTimeout); err != nil {
				t.Fatalf("q did not leave copy mode\n%s", term.Snapshot())
			}
			wideCmd := `clear; c=$(stty size | cut -d' ' -f2); echo "WCOLS=$c="; printf 'WIDE%sS' -; ` +
				`printf 'a%.0s' $(seq $((c-7))); printf '\344\270\255b WIDE%sE\n' -`
			runInShell(t, term, wideCmd, "WIDE-E", shellTimeout)
			time.Sleep(300 * time.Millisecond)
			s = term.Screen()
			m := regexp.MustCompile(`WCOLS=(\d+)=`).FindStringSubmatch(s.Text())
			if m == nil {
				t.Fatalf("the shell did not print the pane width\n%s", term.Snapshot())
			}
			cols, _ := strconv.Atoi(m[1])
			wideWant := "WIDE-S" + strings.Repeat("a", cols-7) + "\u4e2db WIDE-E"
			wideFirst := lastRowWith(s, "WIDE-S")
			if wideLast := lastRowWith(s, "WIDE-E"); wideLast != wideFirst+1 {
				t.Fatalf("the wide line spans rows %d to %d, want two rows\n%s", wideFirst, wideLast, term.Snapshot())
			}
			copySearch(t, term, "WIDE-S", wideFirst)
			from = len(clipboardWrites(out))
			sendEach(t, term, "V", "j", "y")
			if got := rawWrite(t, term, out, from, "V over the wide wrap"); got != wideWant {
				t.Fatalf("the line wrapped before a wide character copied as\n%q\nwant\n%q", got, wideWant)
			}
			alive(t, term, "after the wrapped copy")
		})
	}
}

// blockCmd prints the rows the block cases select. The commands spell every
// marker with a gap, so only the output holds it.
const blockCmd = `clear; printf 'BLOCK%sA one two\nBLOCK%sB \344\270\255\346\226\207 x\nBLOCK%sC\nBLOCK%sD     far\n' - - - -`

// blockSelectionConfig gives the selection a ground no other cell has, so the
// shape on the screen can be read off the cells.
var blockSelectionConfig = strings.Replace(copyPipeConfig, "[appearance.selection]\n", "[appearance.selection]\nbg = \"#00ff00\"\n", 1)

// TestCopyBlockSelection is ctrl+v in copy mode: the selection draws as a
// rectangle, and each way of copying it takes one line per row.
//
// How these could pass wrongly, written down first:
//   - The block could be a character selection that happens to give the same
//     text. Each want has a row where the two differ: a character selection
//     holds everything between the ends, whole lines in the middle.
//   - The drawing could be right only where text is. The short row is the row the
//     rectangle check needs, since the block runs past its end.
//   - A trailing blank could leak in, or be lost. The short row must have none,
//     and the row with text past the block must keep its blanks.
//   - A wide character could be cut. The block's edges fall on it from both sides.
//
// Negative controls: with extractVisualText ignoring the block, the first yank
// holds whole rows. With the render path stopping at each row's text, the
// rectangle check fails on the short row. With BlockRowSpan not widening, the
// wide character check fails. See NEGATIVE_CONTROLS.md.
func TestCopyBlockSelection(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, blockSelectionConfig)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, out: out, env: copyColorOpts.env, args: []string{"new", "block"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, blockCmd, "BLOCK-D", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	top := lastRowWith(term.Screen(), "BLOCK-A")
	line := term.Screen().Line(top)
	at := strings.Index(line, "BLOCK-A")
	if at < 0 {
		t.Fatalf("no first row on screen\n%s", term.Snapshot())
	}
	// A column, not a byte offset: the pane's border comes first.
	marker := utf8.RuneCountInString(line[:at])
	selected := func(s tuitest.Screen, col, row int) bool {
		bg := s.Cell(col, row).Bg
		return bg.Kind == tuitest.ColorRGB && bg.R == 0 && bg.G == 0xff && bg.B == 0
	}
	// startBlock goes to the marker of the first row, moves right to column
	// left, and starts a block.
	startBlock := func(left int) {
		t.Helper()
		copySearch(t, term, "BLOCK-A", top)
		keys := make([]any, 0, left+1)
		for range left {
			keys = append(keys, "l")
		}
		sendEach(t, term, append(keys, tuitest.Ctrl('v'))...)
		if err := term.WaitForText("o corner", uiTimeout); err != nil {
			t.Fatalf("ctrl+v did not start a block: %v\n%s", err, term.Snapshot())
		}
	}
	leave := func() {
		t.Helper()
		sendEach(t, term, "q")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			_, _, ok := copyCursorCell(s)
			return !ok
		}, uiTimeout); err != nil {
			t.Fatalf("q did not leave copy mode\n%s", term.Snapshot())
		}
	}

	// Columns 6 to 9 of four rows. The text is what tmux takes: the short row
	// stops at its text, the fourth keeps the blanks before its text, and the
	// wide character that starts on column 8 is whole.
	startBlock(6)
	sendEach(t, term, "l", "l", "l", "j", "j", "j")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for row := top; row <= top+3; row++ {
			for col := 6; col <= 9; col++ {
				// The cursor's own cell has the cursor ground, not the selection's,
				// and the second column of the wide character is read at its first.
				if (row == top+3 || row == top+1) && col == 9 {
					continue
				}
				if !selected(s, marker+col, row) {
					return false
				}
			}
			if selected(s, marker+5, row) || selected(s, marker+10, row) {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("the selection is not a rectangle: %v\n%s", err, term.SnapshotStyled())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "block")
	savePNG(t, term.Screen(), hostPalette(t, ""), dir, "block")
	from := len(clipboardWrites(out))
	sendEach(t, term, "y")
	if got, want := rawWrite(t, term, out, from, "block y"), "A on\nB 中\nC\nD   "; got != want {
		t.Fatalf("the block copied %q, want %q", got, want)
	}
	leave()

	// o goes to the opposite corner, and the next motion moves that corner.
	startBlock(6)
	sendEach(t, term, "l", "l", "l", "j", "j", "j", "o", "l")
	from = len(clipboardWrites(out))
	sendEach(t, term, "y")
	if got, want := rawWrite(t, term, out, from, "block o l y"), " on\n 中\n\n   "; got != want {
		t.Fatalf("after o and l the block copied %q, want %q", got, want)
	}
	leave()

	// The left edge falls on the second column of the wide character on the
	// second row, and the right edge on the first column of the next one. Both
	// come whole, and the selection draws over both.
	startBlock(9)
	sendEach(t, term, "l", "j")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return selected(s, marker+8, top+1)
	}, uiTimeout); err != nil {
		t.Fatalf("the selection does not cover a wide character on its edge: %v\n%s", err, term.SnapshotStyled())
	}
	from = len(clipboardWrites(out))
	sendEach(t, term, "y")
	if got, want := rawWrite(t, term, out, from, "block over wide"), "ne\n中文"; got != want {
		t.Fatalf("the block over the wide characters copied %q, want %q", got, want)
	}
	leave()

	// A copy pipe gets the same rows, and tr shows the newlines were there.
	startBlock(0)
	sendEach(t, term, "l", "l", "l", "l", "j", "j", "j")
	from = len(clipboardWrites(out))
	sendEach(t, term, "p")
	if got, want := pipeWrite(t, term, out, from, "block p"), "BLOCK BLOCK BLOCK BLOCK"; got != want {
		t.Fatalf("the copy pipe got %q, want %q", got, want)
	}
	leave()

	// A wrapped line gives one line per row on the screen, unlike v and V.
	runInShell(t, term, `clear; printf 'WRAP%sS%s\n' - "$(printf 'x%.0s' $(seq 300))"`, "xxxx", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	first := lastRowWith(term.Screen(), "WRAP-S")
	copySearch(t, term, "WRAP-S", first)
	sendEach(t, term, tuitest.Ctrl('v'), "l", "l", "j")
	from = len(clipboardWrites(out))
	sendEach(t, term, "y")
	if got, want := rawWrite(t, term, out, from, "block over a wrap"), "WRA\nxxx"; got != want {
		t.Fatalf("the block over a wrapped line copied %q, want %q", got, want)
	}
	alive(t, term, "after the block selections")
}
