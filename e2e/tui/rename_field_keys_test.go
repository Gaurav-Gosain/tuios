package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// renameWordKeys are the bytes terminals send for a word move, by terminal.
// xterm, kitty, WezTerm and Ghostty send CSI 1;3 D for option or alt plus
// left, and CSI 1;5 D for ctrl plus left. Terminal.app and iTerm2 with
// "Esc+" send ESC b and ESC f. iTerm2's older Meta mode sends ESC then the
// arrow. CSI 1;9 is super, Command on a Mac, which goes to the start and the
// end of the field as it does in a native one.
var renameWordKeys = []struct {
	name, left, right string
}{
	{"csi alt (xterm, kitty, WezTerm, Ghostty)", "\x1b[1;3D", "\x1b[1;3C"},
	{"csi ctrl", "\x1b[1;5D", "\x1b[1;5C"},
	{"esc b and esc f (Terminal.app, iTerm2)", "\x1bb", "\x1bf"},
	{"esc arrow (iTerm2 meta)", "\x1b\x1b[D", "\x1b\x1b[C"},
}

// TestRenameFieldWordKeys opens the window rename on "alpha beta gamma" and,
// for each terminal's spelling, moves a word left and types X, then a word
// right and types Y. The field must read "alpha beta Xgamma" and then
// "alpha beta XgammaY".
func TestRenameFieldWordKeys(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "rnword"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "--name", "alpha beta gamma"); err != nil {
		t.Fatalf("name: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Alt(tuitest.Esc))
	waitWindowMode(t, term)
	art := artifactDir(t)
	for _, k := range renameWordKeys {
		openRenameOn(t, term, "alpha beta gamma")
		if err := term.Type(k.left); err != nil {
			t.Fatal(err)
		}
		pause(t, "the field ignores a plain key for a moment after a modified one, and that moment has no end on screen", insertGuard)
		sendKeys(t, term, "X")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			f, ok := renameField(s)
			return ok && strings.Contains(f, "X")
		}, uiTimeout); err != nil {
			t.Fatalf("%s: X never typed: %v\n%s", k.name, err, term.Snapshot())
		}
		if f, _ := renameField(term.Screen()); f != "alpha beta Xgamma" {
			t.Errorf("ASSERTION: %s: a word left then X reads %q, want %q", k.name, f, "alpha beta Xgamma")
		}
		if err := term.Type(k.right); err != nil {
			t.Fatal(err)
		}
		pause(t, "the field ignores a plain key for a moment after a modified one, and that moment has no end on screen", insertGuard)
		sendKeys(t, term, "Y")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			f, ok := renameField(s)
			return ok && strings.Contains(f, "Y")
		}, uiTimeout); err != nil {
			t.Fatalf("%s: Y never typed: %v\n%s", k.name, err, term.Snapshot())
		}
		if f, _ := renameField(term.Screen()); f != "alpha beta XgammaY" {
			t.Errorf("ASSERTION: %s: a word right then Y reads %q, want %q", k.name, f, "alpha beta XgammaY")
		}
		saveArtifact(t, term, art, "word-"+strings.Fields(k.name)[0]+"-"+strings.Fields(k.name)[1])
		sendKeys(t, term, tuitest.Esc)
		if err := term.WaitFor(func(s tuitest.Screen) bool { _, ok := renameField(s); return !ok }, uiTimeout); err != nil {
			t.Fatalf("esc did not close the rename: %v\n%s", err, term.Snapshot())
		}
	}
	alive(t, term, "after word keys in the rename field")
}

// selectionCells reads the cells of want on the row that holds it: whether
// each is drawn on the same ground, and which of them, if any, is underlined.
func selectionCells(s tuitest.Screen, want string) (row, col int, ok bool) {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if i := strings.Index(line, want); i >= 0 {
			return r, len([]rune(line[:i])), true
		}
	}
	return 0, 0, false
}

