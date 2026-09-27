package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// The review overlay's diff at each colour depth a terminal can have, on a
// dark terminal and under a light theme, in the unified and the split view.
//
// Below truecolor the diff's tints used to be stepped down one colour at a
// time by the frame writer, and an added line and a removed line both landed
// on the same grey (239 at 256 colours, bright black at 16): only the sign
// column still told them apart. The diff is designed per depth instead:
//
//   - truecolor: the designed tints, an added and a removed line on grounds of
//     their own.
//   - 256 colours: fixed palette entries, the ones Codex uses. On a dark ground
//     an added line is 22 and a removed one 52, with the changed words one step
//     brighter on 28 and 88; on a light ground 194 and 224, with the words on
//     157 and 217.
//   - 16 colours: no line grounds at all. The line numbers are in green for an
//     added line and red for a removed one, the changed words are bold and
//     underlined, and the cursor is reverse video on the gutter only, so the
//     code keeps its colours and its marks under it.
//
// Each frame is saved as text, as styled text and as a PNG drawn by tuios's
// own renderer (internal/shot), with the palette a terminal of that kind
// paints the sixteen slots with.

// reviewDepthBase is the file the fan's base commit holds; reviewDepthNew is
// what the second attempt changes it to. Between them: a removed line with no
// counterpart (the const), a changed line whose changed words are marked (the
// return), and an added line with no counterpart (the second Println).
const reviewDepthBase = `package main

import "fmt"

const retries = 3

func greet(name string) string {
	return "hello " + name
}

func main() {
	fmt.Println(greet("world"))
}
`

const reviewDepthNew = `package main

import "fmt"

func greet(name string) string {
	return "good morning " + name
}

func main() {
	fmt.Println(greet("world"))
	fmt.Println("done")
}
`

// reviewDiffCells are the cells of one frame of the diff the checks read.
type reviewDiffCells struct {
	// del and add are a code cell of the removed and the added line that have
	// no counterpart.
	del, add tuitest.Cell
	// delNum and addNum are a digit of each of those lines' numbers.
	delNum, addNum tuitest.Cell
	// chgDelLine and chgAddLine are a cell of the changed line's old and new
	// text outside the changed words ("return"); chgDelWord and chgAddWord a
	// cell inside them ("hello", "good").
	chgDelLine, chgAddLine, chgDelWord, chgAddWord tuitest.Cell
	// ctx is a code cell of a context line.
	ctx tuitest.Cell
}

// readReviewDiff finds the cells reviewDiffCells names. Every line is
// found by its own text, so a stale frame or a different layout fails here
// rather than passing on the wrong cells.
func readReviewDiff(t *testing.T, term *tuitest.Terminal) reviewDiffCells {
	t.Helper()
	s := term.Screen()
	at := func(text string, off int) (int, int) {
		row, col, ok := textAt(s, text, 0)
		if !ok {
			t.Fatalf("%q is not on screen\n%s", text, term.Snapshot())
		}
		return row, col + off
	}
	cell := func(text string, off int) tuitest.Cell {
		row, col := at(text, off)
		return s.Cell(col, row)
	}
	var d reviewDiffCells
	d.del = cell("const retries", 0)
	d.add = cell(`fmt.Println("done")`, 0)
	d.delNum = reviewLineNumber(t, term, s, "const retries")
	d.addNum = reviewLineNumber(t, term, s, `fmt.Println("done")`)
	d.chgDelLine = cell(`return "hello `, 0)
	d.chgDelWord = cell(`return "hello `, len(`return "`))
	d.chgAddLine = cell(`return "good morning `, 0)
	d.chgAddWord = cell(`return "good morning `, len(`return "`))
	d.ctx = cell(`func greet(name`, 0)
	return d
}

// reviewLineNumber is the last digit of the line number in front of the line
// whose code starts with text: left of the code, past the blanks, the sign,
// and the blanks between the sign and the number.
func reviewLineNumber(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, text string) tuitest.Cell {
	t.Helper()
	row, col, ok := textAt(s, text, 0)
	if !ok {
		t.Fatalf("%q is not on screen\n%s", text, term.Snapshot())
	}
	x := col - 1
	for x >= 0 && strings.TrimSpace(s.Cell(x, row).Content) == "" {
		x--
	}
	if x < 0 || (s.Cell(x, row).Content != "+" && s.Cell(x, row).Content != "-") {
		t.Fatalf("no sign in front of %q on row %d\n%s", text, row, term.Snapshot())
	}
	for x--; x >= 0 && strings.TrimSpace(s.Cell(x, row).Content) == ""; x-- {
	}
	if x < 0 || !strings.ContainsAny(s.Cell(x, row).Content, "0123456789") {
		t.Fatalf("no line number in front of %q on row %d\n%s", text, row, term.Snapshot())
	}
	return s.Cell(x, row)
}

