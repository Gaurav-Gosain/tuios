package tuie2e

import (
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

const homeEndLine = "HOME-start middle END-tail"

const homeEndCmd = `clear; printf 'HOME%sstart middle END%stail\n' - -`

// homeEndClient starts tuios with one pane in terminal mode and its host
// output captured.
func homeEndClient(t *testing.T) (*tuitest.Terminal, *lockedBuffer) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{out: out, env: copyColorOpts.env})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	return term, out
}

// homeEndWrite waits for exactly one clipboard write after the first from,
// and returns it untrimmed.
func homeEndWrite(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, from int, what string) string {
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

// homeEndPress sends keys one at a time, with a beat between.
func homeEndPress(t *testing.T, term *tuitest.Terminal, keys ...any) {
	t.Helper()
	for _, k := range keys {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %q: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

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
	b := strings.Index(line, "middle")
	if b < 0 {
		t.Fatalf("row %d does not hold the line: %q", row, line)
	}
	col := len([]rune(line[:b]))
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

func TestCopyModeHomeAndEnd(t *testing.T) {
	term, out := homeEndClient(t)
	runInShell(t, term, homeEndCmd, "END-tail", shellTimeout)
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
	homeEndPress(t, term, "0")
	zero := copyCursorMoved(t, term, row, mid, "0")
	mid = toMiddle(t, term, row)
	homeEndPress(t, term, "$")
	dollar := copyCursorMoved(t, term, row, mid, "$")

	// Home and End reach the same columns.
	mid = toMiddle(t, term, row)
	homeEndPress(t, term, tuitest.Home)
	if got := copyCursorMoved(t, term, row, mid, "Home"); got != zero {
		t.Fatalf("Home put the copy cursor on column %d, want column %d where 0 puts it", got, zero)
	}
	mid = toMiddle(t, term, row)
	homeEndPress(t, term, tuitest.End)
	if got := copyCursorMoved(t, term, row, mid, "End"); got != dollar {
		t.Fatalf("End put the copy cursor on column %d, want column %d where $ puts it", got, dollar)
	}

	// In a v selection, Home and End move its end.
	toMiddle(t, term, row)
	from := len(clipboardWrites(out))
	homeEndPress(t, term, "v", tuitest.Home, "y")
	if got, want := homeEndWrite(t, term, out, from, "v Home y"), "HOME-start m"; got != want {
		t.Fatalf("v Home y copied %q, want %q", got, want)
	}
	toMiddle(t, term, row)
	from = len(clipboardWrites(out))
	homeEndPress(t, term, "v", tuitest.End, "y")
	if got, want := homeEndWrite(t, term, out, from, "v End y"), "middle END-tail"; got != want {
		t.Fatalf("v End y copied %q, want %q", got, want)
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
	homeEndPress(t, term, "$")
	dollar := copyCursorMoved(t, term, row, mid, "$")

	// End is no longer bound: the cursor stays on the match.
	mid = toMiddle(t, term, row)
	homeEndPress(t, term, tuitest.End)
	time.Sleep(500 * time.Millisecond)
	if r, c, ok := copyCursorCell(term.Screen()); !ok || r != row || c != mid {
		t.Fatalf("End moved the copy cursor to row %d column %d after it was unbound, want it on column %d\n%s", r, c, mid, term.Snapshot())
	}
	// ctrl+e is.
	homeEndPress(t, term, tuitest.Ctrl('e'))
	if got := copyCursorMoved(t, term, row, mid, "ctrl+e"); got != dollar {
		t.Fatalf("ctrl+e put the copy cursor on column %d, want column %d where $ puts it", got, dollar)
	}
	alive(t, term, "after the rebound line end")
}