// TestRenameFieldSelects opens the window rename on "alpha beta gamma". Five
// shift+left select "gamma": all five cells wear one ground, other than the
// field's, and the caret is the underlined first cell. Typing Z replaces the
// selection. Then home and five shift+right select "alpha" the other way: the
// five cells wear the same ground, and the caret is the underlined cell after
// them. shift+end then backspace empties the field.
func TestRenameFieldSelects(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "rnsel"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", sess, "--name", "alpha beta gamma"); err != nil {
		t.Fatalf("name: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", sess}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Alt(tuitest.Esc))
	waitWindowMode(t, term)
	art := artifactDir(t)
	openRenameOn(t, term, "alpha beta gamma")
	press := func(seq string, n int) {
		t.Helper()
		for range n {
			if err := term.Type(seq); err != nil {
				t.Fatal(err)
			}
		}
		pause(t, "the field ignores a plain key for a moment after a modified one, and that moment has no end on screen", insertGuard)
	}
	// check reads the five cells from col on the field's row.
	check := func(what, word string, caretAt int) {
		t.Helper()
		var s tuitest.Screen
		var row, col int
		if err := term.WaitFor(func(sc tuitest.Screen) bool {
			r, c, ok := selectionCells(sc, word)
			if !ok {
				return false
			}
			s, row, col = sc, r, c
			first := sc.Cell(c, r)
			for i := range 5 {
				if sc.Cell(c+i, r).Bg != first.Bg {
					return false
				}
			}
			return sc.Cell(c+caretAt, r).Underline
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the five selected cells do not share one ground, or the caret is not the underlined cell %d: %v\n%s", what, caretAt, err, term.Snapshot())
		}
		if s.Cell(col+5, row).Bg == s.Cell(col, row).Bg && caretAt != 5 {
			t.Errorf("ASSERTION: %s: the cell after the selection wears the selection's ground", what)
		}
		saveArtifact(t, term, art, "select-"+word)
	}
	press("\x1b[1;2D", 5)
	check("right to left", "gamma", 0)
	sendKeys(t, term, "Z")
	waitRenameField(t, term, "typing replaces the selection", "alpha beta Z")
	sendKeys(t, term, tuitest.Esc)
	openRenameOn(t, term, "alpha beta gamma")
	press("\x1b[H", 1)
	press("\x1b[1;2C", 5)
	check("left to right", "alpha", 5)
	press("\x1b[1;2F", 1)
	sendKeys(t, term, tuitest.Backspace)
	waitRenameField(t, term, "shift+end then backspace empties the field", "")
	alive(t, term, "after selecting in the rename field")
}

// TestComposerSelectionLooksTheSameBothWays types "hello world" in the new
// agent sheet's task box. Three shift+left select "rld": the three cells wear
// one ground, and the caret is the first of them, underlined, not a dim
// reversed cell. Home and three shift+right select "hel": the three cells wear
// the same ground, and the caret is the underlined cell after them.
func TestComposerSelectionLooksTheSameBothWays(t *testing.T) {
	term, _, _, _ := startCockpitFleet(t, 160, 45, nil)
	sendKeys(t, term, "n")
	waitText(t, term, "the new agent sheet", "Where it works")
	if err := term.Type("hello world"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the task typed", "hello world")
	art := artifactDir(t)
	// cells checks the n selected cells from offset from of "hello world":
	// one ground, none reversed, other than the text's, and the caret the
	// underlined cell at offset caretAt.
	cells := func(what string, from, n, caretAt int) {
		t.Helper()
		if err := term.WaitFor(func(sc tuitest.Screen) bool {
			r, c, ok := selectionCells(sc, "hello world")
			if !ok {
				return false
			}
			sel := sc.Cell(c+from, r)
			if sel.Bg == sc.Cell(c+(from+n+1)%11, r).Bg && from+n < 11 {
				return false
			}
			for i := range n {
				cl := sc.Cell(c+from+i, r)
				if cl.Bg != sel.Bg || cl.Reverse {
					return false
				}
			}
			return sc.Cell(c+caretAt, r).Underline
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the selected cells do not share one ground, or one is reversed, or the caret is not the underlined cell %d: %v\n%s", what, caretAt, err, term.Snapshot())
		}
		saveArtifact(t, term, art, "composer-select-"+strings.ReplaceAll(what, " ", "-"))
	}
	for range 3 {
		if err := term.Type("\x1b[1;2D"); err != nil {
			t.Fatal(err)
		}
	}
	cells("right to left", 8, 3, 8)
	if err := term.Type("\x1b[H"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := term.Type("\x1b[1;2C"); err != nil {
			t.Fatal(err)
		}
	}
	cells("left to right", 0, 3, 3)
	alive(t, term, "after selecting in the composer")
}
