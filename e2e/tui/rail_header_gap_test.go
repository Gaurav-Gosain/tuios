package tuie2e

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailHeaderKeepsAGapBeforeThePeekedName hovers a session with a long name
// on a narrow rail. The terminals header then names the peeked session on its
// right, beside the add control. The header must keep at least two blank cells
// between "terminals" and whatever follows it, and the add control must still
// make a pane when clicked at the column it is drawn in.
//
// The rail widths are the narrowest one that draws section headers (16) and a
// few above it. The old layout sized the name from half the rail, which ignored
// the label, and at 20 drew "terminalszebra-… +" with the control pushed one
// cell off its spine.
//
// Negative controls are in NEGATIVE_CONTROLS.md: the build before the fix,
// sidebarHeaderGap at 1, and the name sized from half the rail again.
func TestRailHeaderKeepsAGapBeforeThePeekedName(t *testing.T) {
	for _, width := range []int{16, 18, 20, 24} {
		t.Run("width-"+strconv.Itoa(width), func(t *testing.T) {
			railHeaderGapAt(t, width)
		})
	}
}

const longPeekSession = "zebra-session-with-a-very-long-name"

func railHeaderGapAt(t *testing.T, width int) {
	term, base := railClient(t, "e2e", railConfig(width), startOpts{cols: 120, rows: 30})
	renameWindow(t, term, "HOME")
	if out, err := tuiosCLI(t, base, "new", longPeekSession, "--detach"); err != nil {
		t.Fatalf("create %s: %v: %s", longPeekSession, err, out)
	}

	line := func(s tuitest.Screen, r int) string {
		runes := []rune(s.Line(r))
		if len(runes) > width {
			runes = runes[:width]
		}
		return string(runes)
	}
	rowOf := func(s tuitest.Screen, needle string) int {
		_, rows := s.Size()
		for r := range rows {
			if strings.Contains(line(s, r), needle) {
				return r
			}
		}
		return -1
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return rowOf(s, "zeb") >= 0 && rowOf(s, "HOME") > rowOf(s, "terminals")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never listed the long session and this session's pane: %v\n%s", err, term.Snapshot())
	}
	sessRow := rowOf(term.Screen(), "zeb")
	mouseHover(t, term, strings.Index(line(term.Screen(), sessRow), "zeb")+1, sessRow)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		h := rowOf(s, "terminals")
		return h >= 0 && rowOf(s, "HOME") < 0
	}, uiTimeout); err != nil {
		t.Fatalf("hovering the long session did not peek it: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	header := rowOf(s, "terminals")
	text := line(s, header)
	saveFrame(t, term, "railgap-"+strconv.Itoa(width)+"-peek")

	after := []rune(text[strings.Index(text, "terminals")+len("terminals"):])
	rest := strings.TrimLeft(string(after), " ")
	if rest != "" {
		if gap := len(after) - len([]rune(rest)); gap < 2 {
			t.Fatalf("width %d: %d blank cells between the label and the peeked name, want at least 2: %q\n%s",
				width, gap, text, term.Snapshot())
		}
	}
	if width >= 20 && !strings.Contains(rest, "ze") {
		t.Fatalf("width %d: the header does not name the peeked session: %q\n%s", width, text, term.Snapshot())
	}
	addCol := strings.LastIndex(text, "+")
	if addCol < 0 {
		t.Fatalf("width %d: the terminals header has no add control: %q\n%s", width, text, term.Snapshot())
	}
	if want := width - 3; len([]rune(text[:addCol])) != want {
		t.Fatalf("width %d: the add control is at column %d, want %d: %q",
			width, len([]rune(text[:addCol])), want, text)
	}

	col := len([]rune(text[:addCol]))
	mouseHover(t, term, col, header)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return rowOf(s, "HOME") > rowOf(s, "terminals")
	}, uiTimeout); err != nil {
		t.Fatalf("moving onto the add control did not end the peek: %v\n%s", err, term.Snapshot())
	}
	mouseClick(t, term, col, header, tuitest.MouseLeft, 0)
	waitWindowCount(t, term, 2, "after clicking the terminals add control")
	saveFrame(t, term, "railgap-"+strconv.Itoa(width)+"-added")
	alive(t, term, "after clicking the terminals add control")
}
