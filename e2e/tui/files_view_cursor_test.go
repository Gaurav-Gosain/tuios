package tuie2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestFilesViewCursorInsideAFile goes inside a text file in the Files view
// and reads it the way a person does: a cell cursor and a quiet highlight of
// its line show where the reader is, j and l move the cursor, v starts copy
// mode at the cursor with a selection, e e takes two words, and y copies
// them. esc ends copy mode, and the cursor stays where it ended. A click on the
// text puts the cursor under the pointer. The header names the keys that
// select. The frames files-cursor-inside, files-cursor-select and
// files-cursor-click are the artifact.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md): on main, inside the file
// no cell is marked, and the test fails at "j did not show the cursor".
func TestFilesViewCursorInsideAFile(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "HOME", "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "first line of the file\nalpha beta gamma delta\nthird line here\n"
	if err := os.WriteFile(filepath.Join(dir, "words.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"new", "cursor"}, cols: 160, rows: 44, out: out,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && clear && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)

	sendKeys(t, term, tuitest.Ctrl('b'), "f")
	railLists(t, term, "Ctrl+B f did not open the view", []string{"this machine", "words.txt"}, nil)
	sendKeys(t, term, "/", "words", tuitest.Enter, " ")
	railLists(t, term, "space did not show words.txt", []string{"alpha beta gamma delta"}, nil)
	sendKeys(t, term, tuitest.Tab)
	railLists(t, term, "tab did not go inside the file", []string{"Inside the file"}, nil)

	// cell is the cell of the preview at the column of want's first byte
	// plus dx, on want's row.
	cell := func(s tuitest.Screen, want string, dx int) (tuitest.Cell, bool) {
		col, row, ok := findOnGrid(s, want)
		if !ok {
			return tuitest.Cell{}, false
		}
		return s.Cell(col+dx, row), true
	}
	// marked says the cursor is on the cell dx into want: its ground is not
	// the ground of the cell after it, which is on the highlighted line too,
	// and the line's ground is not the ground of the plain line.
	marked := func(s tuitest.Screen, want string, dx int, plain string) bool {
		at, ok1 := cell(s, want, dx)
		next, ok2 := cell(s, want, dx+1)
		other, ok3 := cell(s, plain, 1)
		return ok1 && ok2 && ok3 && at.Bg != next.Bg && next.Bg != other.Bg
	}
	waitFor := func(why string, fn func(tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(fn, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: %v\n%s", why, err, term.Snapshot())
		}
	}

	// j moves the cursor to the second line, and l six times to the b of
	// beta.
	sendKeys(t, term, "j")
	_ = term.WaitStable(uiTimeout)
	// The frame before the check: on main it shows the file with no cursor.
	saveCockpitFrames(t, term, "files-cursor-after-j")
	waitFor("j did not show the cursor on the second line", func(s tuitest.Screen) bool {
		return marked(s, "alpha beta", 0, "third line")
	})
	railLists(t, term, "ASSERTION: the header does not name the keys that select", []string{"v select", "[ copy mode"}, nil)
	sendEach(t, term, "l", "l", "l", "l", "l", "l")
	waitFor("l did not move the cursor to beta", func(s tuitest.Screen) bool {
		return marked(s, "alpha beta", 6, "third line")
	})
	saveCockpitFrames(t, term, "files-cursor-inside")

	// v starts copy mode at the cursor with a selection, e e takes beta
	// gamma, and y copies it.
	from := len(clipboardWrites(out))
	sendEach(t, term, "v", "e", "e")
	railLists(t, term, "ASSERTION: the header does not name the keys of the selection", []string{"y copy", "esc end the selection"}, []string{"v select"})
	saveCockpitFrames(t, term, "files-cursor-select")
	sendEach(t, term, "y")
	if got := rawWrite(t, term, out, from, "ASSERTION: v e e y at the cursor"); got != "beta gamma" {
		t.Fatalf("ASSERTION: v e e y at the cursor copied %q, want %q", got, "beta gamma")
	}
	// esc leaves copy mode: the preview's keys are back, and the cursor is
	// on the second line still.
	sendKeys(t, term, tuitest.Esc)
	waitFor("the cursor did not stay on the second line after esc", func(s tuitest.Screen) bool {
		_, _, hint := findOnGrid(s, "v select")
		at, ok := cell(s, "alpha beta", 0)
		plain, ok2 := cell(s, "third line", 0)
		return hint && ok && ok2 && at.Bg != plain.Bg
	})

	// A click on the text puts the cursor under the pointer.
	col, row := mustFind(t, term, "third line")
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	waitFor("a click did not put the cursor on the i of third", func(s tuitest.Screen) bool {
		return marked(s, "third line", 2, "first line")
	})
	saveCockpitFrames(t, term, "files-cursor-click")
	alive(t, term, "after the cursor inside a file")
}
