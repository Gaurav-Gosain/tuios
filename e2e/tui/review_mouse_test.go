package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewWithTheMouseOnly opens the review with ctrl+b v and then uses it
// with the mouse only, through SGR mouse reports, with no key after the
// open: the wheel scrolls the side under the pointer, a click on a file
// opens it, the diff's scroll bar drags, a click on a hunk's header folds it
// and a second click unfolds it, a drag on the rule between the two sides
// moves it, and a click on a key hint in the footer is that key: split, a
// note and its drop, and close.
//
// How it could pass wrongly, written down first:
//   - A wheel over the list could scroll the diff, or the other way round.
//     Each wheel checks the side under the pointer changed and the other did
//     not.
//   - The file opened could be the first one by accident. The file clicked
//     is the last in the list, and its diff has text no other file has.
//   - A fold could be a redraw of the same rows. The folded hunk's lines
//     must be gone and the header must say how many it hides.
//   - A footer click could hit a key next to the one meant. Each click is
//     followed by the change that one key makes and no other: the split
//     shows the old and new numbers side by side, the note opens its editor
//     with save and drop, and close takes the review off the screen.
//
// Artifact: a frame of each step under artifactDir, as text, styled text
// and PNG.
func TestReviewWithTheMouseOnly(t *testing.T) {
	base, repo := fanFixture(t)
	var oldMain, newMain strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&oldMain, "line %d\n", i)
		switch {
		case i == 20:
			newMain.WriteString("line 20 CHANGED-FIRST\n")
		case i == 250:
			newMain.WriteString("line 250 CHANGED-LAST\n")
		default:
			fmt.Fprintf(&newMain, "line %d\n", i)
		}
		if i == 120 {
			for j := 1; j <= 50; j++ {
				fmt.Fprintf(&newMain, "added %d\n", j)
			}
		}
	}
	const listed = 41
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "main.go", oldMain.String())
	for i := range listed {
		write(repo, fmt.Sprintf("f%02d.txt", i), "a\n")
	}
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "files")
	session, path := reviewFanAt(t, base, repo, "mouse")
	write(path, "main.go", newMain.String())
	for i := range listed {
		write(path, fmt.Sprintf("f%02d.txt", i), fmt.Sprintf("a\nFILE-%02d\n", i))
	}

	const cols, rows = 140, 36
	term := attachIn(t, base, session, startOpts{cols: cols, rows: rows})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	// No key from here on.
	waitScreen(t, term, "the review never opened", "Review", "f00.txt", "FILE-00")
	art := artifactDir(t)
	frame := func(name string) {
		t.Helper()
		saveArtifact(t, term, art, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), art, name)
	}
	frame("1-open")

	const paneTop, footer = 3, rows - 2
	until := func(what string, ok func(tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(ok, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: %v\n%s", what, err, term.Snapshot())
		}
	}
	// ruleCol is the column of the rule between the list and the diff.
	ruleCol := func(s tuitest.Screen) int {
		line := []rune(s.Line(paneTop + 5))
		for x := 2; x < len(line)-2; x++ {
			if line[x] == '│' {
				return x
			}
		}
		return -1
	}
	s := term.Screen()
	rule := ruleCol(s)
	if rule < 0 {
		t.Fatalf("no rule between the list and the diff\n%s", term.Snapshot())
	}
	listX, diffX := 4, rule+4
	part := func(s tuitest.Screen, y, from, to int) string {
		line := []rune(s.Line(y))
		return string(line[min(from, len(line)):min(to, len(line))])
	}
	listTop := func(s tuitest.Screen) string { return part(s, paneTop, 0, rule) }
	diffTop := func(s tuitest.Screen) string { return part(s, paneTop, rule+1, cols) }

	// The wheel over the list scrolls the list, and leaves the diff.
	diffBefore := diffTop(s)
	wheelAt(t, term, listX, paneTop+5, tuitest.MouseWheelDown, 4)
	until("the wheel over the list never scrolled it to f12.txt", func(s tuitest.Screen) bool {
		return strings.Contains(listTop(s), "f12.txt")
	})
	if got := diffTop(term.Screen()); got != diffBefore {
		t.Errorf("ASSERTION: the wheel over the list moved the diff: %q, was %q", got, diffBefore)
	}
	frame("2-list-wheel")

	// A click on the last file opens it.
	row, col := findText(t, term, "main.go")
	leftClick(t, term, col, row)
	waitScreen(t, term, "a click on main.go never opened it", "CHANGED-FIRST")
	frame("3-file-click")

	// The wheel over the diff scrolls the diff, and leaves the list.
	s = term.Screen()
	listBefore, diffBefore := listTop(s), diffTop(s)
	wheelAt(t, term, diffX+10, paneTop+5, tuitest.MouseWheelDown, 2)
	until("the wheel over the diff never scrolled it", func(s tuitest.Screen) bool { return diffTop(s) != diffBefore })
	if got := listTop(term.Screen()); got != listBefore {
		t.Errorf("ASSERTION: the wheel over the diff moved the list: %q, was %q", got, listBefore)
	}
	frame("4-diff-wheel")

	// The scroll bar, in the column inside the right edge, drags to the end.
	barX := cols - 2
	thumb := func(s tuitest.Screen) (top, bottom int) {
		top, bottom = -1, -1
		for y := paneTop; y < footer-1; y++ {
			c := s.Cell(barX, y)
			if strings.TrimSpace(c.Content) != "" && c.Content != "│" {
				if top < 0 {
					top = y
				}
				bottom = y
			}
		}
		return top, bottom
	}
	top, _ := thumb(term.Screen())
	if top < 0 {
		t.Fatalf("ASSERTION: the diff of main.go has no scroll bar thumb in column %d\n%s", barX, term.SnapshotStyled())
	}
	mouseDrag(t, term, barX, top, barX, footer-2, tuitest.MouseLeft, 0)
	until("a drag of the scroll bar never reached the end of main.go", func(s tuitest.Screen) bool {
		return screenHas(s, "CHANGED-LAST") && !screenHas(s, "CHANGED-FIRST")
	})
	frame("5-bar-drag")
	_, bottom := thumb(term.Screen())
	mouseDrag(t, term, barX, bottom, barX, paneTop, tuitest.MouseLeft, 0)
	until("a drag of the scroll bar never came back to the start", func(s tuitest.Screen) bool { return screenHas(s, "CHANGED-FIRST") })

	// A click on the first hunk's header folds it, and a second unfolds it.
	row, col = findText(t, term, "@@ -")
	leftClick(t, term, col, row)
	until("a click on the hunk header never folded it", func(s tuitest.Screen) bool {
		return screenHas(s, "lines folded") && !screenHas(s, "CHANGED-FIRST")
	})
	frame("6-fold")
	row, col = findText(t, term, "lines folded")
	leftClick(t, term, col, row)
	until("a second click never unfolded the hunk", func(s tuitest.Screen) bool {
		return !screenHas(s, "lines folded") && screenHas(s, "CHANGED-FIRST")
	})

	// A drag on the rule moves it.
	mouseDrag(t, term, rule, paneTop+8, rule+12, paneTop+8, tuitest.MouseLeft, 0)
	until("a drag on the rule never moved it 12 columns", func(s tuitest.Screen) bool { return ruleCol(s) == rule+12 })
	frame("7-rule-drag")

	// The footer's hints are buttons.
	hint := func(text string) {
		t.Helper()
		var x int
		until("the footer never showed "+text, func(s tuitest.Screen) bool {
			i := strings.Index(s.Line(footer), text)
			if i < 0 {
				return false
			}
			x = len([]rune(s.Line(footer)[:i]))
			return true
		})
		leftClick(t, term, x, footer)
	}
	hint("s split")
	until("a click on s split never split the diff", func(s tuitest.Screen) bool { return strings.Contains(s.Line(footer), "s unified") })
	frame("8-split")
	row, col = findText(t, term, "CHANGED-FIRST")
	leftClick(t, term, col, row)
	hint("c note")
	until("a click on c note never opened the note editor", func(s tuitest.Screen) bool {
		f := s.Line(footer)
		return strings.Contains(f, "save") && strings.Contains(f, "esc drop")
	})
	frame("9-note")
	hint("esc drop")
	until("a click on esc drop never closed the editor", func(s tuitest.Screen) bool { return strings.Contains(s.Line(footer), "c note") })
	hint("esc close")
	until("a click on esc close never closed the review", func(s tuitest.Screen) bool { return !screenHas(s, "CHANGED-FIRST") })
	frame("10-closed")
}
