package vt_test

// A resize must not lose, duplicate or reorder text.
//
// The property needs nobody to write down the right screen. Whatever a resize
// does to the layout, the text a guest printed is still the same text: the
// history and the screen, read top to bottom with soft-wrapped rows joined
// into the lines they came from, hold the same lines before and after any
// sequence of resizes. A ring at its cap may let the oldest lines go, so then
// the lines after are a tail of the lines before.
//
// It runs on whichever backend the build selects, so it holds both to it:
//
//	go test ./internal/vt/ -run ResizeConserves
//	go test -tags ghostty ./internal/vt/ -run ResizeConserves
//
// The content is drawn from what breaks a resize: wide runes, marks, emoji
// joined by ZWJ, colour runs and OSC 8 links, lines long enough to wrap, blank
// lines, a cursor parked above the bottom by a full-screen program, and the
// alternate screen. What it does not draw from is anything that erases, since
// an erase loses text on purpose.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// conserveRingCap is large enough that no case here fills the ring.
const conserveRingCap = 100000

// conserveScript is one generated case: what is written, and the resizes that
// follow it.
type conserveScript struct {
	cols, rows int
	in         string
	// alt is written after in, and leaves the main screen under an alternate
	// screen for the resizes; leave ends it.
	alt, leave string
	sizes      [][2]int
}

func (s conserveScript) String() string {
	return fmt.Sprintf("start %dx%d\nin %q\nalt %q\nsizes %v", s.cols, s.rows, s.in, s.alt, s.sizes)
}

// conserveAtoms has no space. A row that wraps before a wide rune ends in a
// blank column that is not text, and a space typed there would read the same.
var conserveAtoms = []string{
	"a", "b", "c", "x", "y", "z", "0", "1", "_", "-",
	"世", "界", "é", "é", "\U0001F468‍\U0001F469‍\U0001F467", "\U0001F1FA\U0001F1F8", "\u261d\ufe0f",
}

// genConserve draws a case from seed. widths says whether the resizes may
// change the width as well as the height.
func genConserve(seed uint64, widths bool) conserveScript {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	s := conserveScript{cols: 4 + r.IntN(14), rows: 2 + r.IntN(8)}
	var b strings.Builder
	lines := r.IntN(3 * s.rows)
	for range lines {
		switch r.IntN(10) {
		case 0:
			// A blank line.
		case 1:
			fmt.Fprintf(&b, "\x1b[3%dm", 1+r.IntN(6))
		case 2:
			fmt.Fprintf(&b, "\x1b]8;;http://h/%d\x07", r.IntN(4))
		}
		n := r.IntN(3 * s.cols)
		for range n {
			b.WriteString(conserveAtoms[r.IntN(len(conserveAtoms))])
		}
		if r.IntN(4) == 0 {
			b.WriteString("\x1b]8;;\x07\x1b[m")
		}
		b.WriteString("\r\n")
	}
	b.WriteString("\x1b[m\x1b]8;;\x07$ ")
	if r.IntN(3) == 0 {
		// A program that draws at the bottom and parks the cursor higher up.
		fmt.Fprintf(&b, "\x1b[%d;1Hstatus\x1b[%d;%dH", s.rows, 1+r.IntN(s.rows), 1+r.IntN(s.cols))
	}
	s.in = b.String()
	if r.IntN(4) == 0 {
		s.alt = "\x1b[?1049h\x1b[Hfull screen"
		s.leave = "\x1b[?1049l"
	}
	for range 1 + r.IntN(6) {
		w := s.cols
		if widths {
			// Two columns, because a wide rune cannot exist in one.
			w = 2 + r.IntN(19)
		}
		s.sizes = append(s.sizes, [2]int{w, 1 + r.IntN(12)})
	}
	return s
}

// cellKey is a cell as the property compares it: its text, its colours and
// attributes, and its link.
func cellKey(c uv.Cell) string {
	content := c.Content
	if content == "" {
		content = " "
	}
	if content == " " && c.Style.IsZero() && c.Link.URL == "" {
		return " "
	}
	return fmt.Sprintf("%s|%v|%v|%x|%s", content, c.Style.Fg, c.Style.Bg, c.Style.Attrs, c.Link.URL)
}

