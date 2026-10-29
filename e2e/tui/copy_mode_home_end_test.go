package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Home and End in copy mode (#515): Home goes to the start of the line as 0
// does, End to its end as $ does, in normal mode and in a v selection. The
// keys are the [keybindings.copy_mode] section's defaults, so a config can
// move them.
//
// How these could pass wrongly, written down first:
//   - The cursor could already be at the column the key should reach. Every
//     press starts from the middle of the line, at the search match.
//   - The column could be right for the wrong reason. The columns Home and End
//     reach are compared with the ones 0 and $ reach on the same line.
//   - A selection could look right on screen and copy something else. The v
//     cases read the clipboard write off the wire.
//   - The binding could be ignored and the keys fixed. The rebound case moves
//     End to ctrl+e and requires End to stop moving the cursor (the negative
//     half) and ctrl+e to move it (the positive half).
//
// $ and ^ stop on the line's text (#980): $ on the last nonblank character,
// not the blank padding out to the pane edge, and ^ on the first nonblank
// character of an indented line, not column zero as 0 does. The expected
// columns come from where the text sits in the screen line, so a $ that went
// to the pane edge, or a ^ that did what 0 does, lands elsewhere.

const homeEndLine = "HOME-start middle END-tail"

const homeEndCmd = `clear; printf 'HOME%sstart middle END%stail\n' - -`

// copyCursorMoved waits until the copy cursor is on row and its column is not
// from, and returns the column.
func copyCursorMoved(t *testing.T, term *tuitest.Terminal, row, from int, what string) int {
	t.Helper()
	var col int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, c, ok := copyCursorCell(s)
		col = c
		return ok && r == row && c != from
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the copy cursor did not move off column %d on row %d\n%s", what, from, row, term.Snapshot())
	}
	return col
}

// toMiddle searches for "middle", waits for the copy cursor to land on it,
// and returns its column.
func toMiddle(t *testing.T, term *tuitest.Terminal, row int) int {
	t.Helper()
	line := term.Screen().Line(row)
	before, _, ok := strings.Cut(line, "middle")
	if !ok {
		t.Fatalf("row %d does not hold the line: %q", row, line)
	}
	col := len([]rune(before))
	if err := term.SendKeys("?middle", tuitest.Enter); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, c, ok := copyCursorCell(s)
		return ok && r == row && c == col
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not land on row %d column %d\n%s", row, col, term.Snapshot())
	}
	return col
}

// indentLine is an indented line for ^, printed above homeEndLine.
const indentLine = "    INDENT-text rest"

// screenCol returns the column of the first cell of needle on row, plus off.
func screenCol(t *testing.T, term *tuitest.Terminal, row int, needle string, off int) int {
	t.Helper()
	line := term.Screen().Line(row)
	before, _, ok := strings.Cut(line, needle)
	if !ok {
		t.Fatalf("row %d does not hold %q: %q", row, needle, line)
	}
	return len([]rune(before)) + off
}

