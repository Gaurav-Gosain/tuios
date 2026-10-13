package tuie2e

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// A rail section with more rows than lines draws a scroll thumb in its last
// content column, saying where in its rows the section is. The rest of the
// column is blank. The tests read the column off a real client's screen,
// wheel the section from its top to its bottom and back, and save every
// screen they read.
//
// How they could pass wrongly, and what stops it:
//   - a thumb drawn on every section, overflowing or not: a section that fits
//     must show no thumb, and its rows must read exactly as they did before
//     any section overflowed.
//   - a thumb that never moves: the middle state needs the thumb off both the
//     first and the last line, and the bottom state needs it on the last.
//   - plain rounding putting the thumb on the first line one notch into the
//     scroll: the custom fixture's numbers (70 rows on 8 lines, a 3-row
//     notch) round to the top, so the middle state fails without the clamp.
//   - the thumb sitting on the "+N" line while rows are still hidden: the
//     "+N" line must be blank in the column, and the thumb reaches the last
//     line only in the state with no "+N".
//   - anything but the thumb in the column, such as a track: every other
//     line of the section must be blank there.
//   - the thumb drawn over the last content column instead of reserving it:
//     a cut row's ellipsis must sit in the column before the thumb's.
//   - a thumb told apart only by colour: the 16-colour run asserts the glyph,
//     and the ASCII run asserts the ASCII one.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Rail scrollbar on
// overflowing sections").

// railScrollTitle is the custom section's heading, a word nothing else on the
// screen prints.
const railScrollTitle = "scrollfeed"

// railScrollFeed is a custom section of 70 rows, each long enough to be cut
// at the rail's width. Its share gives it 8 lines on a 120x40 screen.
func railScrollFeed(position string) string {
	return "[appearance.sidebar]\nenabled = true\nposition = \"" + position + "\"\nwidth = 28\n" +
		"sections = \"sessions,custom:25\"\n\n[appearance.sidebar.custom]\ntitle = \"" + railScrollTitle + "\"\n" +
		"command = \"i=1; while [ $i -le 70 ]; do printf 'FEED-%02d-abcdefghijklmnopqrstuvwxyz\\\\n' $i; i=$((i+1)); done\"\n"
}

// railBar is how a test reads one rail: the thumb's column, and the columns
// of the rail's content.
type railBar struct {
	col    int // the thumb's column
	cut    int // the content column before it
	x0, x1 int // the rail's columns, the edge rule included
	thumb  string
}

// leftRailBar reads a 28-column rail on the left: the content is columns 0
// to 26, the edge rule 27, so the thumb's column is 26.
func leftRailBar(thumb string) railBar {
	return railBar{col: railWidth - 2, cut: railWidth - 3, x0: 0, x1: railWidth, thumb: thumb}
}

// rightRailBar reads a 28-column rail on the right of a cols-wide screen:
// the edge rule faces the panes, so the thumb's column is the screen's last.
func rightRailBar(cols int, thumb string) railBar {
	return railBar{col: cols - 1, cut: cols - 2, x0: cols - railWidth, x1: cols, thumb: thumb}
}

func (b railBar) line(s tuitest.Screen, row int) string {
	runes := []rune(s.Line(row))
	return string(runes[min(b.x0, len(runes)):min(b.x1, len(runes))])
}

// header is the screen row whose rail text starts with label, or -1. The
// text starts after the edge rule on a right rail.
func (b railBar) header(s tuitest.Screen, label string) int {
	_, rows := s.Size()
	for r := range rows {
		if f := strings.Fields(strings.TrimLeft(b.line(s, r), "│|")); len(f) > 0 && f[0] == label {
			return r
		}
	}
	return -1
}

// cells is the thumb's column on the lines of the section under a header,
// one entry per line. A line is the section's while it carries the row mark,
// a text every row of the fixture's section holds, or a "+N".
func (b railBar) cells(s tuitest.Screen, header, rowMark string) (top int, cells []string) {
	top = b.header(s, header) + 1
	_, rows := s.Size()
	for r := top; top > 0 && r < rows; r++ {
		if text := b.line(s, r); !strings.Contains(text, rowMark) && !plusCountRe.MatchString(text) {
			break
		}
		cells = append(cells, s.Cell(b.col, r).Content)
	}
	return top, cells
}

// state is where the thumb is: "top", "middle", "bottom", or a description
// of what is wrong with the column.
func (b railBar) state(s tuitest.Screen, header, rowMark string) string {
	top, cells := b.cells(s, header, rowMark)
	if len(cells) < 2 {
		return fmt.Sprintf("no section (%d lines)", len(cells))
	}
	for i, c := range cells {
		if c != b.thumb && c != " " {
			return fmt.Sprintf("%q on line %d of the column", c, i)
		}
	}
	first := slices.Index(cells, b.thumb)
	if first < 0 {
		return "no thumb"
	}
	end := first
	for end < len(cells) && cells[end] == b.thumb {
		end++
	}
	if slices.Contains(cells[end:], b.thumb) {
		return "a thumb in two pieces"
	}
	plusLine := plusCountRe.MatchString(b.line(s, top+len(cells)-1))
	switch {
	case first == 0 && end < len(cells) && plusLine:
		return "top"
	case first > 0 && end < len(cells) && plusLine:
		return "middle"
	case first > 0 && end == len(cells) && !plusLine:
		return "bottom"
	}
	return fmt.Sprintf("thumb on lines %d to %d of %d, +N on the last line %v", first, end-1, len(cells), plusLine)
}

