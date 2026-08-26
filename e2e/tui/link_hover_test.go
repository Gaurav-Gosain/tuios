package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Link hover is the first thing in tuios drawn onto pane content from a pointer
// position, and the unit tests for it assert on renderTerminal's output. This
// asserts on the host's own grid, through a real PTY, because that is the level
// at which the recurring failure in this codebase lives: internal state and
// pixels disagreeing.
//
// It is also the only test that exercises the motion filter, the hover
// resolver, the cell loop and the compositor as one thing. Any of the four
// dropping the event produces the same visible result, which is the point.

// linkURL is printed as plain text, never through OSC 8, so this covers the
// case that matters in practice: a URL a program printed with no idea that
// anything would ever want to click it.
const linkURL = "https://example.com/e2e-link"

// findOnScreen returns the column and row of the first cell of want, or
// ok=false. It matches per physical row, so a URL that soft-wrapped is not
// found; the terminal is wide enough that it cannot.
func findOnScreen(s tuitest.Screen, want string) (col, row int, ok bool) {
	cols, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		i := strings.Index(line, want)
		if i < 0 {
			continue
		}
		if i+len(want) > cols {
			continue
		}
		return i, r, true
	}
	return 0, 0, false
}

// cellUnderlined reports whether the cell at (col, row) is drawn underlined.
func cellUnderlined(s tuitest.Screen, col, row int) bool {
	return s.Cell(col, row).Underline
}

// TestHoveringALinkUnderlinesItOnScreen.
//
// Negative control, NOT YET RUN. The two controls to build are a binary with
// the links clause removed from filterMouseMotion in cmd/tuios/run.go, and one
// with the highlight branch removed from the cell loop in renderTerminal. Both
// should fail at the first WaitFor, with the URL on screen and no cell
// underlined. This has to be confirmed and recorded in NEGATIVE_CONTROLS.md
// before the test counts as evidence.
func TestHoveringALinkUnderlinesItOnScreen(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the link")

	enterTerminalMode(t, term)
	runInShell(t, term, "printf 'go to %s now\\n' "+linkURL, linkURL, uiTimeout)

	// Back to window management, so nothing about this depends on what the
	// shell would have done with the click. A shell tracks no mouse either way;
	// the mode is pinned so the test says which one it means.
	leaveTerminalMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}

	snap := term.Snapshot()
	col, row, ok := findOnScreen(snap, linkURL)
	if !ok {
		t.Fatalf("the URL is not on screen to hover:\n%s", snap)
	}

	// Nothing is underlined before the pointer arrives, so the assertion below
	// cannot be satisfied by styling the shell already had.
	if cellUnderlined(snap, col, row) {
		t.Fatalf("the URL was already underlined with no pointer on it:\n%s", snap)
	}

	// The middle of the URL, well away from either end, so an off-by-one in the
	// run's bounds does not decide the result.
	mid := col + len(linkURL)/2
	mouseHover(t, term, mid, row)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return cellUnderlined(s, mid, row)
	}, uiTimeout); err != nil {
		t.Fatalf("the hovered link never underlined itself: %v\n%s", err, term.Snapshot())
	}

	// The whole run, not only the cell under the pointer. This is what
	// distinguishes a link highlight from a cursor.
	after := term.Snapshot()
	for c := col; c < col+len(linkURL); c++ {
		if !cellUnderlined(after, c, row) {
			t.Errorf("column %d of the run is not underlined; the run is %d..%d",
				c, col, col+len(linkURL)-1)
		}
	}

	// And the text around it is not. An underline that ran to the end of the
	// line would satisfy every assertion above.
	if col > 0 && cellUnderlined(after, col-1, row) {
		t.Error("the cell before the URL is underlined")
	}
	if end := col + len(linkURL); end < 200 && cellUnderlined(after, end, row) {
		t.Error("the cell after the URL is underlined")
	}

	// The pointer leaving takes the underline with it. Without the trailing
	// event LinkHoverActive keeps flowing, nothing downstream is ever told the
	// pointer left and the highlight outlives it.
	mouseHover(t, term, mid, row+2)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !cellUnderlined(s, mid, row)
	}, uiTimeout); err != nil {
		t.Fatalf("the underline outlived the pointer: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after hovering a link")
}

// TestPlainTextIsNotUnderlined is the false-positive guard, at the level the
// user sees it. The scanner is deliberately narrow because underlining ordinary
// prose and then offering to open it is worse than missing a link, and this
// checks that on the host's own grid rather than on the scanner's unit table.
func TestPlainTextIsNotUnderlined(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell")

	enterTerminalMode(t, term)
	// A sentence with a scheme-shaped word, a bare host name and a path in it.
	// None of the three is a link, and all three have caught naive detectors.
	const prose = "the https protocol on example.com under /usr/local"
	runInShell(t, term, "printf '%s\\n' "+quoteForShell(prose), "protocol on", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}

	snap := term.Snapshot()
	col, row, ok := findOnScreen(snap, prose)
	if !ok {
		t.Fatalf("the sentence is not on screen:\n%s", snap)
	}

	for c := col; c < col+len(prose); c++ {
		mouseHover(t, term, c, row)
		// A hover that finds nothing produces no frame of its own, so this
		// samples rather than waits: there is nothing to wait for, and the
		// failure being guarded is a highlight appearing, not one being late.
		time.Sleep(5 * time.Millisecond)
		if cellUnderlined(term.Snapshot(), c, row) {
			t.Fatalf("column %d of %q was underlined as a link", c-col, prose)
		}
	}

	alive(t, term, "after hovering plain text")
}

// quoteForShell wraps a string in single quotes for the shell the harness
// drives. The strings here hold no single quotes, so the simple form is enough
// and a helper that pretended otherwise would be untested code.
func quoteForShell(s string) string {
	if strings.Contains(s, "'") {
		panic("quoteForShell was given a string with a quote in it")
	}
	return "'" + s + "'"
}