func TestCopyModeHomeAndEnd(t *testing.T) {
	term, out := startWhitespaceClient(t, false)
	runInShell(t, term, "clear; printf '%s\\n' '"+indentLine+"'; "+strings.TrimPrefix(homeEndCmd, "clear; "), "END-tail", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	row := lastRowWith(term.Screen(), homeEndLine)
	if row < 0 {
		t.Fatalf("the line is not on the screen\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")

	// The columns 0 and $ reach, each from the middle of the line.
	mid := toMiddle(t, term, row)
	sendEach(t, term, "0")
	zero := copyCursorMoved(t, term, row, mid, "0")
	mid = toMiddle(t, term, row)
	sendEach(t, term, "$")
	dollar := copyCursorMoved(t, term, row, mid, "$")
	if want := screenCol(t, term, row, homeEndLine, len(homeEndLine)-1); dollar != want {
		t.Fatalf("$ put the copy cursor on column %d, want column %d, the last l of END-tail", dollar, want)
	}

	// Home and End reach the same columns.
	mid = toMiddle(t, term, row)
	sendEach(t, term, tuitest.Home)
	if got := copyCursorMoved(t, term, row, mid, "Home"); got != zero {
		t.Fatalf("Home put the copy cursor on column %d, want column %d where 0 puts it", got, zero)
	}
	mid = toMiddle(t, term, row)
	sendEach(t, term, tuitest.End)
	if got := copyCursorMoved(t, term, row, mid, "End"); got != dollar {
		t.Fatalf("End put the copy cursor on column %d, want column %d where $ puts it", got, dollar)
	}

	// In a v selection, Home and End move its end.
	toMiddle(t, term, row)
	from := len(clipboardWrites(out))
	sendEach(t, term, "v", tuitest.Home, "y")
	if got, want := rawWrite(t, term, out, from, "v Home y"), "HOME-start m"; got != want {
		t.Fatalf("v Home y copied %q, want %q", got, want)
	}
	toMiddle(t, term, row)
	from = len(clipboardWrites(out))
	sendEach(t, term, "v", tuitest.End, "y")
	if got, want := rawWrite(t, term, out, from, "v End y"), "middle END-tail"; got != want {
		t.Fatalf("v End y copied %q, want %q", got, want)
	}

	// ^ on the indented line goes to its first nonblank character, from the
	// last word of the line.
	irow := lastRowWith(term.Screen(), indentLine)
	if irow < 0 {
		t.Fatalf("the indented line is not on the screen\n%s", term.Snapshot())
	}
	rest := screenCol(t, term, irow, "rest", 0)
	if err := term.SendKeys("?rest", tuitest.Enter); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, c, ok := copyCursorCell(s)
		return ok && r == irow && c == rest
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not land on row %d column %d\n%s", irow, rest, term.Snapshot())
	}
	sendEach(t, term, "^")
	if got, want := copyCursorMoved(t, term, irow, rest, "^"), screenCol(t, term, irow, "INDENT", 0); got != want {
		t.Fatalf("^ put the copy cursor on column %d, want column %d, the I of INDENT", got, want)
	}
	alive(t, term, "after Home and End in copy mode")
}

// TestCopyModeEndRebinds moves the line end action from End to ctrl+e.
func TestCopyModeEndRebinds(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig+"\n[keybindings.copy_mode]\ncopy_mode_line_end = [\"ctrl+e\"]\n")
	term := startIn(t, base, copyColorOpts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, homeEndCmd, "END-tail", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	row := lastRowWith(term.Screen(), homeEndLine)
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")

	mid := toMiddle(t, term, row)
	sendEach(t, term, "$")
	dollar := copyCursorMoved(t, term, row, mid, "$")

	// End is no longer bound: the cursor stays on the match.
	mid = toMiddle(t, term, row)
	sendEach(t, term, tuitest.End)
	time.Sleep(500 * time.Millisecond)
	if r, c, ok := copyCursorCell(term.Screen()); !ok || r != row || c != mid {
		t.Fatalf("End moved the copy cursor to row %d column %d after it was unbound, want it on column %d\n%s", r, c, mid, term.Snapshot())
	}
	// ctrl+e is.
	sendEach(t, term, tuitest.Ctrl('e'))
	if got := copyCursorMoved(t, term, row, mid, "ctrl+e"); got != dollar {
		t.Fatalf("ctrl+e put the copy cursor on column %d, want column %d where $ puts it", got, dollar)
	}
	alive(t, term, "after the rebound line end")
}

// o in a v or V selection moves the copy cursor to the other end (#1226), as
// other-end does in tmux and o in vim.
//
// How these could pass wrongly, written down first:
//   - o could do nothing and the copy still match. The issue's case, 0 v l l
//     o l, copies "bc" only when l moves the start. Without o it copies "abcd".
//   - o could move the cursor and change the selection with it. 0 v l l o y
//     must still copy "abc".
//   - o could swap once and then stick. o o l must move the far end again and
//     copy "abcd".
//   - The other end could be off screen and the cursor left there unseen. The
//     V case moves 45 lines down from a line at the top of the pane, so the
//     start scrolls out, and o must bring its row back with the cursor on it.
func TestCopyModeVisualOtherEnd(t *testing.T) {
	term, out := startWhitespaceClient(t, false)
	runInShell(t, term, `clear; seq -f 'ROW%02g' 1 60; printf 'abc%sdef\n' ''`, "abcdef", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	row := lastRowWith(term.Screen(), "abcdef")
	if row < 0 {
		t.Fatalf("the line is not on the screen\n%s", term.Snapshot())
	}

	// v: each case starts at the a of abcdef.
	toA := func() int {
		t.Helper()
		copySearch(t, term, "abcdef", row)
		sendEach(t, term, "0")
		r, c, ok := copyCursorCell(term.Screen())
		if !ok || r != row {
			t.Fatalf("0 did not keep the copy cursor on row %d\n%s", row, term.Snapshot())
		}
		return c
	}
	cases := []struct {
		keys []any
		want string
	}{
		{[]any{"v", "l", "l", "o", "l", "y"}, "bc"},
		{[]any{"v", "l", "l", "o", "y"}, "abc"},
		{[]any{"v", "l", "l", "o", "o", "l", "y"}, "abcd"},
	}
	for _, tc := range cases {
		toA()
		from := len(clipboardWrites(out))
		sendEach(t, term, tc.keys...)
		what := fmt.Sprint(tc.keys...)
		if got := rawWrite(t, term, out, from, what); got != tc.want {
			t.Fatalf("0 %v copied %q, want %q", tc.keys, got, tc.want)
		}
	}

	// o puts the cursor on the start, at the a.
	a := toA()
	sendEach(t, term, "v", "l", "l", "o")
	if r, c, ok := copyCursorCell(term.Screen()); !ok || r != row || c != a {
		t.Fatalf("0 v l l o put the copy cursor on row %d column %d, want row %d column %d\n%s", r, c, row, a, term.Snapshot())
	}
	copiesFrame(t, term, "other-end-v")
	sendEach(t, term, "q")

	// V: the start is off screen when o is pressed, and comes back.
	if err := term.SendKeys("?ROW05", tuitest.Enter); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && strings.Contains(s.Line(r), "ROW05")
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not land on ROW05\n%s", term.Snapshot())
	}
	sendEach(t, term, "V", "4", "5", "j")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && strings.Contains(s.Line(r), "ROW50") && lastRowWith(s, "ROW05") < 0
	}, uiTimeout); err != nil {
		t.Fatalf("V 45j did not reach ROW50 with ROW05 off screen\n%s", term.Snapshot())
	}
	copiesFrame(t, term, "other-end-V-before")
	sendEach(t, term, "o")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && strings.Contains(s.Line(r), "ROW05")
	}, uiTimeout); err != nil {
		t.Fatalf("o did not bring the copy cursor to ROW05 on screen\n%s", term.Snapshot())
	}
	copiesFrame(t, term, "other-end-V-after")
	from := len(clipboardWrites(out))
	sendEach(t, term, "j", "y")
	var want []string
	for i := 6; i <= 50; i++ {
		want = append(want, fmt.Sprintf("ROW%02d", i))
	}
	if got := rawWrite(t, term, out, from, "V 45j o j y"); got != strings.Join(want, "\n") {
		t.Fatalf("V 45j o j y copied %q, want ROW06 to ROW50", got)
	}
	alive(t, term, "after o in copy mode")
}