// waitBarState waits for the thumb to reach want and saves the screen.
func waitBarState(t *testing.T, term *tuitest.Terminal, b railBar, header, rowMark, want, dir, name string) tuitest.Screen {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return b.state(s, header, rowMark) == want }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the %s section's thumb is %q, want %q: %v\n%s", header, b.state(term.Screen(), header, rowMark), want, err, term.Snapshot())
	}
	saveArtifact(t, term, dir, name)
	return term.Screen()
}

// reserved checks that the thumb's column is taken from the rows rather than
// drawn over them: a cut row's ellipsis sits in the column before it, and the
// "+N" line has a blank there.
func reserved(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, b railBar, header, rowMark string) {
	t.Helper()
	top, cells := b.cells(s, header, rowMark)
	for i := range cells {
		got := s.Cell(b.cut, top+i).Content
		want := "…"
		if i == len(cells)-1 && plusCountRe.MatchString(b.line(s, top+i)) {
			want = " "
		}
		if got != want {
			t.Fatalf("ASSERTION: line %d of the %s section has %q before the thumb's column, want %q\n%s", i, header, got, want, term.Snapshot())
		}
	}
}

// TestRailScrollbarFollowsTheWheel: the thumb starts at the top, moves off
// both ends after one notch, reaches the bottom when the wheel runs out of
// rows, and comes back to the top. The terminals section and the custom
// section are both driven, and the custom one on either edge, since a right
// rail composes its line the other way round.
func TestRailScrollbarFollowsTheWheel(t *testing.T) {
	t.Run("custom", func(t *testing.T) {
		term, _ := railClient(t, "bar", railScrollFeed("left"), startOpts{cols: 120, rows: 40})
		railBarWalk(t, term, leftRailBar("┃"), railScrollTitle, "FEED-", true)
	})
	t.Run("custom-right", func(t *testing.T) {
		term, _ := railClient(t, "bar", railScrollFeed("right"), startOpts{cols: 120, rows: 40})
		railBarWalk(t, term, rightRailBar(120, "┃"), railScrollTitle, "FEED-", true)
	})
	t.Run("terminals", func(t *testing.T) {
		term, base := railClient(t, "bar", "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\nsections = \"sessions,terminals:20\"\n",
			startOpts{cols: 120, rows: 40})
		openPanes(t, base, "bar", 13)
		railBarWalk(t, term, leftRailBar("┃"), "terminals", "pane", false)
	})
}

// TestRailScrollbarOnAPeek: hovering another session's row shows its panes in
// the terminals section, windowed at the offset the attached session's list
// is scrolled to. With the attached list at its bottom and the peeked one as
// long, the peek's first rows are out of view above, and only the thumb says
// so: there is no "+N" below. The thumb is at the bottom of the column.
func TestRailScrollbarOnAPeek(t *testing.T) {
	term, base := railClient(t, "bar", "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\nsections = \"sessions,terminals:20\"\n",
		startOpts{cols: 120, rows: 40})
	openPanes(t, base, "bar", 13)
	if out, err := tuiosCLI(t, base, "new", "other", "--detach"); err != nil {
		t.Fatalf("create the other session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "other", "--name", "peek-first"); err != nil {
		t.Fatalf("name the other session's first pane: %v: %s", err, out)
	}
	for i := range 13 {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "other", "peek"+strconv.Itoa(i), "--no-focus"); err != nil {
			t.Fatalf("open the other session's pane %d: %v: %s", i, err, out)
		}
	}
	b, dir := leftRailBar("┃"), artifactDir(t)
	s := waitBarState(t, term, b, "terminals", "pane", "top", dir, "top")
	wheelAt(t, term, b.x0+5, b.header(s, "terminals")+1, tuitest.MouseWheelDown, 40)
	waitBarState(t, term, b, "terminals", "pane", "bottom", dir, "bottom")

	row := -1
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, rows := s.Size()
		for r := range rows {
			if strings.Contains(b.line(s, r), "other") {
				row = r
				return true
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("the sessions section never listed the other session:\n%s", term.Snapshot())
	}
	moveMouse(t, term, b.x0+5, row)
	waitBarState(t, term, b, "terminals", "peek", "bottom", dir, "peek")
	alive(t, term, "after peeking a scrolled terminals section")
}

// openPanes names the session's first pane and opens n more, so every row of
// its terminals section carries "pane".
func openPanes(t *testing.T, base, session string, n int) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "--name", "pane-first"); err != nil {
		t.Fatalf("name the first pane: %v: %s", err, out)
	}
	for i := range n {
		if out, err := tuiosCLI(t, base, "new-window", "-s", session, "pane"+strconv.Itoa(i), "--no-focus"); err != nil {
			t.Fatalf("open pane %d: %v: %s", i, err, out)
		}
	}
}

