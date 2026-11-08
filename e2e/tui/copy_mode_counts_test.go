package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Counts in copy mode (M5 in the state machine audit). A count had no bound,
// so 9999999999w ran ten billion word motions on the client's Update
// goroutine and froze it. A count before f, F, t or T was dropped when the
// key armed the search, so 3fx went to the first x. A pending g lasted until
// the next g, so g j g went to the top. And a count typed and left waited for
// its key for ever.
//
// How these could pass wrongly, written down first:
//   - The cursor could already sit where a key should take it. Each step
//     starts from column 0 of the counts line, with the x characters at
//     columns that differ for each count.
//   - A frozen client could pass a check that reads the screen it drew
//     before. After the huge count, the test waits for a move that only a
//     client still taking keys can make.
//   - 3fx could pass by going to the last x. The line has five, and 3fx must
//     stop on the third.
//   - A count that does nothing could pass "fewer matches than the count". 9fx
//     must leave the cursor where it was, and 2fx right after must move.
//   - g j g could pass when g does nothing at all. gg right after must reach
//     the top.
//   - The stale count could pass when every count is dropped. A count of 3
//     typed at once still moves three lines.
func TestCopyModeCounts(t *testing.T) {
	term, _ := startWhitespaceClient(t, false)
	runInShell(t, term, "clear; printf 'COUN''TS ax bx cx dx ex\\nL1\\nL2\\nL3\\nL4\\nL5\\n'; echo COUNTS-''DONE",
		"COUNTS-DONE", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	const line = "COUNTS ax bx cx dx ex"
	row := lastRowWith(term.Screen(), line)
	if row < 0 {
		t.Fatalf("the counts line is not on the screen\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")

	// at waits for the copy cursor on row and col.
	at := func(r, c int, what string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			gr, gc, ok := copyCursorCell(s)
			return ok && gr == r && gc == c
		}, uiTimeout); err != nil {
			gr, gc, _ := copyCursorCell(term.Screen())
			t.Fatalf("%s: the copy cursor is on row %d column %d, want row %d column %d\n%s",
				what, gr, gc, r, c, term.Snapshot())
		}
	}
	// stays checks the cursor is still on row and col after a moment.
	stays := func(r, c int, what string) {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		if gr, gc, ok := copyCursorCell(term.Screen()); !ok || gr != r || gc != c {
			t.Fatalf("%s: the copy cursor moved to row %d column %d, want it on row %d column %d\n%s",
				what, gr, gc, r, c, term.Snapshot())
		}
	}
	// home goes back to column 0 of the counts line, from the bottom, so
	// the view is the live screen again.
	home := func() {
		t.Helper()
		sendEach(t, term, "G")
		if err := term.SendKeys("?COUNTS ax", tuitest.Enter); err != nil {
			t.Fatalf("search: %v", err)
		}
		at(row, screenCol(t, term, row, line, 0), "search for the counts line")
		sendEach(t, term, "0")
		at(row, screenCol(t, term, row, line, 0), "0")
	}
	col := func(needle string, off int) int { return screenCol(t, term, row, needle, off) }

	// A huge count is capped, and the motion stops at the end of the buffer:
	// the client takes the next key at once.
	home()
	sendEach(t, term, "9", "9", "9", "9", "9", "9", "9", "9", "9", "9", "9", "9", "w")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r > row
	}, uiTimeout); err != nil {
		t.Fatalf("a count of twelve digits then w: the copy cursor never left the counts line; the client takes no keys\n%s", term.Snapshot())
	}
	home()
	alive(t, term, "after a count of twelve digits")

	// 3fx goes to the third x.
	sendEach(t, term, "3", "f", "x")
	at(row, col("cx", 1), "3fx")

	// 2tx stops before the second x.
	home()
	sendEach(t, term, "2", "t", "x")
	at(row, col("bx", 0), "2tx")

	// 9fx, with five x on the line and none after it, does not move; 2fx
	// then does.
	home()
	start := col(line, 0)
	sendEach(t, term, "9", "f", "x")
	stays(row, start, "9fx with five matches")
	sendEach(t, term, "2", "f", "x")
	at(row, col("bx", 1), "2fx after 9fx")

	// g j g is j, not gg. gg then goes to the top.
	home()
	sendEach(t, term, "g", "j", "g")
	at(row+1, start, "g j g")
	stays(row+1, start, "g j g")
	sendEach(t, term, "g", "g")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r <= row
	}, uiTimeout); err != nil {
		t.Fatalf("gg did not go up to the counts line or above it\n%s", term.Snapshot())
	}

	// A count typed at once moves three lines; one left for longer than the
	// timeout is dropped, and the key moves one line.
	home()
	sendEach(t, term, "3", "j")
	at(row+3, start, "3j")
	home()
	sendEach(t, term, "3")
	time.Sleep(5500 * time.Millisecond)
	sendEach(t, term, "j")
	at(row+1, start, "3, a pause, then j")
	stays(row+1, start, "3, a pause, then j")

	if !strings.Contains(term.Screen().Line(row), line) {
		t.Fatalf("the counts line left row %d\n%s", row, term.Snapshot())
	}
	alive(t, term, "after the count checks")

	path := filepath.Join(artifactDir(t), "copy-mode-counts.txt")
	if err := os.WriteFile(path, []byte(term.Snapshot()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("the last screen is in %s", path)
}