// logicalLines reads the history and the main screen as the lines the guest
// printed: soft-wrapped rows are joined, trailing blanks are dropped from
// each line, and trailing blank lines are dropped from the end.
func logicalLines(tm vt.Terminal) []string {
	type row struct {
		cells   uv.Line
		wrapped bool
	}
	var rows []row
	for i := range tm.ScrollbackLen() {
		w, _ := tm.ScrollbackSoftWrapped(i)
		rows = append(rows, row{tm.ScrollbackLine(i), w})
	}
	for y := range tm.Height() {
		var cells uv.Line
		for x := range tm.Width() {
			if c := tm.MainCellAt(x, y); c != nil {
				cells = append(cells, *c)
			} else {
				cells = append(cells, uv.EmptyCell)
			}
		}
		w, _ := tm.RowSoftWrapped(y)
		rows = append(rows, row{cells, w})
	}

	var out []string
	var cur []string
	// padded is set after a wrapped row that ends in a blank. When the next
	// row starts with a wide cell, that blank is the column the wide cell
	// did not fit in, not text, and where a row ends with one depends on the
	// width the line was wrapped at.
	padded := false
	for _, r := range rows {
		if padded && len(r.cells) > 0 && r.cells[0].Width > 1 {
			cur = cur[:len(cur)-1]
		}
		for _, c := range r.cells {
			if c.Width == 0 && c.Content == "" {
				continue // the second half of a wide cell
			}
			cur = append(cur, cellKey(c))
		}
		padded = r.wrapped && len(cur) > 0 && cur[len(cur)-1] == " "
		if r.wrapped {
			continue
		}
		for len(cur) > 0 && cur[len(cur)-1] == " " {
			cur = cur[:len(cur)-1]
		}
		out = append(out, strings.Join(cur, "\x00"))
		cur = nil
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\x00"))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// conserveProblem runs s and returns what it lost, or "".
func conserveProblem(s conserveScript) string {
	tm := vt.NewWithScrollback(s.cols, s.rows, conserveRingCap)
	defer func() { _ = tm.Close() }()
	_, _ = tm.Write([]byte(s.in))
	before := logicalLines(tm)
	if s.alt != "" {
		_, _ = tm.Write([]byte(s.alt))
	}
	for _, sz := range s.sizes {
		tm.Resize(sz[0], sz[1])
	}
	if s.leave != "" {
		_, _ = tm.Write([]byte(s.leave))
	}
	after := logicalLines(tm)
	if slices.Equal(before, after) {
		return ""
	}
	show := func(ls []string) string {
		var b strings.Builder
		for i, l := range ls {
			var t strings.Builder
			for k := range strings.SplitSeq(l, "\x00") {
				t.WriteString(strings.SplitN(k, "|", 2)[0])
			}
			fmt.Fprintf(&b, "  %2d %q\n", i, t.String())
		}
		return b.String()
	}
	return fmt.Sprintf("the lines changed across the resizes\nbefore:\n%safter:\n%s", show(before), show(after))
}

// TestResizeConservesTextHeights changes only the height. A screen that gets
// shorter has to put its rows somewhere, and one that gets taller has to keep
// the ones it has.
func TestResizeConservesTextHeights(t *testing.T) {
	n := uint64(400)
	if testing.Short() {
		n = 100
	}
	for seed := range n {
		s := genConserve(seed, false)
		if p := conserveProblem(s); p != "" {
			t.Fatalf("seed %d\n%s\n%s", seed, s, p)
		}
	}
}

// FuzzResizeConservesTextHeights is the same property under the mutator.
func FuzzResizeConservesTextHeights(f *testing.F) {
	for seed := range uint64(8) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		s := genConserve(seed, false)
		if p := conserveProblem(s); p != "" {
			t.Fatalf("seed %d\n%s\n%s", seed, s, p)
		}
	})
}