// railBarWalk wheels one section from its top to its bottom and back,
// asserting the thumb at each stop. cut says whether the section's rows are
// long enough to be cut, which is what the reservation is checked on.
func railBarWalk(t *testing.T, term *tuitest.Terminal, b railBar, header, rowMark string, cut bool) {
	t.Helper()
	dir := artifactDir(t)
	s := waitBarState(t, term, b, header, rowMark, "top", dir, "top")
	if cut {
		reserved(t, term, s, b, header, rowMark)
	}
	row := b.header(s, header) + 1
	col := b.x0 + 5
	wheelAt(t, term, col, row, tuitest.MouseWheelDown, 1)
	s = waitBarState(t, term, b, header, rowMark, "middle", dir, "middle")
	if cut {
		reserved(t, term, s, b, header, rowMark)
	}
	wheelAt(t, term, col, row, tuitest.MouseWheelDown, 40)
	s = waitBarState(t, term, b, header, rowMark, "bottom", dir, "bottom")
	if cut {
		reserved(t, term, s, b, header, rowMark)
	}
	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 40)
	waitBarState(t, term, b, header, rowMark, "top", dir, "top-again")
	alive(t, term, "after wheeling the "+header+" section")
}

// TestRailScrollbarAbsentWhenTheSectionFits: a rail whose sections all fit
// has no thumb. Shrinking the screen makes the terminals section overflow:
// it gets a thumb, and the sessions section beside it, which still fits,
// draws its row exactly as before. Growing the screen back takes the thumb
// away again.
func TestRailScrollbarAbsentWhenTheSectionFits(t *testing.T) {
	term, base := railClient(t, "fits", "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\nsections = \"sessions,terminals\"\n",
		startOpts{cols: 120, rows: 40})
	dir := artifactDir(t)
	b := leftRailBar("┃")
	openPanes(t, base, "fits", 7)
	noThumb := func(s tuitest.Screen) bool {
		_, rows := s.Size()
		for r := range rows {
			if s.Cell(b.col, r).Content == b.thumb {
				return false
			}
		}
		return true
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railRowOf(s, "pane6") >= 0 }, uiTimeout); err != nil {
		t.Fatalf("the terminals section never listed all eight panes: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	saveArtifact(t, term, dir, "fits")
	if !noThumb(s) {
		t.Fatalf("ASSERTION: a rail whose sections fit draws a thumb\n%s", term.Snapshot())
	}
	sessionRow := b.header(s, "sessions") + 1
	before := b.line(s, sessionRow)

	if err := term.Resize(120, 13); err != nil {
		t.Fatalf("shrink the screen: %v", err)
	}
	s = waitBarState(t, term, b, "terminals", "pane", "top", dir, "overflowing")
	if _, cells := b.cells(s, "sessions", "fits"); slices.Contains(cells, b.thumb) {
		t.Fatalf("ASSERTION: the sessions section fits and draws a thumb\n%s", term.Snapshot())
	}
	if after := b.line(s, sessionRow); after != before {
		t.Fatalf("ASSERTION: the sessions row changed when terminals overflowed:\n before %q\n after  %q\n%s", before, after, term.Snapshot())
	}

	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("grow the screen: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railRowOf(s, "pane6") >= 0 && noThumb(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the thumb stayed once the terminals section fit again: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "fits-again")
	alive(t, term, "after the terminals section fit again")
}

// TestRailScrollbarDepthsAndASCII: at 16 colours the thumb is still its
// glyph, and under --ascii-only it is the ascii glyph set's "|".
func TestRailScrollbarDepthsAndASCII(t *testing.T) {
	t.Run("16", func(t *testing.T) {
		term, _ := railClient(t, "bar", railScrollFeed("left"), startOpts{cols: 120, rows: 40, env: chromeDepths[0].env})
		railBarEnds(t, term, leftRailBar("┃"))
	})
	t.Run("ascii", func(t *testing.T) {
		term, _ := railClient(t, "bar", railScrollFeed("left"), startOpts{cols: 120, rows: 40, args: []string{"--ascii-only"}})
		railBarEnds(t, term, leftRailBar("|"))
	})
}

// railBarEnds asserts the top and the bottom of the custom section.
func railBarEnds(t *testing.T, term *tuitest.Terminal, b railBar) {
	t.Helper()
	dir := artifactDir(t)
	s := waitBarState(t, term, b, railScrollTitle, "FEED-", "top", dir, "top")
	wheelAt(t, term, b.x0+5, b.header(s, railScrollTitle)+1, tuitest.MouseWheelDown, 40)
	waitBarState(t, term, b, railScrollTitle, "FEED-", "bottom", dir, "bottom")
	alive(t, term, "after wheeling the custom section")
}