// TestReviewDiffAtEveryColourDepth opens the review of a changed Go file under
// each colour depth, on a dark terminal and under a light theme, in the
// unified and the split view, and holds the diff to what the depth is
// designed to show (see the comment at the top of this file).
//
// How this could pass wrongly, written down first:
//   - The profile could be ignored and every run drawn in truecolor. The 256
//     runs require exact palette indices and the 16 runs default grounds,
//     which truecolor never writes.
//   - A cell could be read off a stale frame, or off the file list rather than
//     the diff. Every line is found by text only the diff shows, and the split
//     view is waited for by the footer key only it offers.
//   - An added and a removed line could still share a ground and pass because
//     each is checked alone. They are compared to each other, and at 16
//     colours their line numbers must be in different slots.
//   - At 16 colours the changed words could be marked by a ground the frame
//     writer happened to keep. Every code cell is required to be on the
//     default ground, and the words to be bold and underlined while the rest
//     of the line is not underlined.
//   - At 16 colours the cursor could reverse the whole row, which drops the
//     code's colours and hides the changed words' mark. The cursor is moved
//     onto the changed line and only its gutter may be reversed.
//
// Negative controls are in NEGATIVE_CONTROLS.md.
func TestReviewDiffAtEveryColourDepth(t *testing.T) {
	for _, look := range chromeLooks {
		for _, d := range chromeDepths {
			t.Run(look.name+"-"+d.name, func(t *testing.T) {
				base, repo := fanFixture(t)
				if look.theme != "" {
					writeConfig(t, base, "[appearance]\ntheme = \""+look.theme+"\"\n")
				}
				if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte(reviewDepthBase), 0o644); err != nil {
					t.Fatal(err)
				}
				testutil.Git(t, repo, "add", "main.go")
				testutil.Git(t, repo, "commit", "-q", "-m", "main")
				session, path := reviewFanAt(t, base, repo, "dp")
				if err := os.WriteFile(filepath.Join(path, "main.go"), []byte(reviewDepthNew), 0o644); err != nil {
					t.Fatal(err)
				}

				dir := artifactDir(t)
				host := hostPalette(t, look.theme)
				term := attachIn(t, base, session, startOpts{cols: 160, rows: 40, env: d.env})
				sendKeys(t, term, tuitest.Ctrl('b'), "v")
				waitScreen(t, term, "the review never opened", "Review", "M main.go", `"good morning `, "s split")
				saveArtifact(t, term, dir, "unified")
				savePNG(t, term.Screen(), host, dir, "unified")
				checkReviewDiff(t, term, look.name, d.name, "unified")

				if d.name == "16" {
					checkReviewCursor16(t, term, host, dir)
				}

				sendKeys(t, term, "s")
				waitScreen(t, term, "s did not switch to the split view", "s unified", `"good morning `)
				saveArtifact(t, term, dir, "split")
				savePNG(t, term.Screen(), host, dir, "split")
				checkReviewDiff(t, term, look.name, d.name, "split")
			})
		}
	}
}

// reviewDiff256 are the palette entries the diff takes at 256 colours: the
// removed line, the added line, the removed words and the added words.
var reviewDiff256 = map[string][4]uint8{
	"dark":  {52, 22, 88, 28},
	"light": {224, 194, 217, 157},
}

