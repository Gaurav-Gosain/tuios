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
	// checkCursor says the cursor has to stay on the character it was on.
	checkCursor bool
	// ringCap, when set, is the ring's line cap; the oldest lines may go.
	ringCap int
	// parked says a program left the cursor above text it drew lower down.
	parked bool
	// checkMarks says the OSC 133 marks have to stay on their text. A mark
	// on an open prompt's row past a narrower width is clamped to it.
	checkMarks bool
	// saved says the cursor was saved (DECSC) at the end of the last line
	// of output, past its text, and has to come back there.
	saved bool
	// open says the last prompt is marked open with OSC 133 A and B, so
	// the reflow keeps it on its rows and clamps a cursor or mark past the
	// width, which the shell repaints.
	open bool
}

func (s conserveScript) String() string {
	return fmt.Sprintf("start %dx%d\nin %q\nalt %q\nsizes %v", s.cols, s.rows, s.in, s.alt, s.sizes)
}

// conserveAtoms has no space. A row that wraps before a wide rune ends in a
// blank column that is not text, and a space typed there would read the same.
var conserveAtoms = []string{
	"a", "b", "c", "x", "y", "z", "0", "1", "_", "-",
	"\u4e16", "\u754c", "\u00e9", "e\u0301", "\U0001F468\u200d\U0001F469\u200d\U0001F467", "\U0001F1FA\U0001F1F8", "\u261d\ufe0f",
}