func checkReviewDiff(t *testing.T, term *tuitest.Terminal, look, depth, view string) {
	t.Helper()
	c := readReviewDiff(t, term)
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf(view+": "+format+"\n%s", append(args, term.SnapshotStyled())...)
	}
	switch depth {
	case "16":
		for name, cell := range map[string]tuitest.Cell{
			"removed line": c.del, "added line": c.add, "context line": c.ctx,
			"old changed line": c.chgDelLine, "new changed line": c.chgAddLine,
			"old changed words": c.chgDelWord, "new changed words": c.chgAddWord,
		} {
			if cell.Bg.Kind != tuitest.ColorDefault || cell.Reverse {
				fail("the %s's code has ground %+v (reverse %v) at 16 colours, want the terminal's own", name, cell.Bg, cell.Reverse)
			}
		}
		for name, cell := range map[string]tuitest.Cell{"old": c.chgDelWord, "new": c.chgAddWord} {
			if !cell.Bold || !cell.Underline {
				fail("the %s changed words are %+v, want bold and underlined", name, cell)
			}
		}
		for name, cell := range map[string]tuitest.Cell{"old": c.chgDelLine, "new": c.chgAddLine} {
			if cell.Underline {
				fail("the %s changed line is underlined outside its changed words: %+v", name, cell)
			}
		}
		if c.addNum.Fg != (tuitest.Color{Kind: tuitest.ColorIndexed, Index: 2}) {
			fail("the added line's number is drawn in %+v, want slot 2 (green)", c.addNum.Fg)
		}
		if c.delNum.Fg != (tuitest.Color{Kind: tuitest.ColorIndexed, Index: 1}) {
			fail("the removed line's number is drawn in %+v, want slot 1 (red)", c.delNum.Fg)
		}
	case "256":
		want := reviewDiff256[look]
		for i, got := range []tuitest.Cell{c.del, c.add, c.chgDelWord, c.chgAddWord} {
			name := [...]string{"removed line", "added line", "removed words", "added words"}[i]
			if got.Bg != (tuitest.Color{Kind: tuitest.ColorIndexed, Index: want[i]}) {
				fail("the %s's ground is %+v, want palette entry %d", name, got.Bg, want[i])
			}
		}
		if c.chgDelLine.Bg != c.del.Bg || c.chgAddLine.Bg != c.add.Bg {
			fail("a changed line's ground outside its words (%+v, %+v) is not its kind's (%+v, %+v)",
				c.chgDelLine.Bg, c.chgAddLine.Bg, c.del.Bg, c.add.Bg)
		}
		// The numbers carry the kind's colour too. A red placed on the
		// palette by nearest match can land on a grey, which is what
		// colorprofile did with Catppuccin Latte's red.
		for name, cell := range map[string]tuitest.Cell{"removed": c.delNum, "added": c.addNum} {
			if cell.Fg.Kind != tuitest.ColorIndexed || grey256(cell.Fg.Index) {
				fail("the %s line's number is drawn in %+v, a grey or not a palette entry", name, cell.Fg)
			}
		}
	default:
		if c.del.Bg.Kind != tuitest.ColorRGB || c.add.Bg.Kind != tuitest.ColorRGB {
			fail("truecolor line grounds are not RGB: removed %+v, added %+v", c.del.Bg, c.add.Bg)
		}
		if c.del.Bg == c.add.Bg {
			fail("an added and a removed line share the ground %+v", c.add.Bg)
		}
		if c.chgAddWord.Bg == c.chgAddLine.Bg || c.chgDelWord.Bg == c.chgDelLine.Bg {
			fail("the changed words are on their line's ground")
		}
	}
	if depth != "16" && (c.del.Bg == c.add.Bg || c.ctx.Bg == c.add.Bg || c.ctx.Bg == c.del.Bg) {
		fail("the removed %+v, added %+v and context %+v lines do not each have their own ground", c.del.Bg, c.add.Bg, c.ctx.Bg)
	}
	if c.delNum.Fg == c.addNum.Fg {
		fail("the removed and the added line's numbers share the ink %+v", c.addNum.Fg)
	}
}

// grey256 reports whether xterm palette entry i is a grey: the grey ramp, or
// a cube entry with its three channels equal.
func grey256(i uint8) bool {
	if i >= 232 {
		return true
	}
	if i < 16 {
		return false
	}
	v := int(i) - 16
	return v/36 == v/6%6 && v/6%6 == v%6
}

// checkReviewCursor16 moves the diff's cursor onto the new side of the
// changed line and holds it to the 16-colour cursor: the line number reversed,
// the code under it neither reversed nor stripped of its marks.
func checkReviewCursor16(t *testing.T, term *tuitest.Terminal, host *shot.Palette, dir string) {
	t.Helper()
	onLine := func(s tuitest.Screen) bool {
		row, col, ok := textAt(s, `return "good morning `, 0)
		if !ok {
			return false
		}
		for x := col - 1; x >= 0; x-- {
			if strings.ContainsAny(s.Cell(x, row).Content, "0123456789") {
				return s.Cell(x, row).Reverse
			}
		}
		return false
	}
	for range 20 {
		if onLine(term.Screen()) {
			break
		}
		sendKeys(t, term, "j")
	}
	if !onLine(term.Screen()) {
		t.Fatalf("the cursor never reached the changed line with its number reversed\n%s", term.SnapshotStyled())
	}
	s := term.Screen()
	saveArtifact(t, term, dir, "unified-cursor")
	savePNG(t, s, host, dir, "unified-cursor")
	c := readReviewDiff(t, term)
	if c.chgAddLine.Reverse || c.chgAddWord.Reverse {
		t.Fatalf("the code under the cursor is reversed: line %+v, words %+v\n%s", c.chgAddLine, c.chgAddWord, term.SnapshotStyled())
	}
	if !c.chgAddWord.Bold || !c.chgAddWord.Underline {
		t.Fatalf("the changed words under the cursor lost their mark: %+v\n%s", c.chgAddWord, term.SnapshotStyled())
	}
}