// genConserve draws a case from seed. widths says whether the resizes may
// change the width as well as the height.
func genConserve(seed uint64, widths bool) conserveScript {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	s := conserveScript{cols: 4 + r.IntN(14), rows: 2 + r.IntN(8)}
	var b strings.Builder
	lines := r.IntN(3 * s.rows)
	for i := range lines {
		if r.IntN(4) == 0 {
			// A shell's marks: the prompt's start, then the output's.
			b.WriteString("\x1b]133;A\x07")
		}
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
		if r.IntN(4) == 0 {
			// A mark just past the text, where a shell marks the end of a
			// command line.
			b.WriteString("\x1b]133;C\x07")
		}
		if i == lines-1 && r.IntN(3) == 0 {
			b.WriteString("\x1b7")
			s.saved = true
		}
		b.WriteString("\r\n")
	}
	b.WriteString("\x1b[m\x1b]8;;\x07")
	open := r.IntN(3) == 0
	if open {
		b.WriteString("\x1b]133;A\x07$ \x1b]133;B\x07")
	} else {
		b.WriteString("$ ")
	}
	// An open prompt is resized back to its first size at the end (see
	// conserveProblem), so the cursor on it has to come back where it was,
	// even from a width that could not show its column.
	s.checkCursor = true
	s.checkMarks = !open
	if !open && r.IntN(3) == 0 {
		// A program that draws at the bottom and parks the cursor higher up.
		// A screen too short for the text below the cursor keeps the text
		// and lets the cursor's row go into the history, as ghostty and
		// kitty do, so the cursor is not followed here.
		s.checkCursor = false
		s.parked = true
		fmt.Fprintf(&b, "\x1b[%d;1Hstatus\x1b[%d;%dH", s.rows, 1+r.IntN(s.rows), 1+r.IntN(s.cols))
	}
	s.in = b.String()
	// A shell's open prompt is where the cursor is, with nothing under it,
	// so it is not drawn together with a parked cursor.
	s.open = open
	if r.IntN(4) == 0 {
		s.checkCursor = false
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
	lines, _ := logicalLinesCursor(tm)
	return lines
}

// logicalLinesCursor is logicalLines, and where the cursor is in them: the
// line and the number of characters before it in the line.
func logicalLinesCursor(tm vt.Terminal) ([]string, [2]int) {
	cy := tm.ScrollbackLen() + tm.CursorPosition().Y
	lines, at := logicalLinesAt(tm, [][2]int{{cy, tm.CursorPosition().X}})
	return lines, at[0]
}

// logicalLinesAt is logicalLines, and where each point, a row counted from
// the oldest history row and a column, is in them: the line and the number
// of characters before it in the line. A point past a row's last cell is
// found at that row's end.
func logicalLinesAt(tm vt.Terminal, points [][2]int) ([]string, [][2]int) {
	at := make([][2]int, len(points))
	for i := range at {
		at[i] = [2]int{-1, -1}
	}
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
	for i, r := range rows {
		if padded && len(r.cells) > 0 && r.cells[0].Width > 1 {
			cur = cur[:len(cur)-1]
		}
		for k, pt := range points {
			if pt[0] == i && pt[1] >= len(r.cells) {
				at[k] = [2]int{len(out), len(cur) + (pt[1] - len(r.cells)) + countCells(r.cells)}
			}
		}
		for x, c := range r.cells {
			for k, pt := range points {
				if pt[0] == i && pt[1] == x {
					at[k] = [2]int{len(out), len(cur)}
				}
			}
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
	return out, at
}

// countCells is the number of characters in a row, not counting the second
// halves of wide ones.
func countCells(cells uv.Line) int {
	n := 0
	for _, c := range cells {
		if c.Width != 0 || c.Content != "" {
			n++
		}
	}
	return n
}

// markPoints is where the terminal's OSC 133 marks are, as points for
// logicalLinesAt.
func markPoints(tm vt.Terminal) [][2]int {
	var pts [][2]int
	for _, m := range tm.SemanticMarkers().Markers() {
		pts = append(pts, [2]int{m.AbsLine, m.Col})
	}
	return pts
}

// conserveProblem runs s and returns what it lost, or "".
func conserveProblem(s conserveScript) string {
	ringCap := conserveRingCap
	if s.ringCap > 0 {
		ringCap = s.ringCap
	}
	tm := vt.NewWithScrollback(s.cols, s.rows, ringCap)
	defer func() { _ = tm.Close() }()
	_, _ = tm.Write([]byte(s.in))
	before, curBefore := logicalLinesCursor(tm)
	_, marksBefore := logicalLinesAt(tm, markPoints(tm))
	var savedBefore [2]int
	if s.saved {
		// Where the saved cursor is, read on a copy: restoring it here would
		// move the cursor the resizes are to carry.
		cp := vt.NewWithScrollback(s.cols, s.rows, ringCap)
		_, _ = cp.Write([]byte(s.in + "\x1b8"))
		_, savedBefore = logicalLinesCursor(cp)
		_ = cp.Close()
	}
	if s.alt != "" {
		_, _ = tm.Write([]byte(s.alt))
	}
	for _, sz := range s.sizes {
		tm.Resize(sz[0], sz[1])
	}
	if s.open {
		// An open prompt keeps each of its rows on one row, and what does
		// not fit a narrower screen waits off the edge for the shell to
		// repaint. Back at the size it was printed at, it all shows again.
		tm.Resize(s.cols, s.rows)
	}
	if s.leave != "" {
		_, _ = tm.Write([]byte(s.leave))
	}
	after, curAfter := logicalLinesCursor(tm)
	if s.ringCap > 0 && len(after) <= len(before) && len(after) > 0 {
		// The oldest lines may have gone. The first line left can be the
		// tail of a line whose start went, so it is compared as a suffix.
		tail := before[len(before)-len(after):]
		if slices.Equal(tail[1:], after[1:]) && strings.HasSuffix(tail[0], after[0]) {
			return ""
		}
	}
	if slices.Equal(before, after) {
		if s.checkCursor && curBefore != curAfter {
			return fmt.Sprintf("the cursor moved off its text: line %d character %d before, line %d character %d after",
				curBefore[0], curBefore[1], curAfter[0], curAfter[1])
		}
		// The libghostty backend does not move the marks with a reflow;
		// see the report on the backends' resize behaviour.
		if s.checkMarks && s.checkCursor && vt.Backend != "ghostty" {
			_, marksAfter := logicalLinesAt(tm, markPoints(tm))
			if !slices.Equal(marksBefore, marksAfter) {
				return fmt.Sprintf("an OSC 133 mark moved off its text: %v before, %v after", marksBefore, marksAfter)
			}
		}
		// The saved cursor is checked where it was saved: on its line, up to
		// just past the text. A DECSC followed by a scroll names a screen
		// row, not a line, and lands wherever the scroll left it, so a
		// position further past the text is not text to follow. DECRC
		// clamps to the screen, so a saved place just past text that fills
		// the last row comes back on the last character.
		if s.saved && s.checkMarks && s.checkCursor && s.alt == "" && minHeight(s.sizes) >= 2 && vt.Backend != "ghostty" &&
			savedBefore[0] >= 0 && savedBefore[0] < len(before) &&
			savedBefore[1] <= lineLen(before[savedBefore[0]]) {
			// A saved place whose row went into the history at any step
			// comes back at the top of the screen; that is not a place to
			// follow either.
			if savedLeftScreen(s, ringCap, savedBefore) {
				return ""
			}
			_, _ = tm.Write([]byte("\x1b8"))
			_, savedAfter := logicalLinesCursor(tm)
			clamped := savedAfter[0] == savedBefore[0] && savedAfter[1] == savedBefore[1]-1 &&
				savedBefore[1] == lineLen(before[savedBefore[0]]) &&
				tm.CursorPosition().X == tm.Width()-1
			if savedAfter != savedBefore && !clamped {
				return fmt.Sprintf("the saved cursor moved off its text: %v before, %v after", savedBefore, savedAfter)
			}
		}
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

// ghosttyJoinsLines is the libghostty bug the width property finds: with the
// cursor parked on a blank row above text, a narrowing that wraps the
// cursor's row and a widening after it join that row to the line below and
// drop the blank rows between. TestGhosttyParkedCursorJoinsLines pins it, so
// the skip goes when the bug does.
func ghosttyJoinsLines(s conserveScript) bool {
	return vt.Backend == "ghostty" && s.parked
}

// TestResizeConservesText changes the width as well. The main screen reflows,
// so a line wider than a narrower screen wraps instead of losing its tail, and
// the cursor stays on the character it was on.
func TestResizeConservesText(t *testing.T) {
	n := uint64(3000)
	if testing.Short() {
		n = 300
	}
	for seed := range n {
		s := genConserve(seed, true)
		if ghosttyJoinsLines(s) {
			continue
		}
		if p := conserveProblem(s); p != "" {
			t.Fatalf("seed %d\n%s\n%s", seed, s, p)
		}
	}
}

// FuzzResizeConservesText is the same property under the mutator.
func FuzzResizeConservesText(f *testing.F) {
	for seed := range uint64(8) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		s := genConserve(seed, true)
		if ghosttyJoinsLines(s) {
			t.Skip("libghostty joins lines here; see ghosttyJoinsLines")
		}
		if p := conserveProblem(s); p != "" {
			t.Fatalf("seed %d\n%s\n%s", seed, s, p)
		}
	})
}

// TestResizeConservesTextAtRingCap runs the width property on a ring that is
// full, so the rows a reflow pushes back evict the oldest lines. What is left
// has to be the newest lines, whole and in order.
func TestResizeConservesTextAtRingCap(t *testing.T) {
	n := uint64(2000)
	if testing.Short() {
		n = 200
	}
	for seed := range n {
		s := genConserve(seed, true)
		s.ringCap = 6
		s.checkCursor = false
		if ghosttyJoinsLines(s) {
			continue
		}
		if p := conserveProblem(s); p != "" {
			t.Fatalf("seed %d\n%s\n%s", seed, s, p)
		}
	}
}

// TestGhosttyParkedCursorJoinsLines pins the libghostty bug the width
// property skips. A blank row holds the cursor in its last column, above a
// row of text. Narrowing wraps the cursor's row, and a short screen and a
// widening after it join it to the text below: "status" moves to column 8 of
// the cursor's row and a blank line is gone. If this starts passing, delete
// it and ghosttyJoinsLines.
func TestGhosttyParkedCursorJoinsLines(t *testing.T) {
	if vt.Backend != "ghostty" {
		t.Skip("the pure Go emulator keeps these lines apart")
	}
	tm := vt.NewWithScrollback(15, 4, conserveRingCap)
	defer func() { _ = tm.Close() }()
	_, _ = tm.Write([]byte("$ \x1b[4;1Hstatus\x1b[3;15H"))
	before := logicalLines(tm)
	tm.Resize(8, 1)
	tm.Resize(15, 7)
	if after := logicalLines(tm); slices.Equal(before, after) {
		t.Errorf("libghostty no longer joins the lines; delete this test and ghosttyJoinsLines")
	}
}

// minHeight is the shortest of sizes.
func minHeight(sizes [][2]int) int {
	h := 1 << 30
	for _, sz := range sizes {
		h = min(h, sz[1])
	}
	return h
}

// lineLen is the number of characters in a line logicalLines returned.
func lineLen(l string) int {
	if l == "" {
		return 0
	}
	return strings.Count(l, "\x00") + 1
}

// savedLeftScreen reports whether, at any step of s, the screen's top row
// was past the saved place, so the saved row had gone into the history.
func savedLeftScreen(s conserveScript, ringCap int, saved [2]int) bool {
	tm := vt.NewWithScrollback(s.cols, s.rows, ringCap)
	defer func() { _ = tm.Close() }()
	_, _ = tm.Write([]byte(s.in))
	for _, sz := range s.sizes {
		tm.Resize(sz[0], sz[1])
		_, top := logicalLinesAt(tm, [][2]int{{tm.ScrollbackLen(), 0}})
		if top[0][0] > saved[0] || top[0][0] == saved[0] && top[0][1] > saved[1] {
			return true
		}
	}
	return false
}
