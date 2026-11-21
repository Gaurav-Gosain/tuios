package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	bigCols, bigRows     = 120, 40
	smallCols, smallRows = 60, 20
)

func paneSpanRight(s tuitest.Screen) int {
	cols, rows := s.Size()
	widest := 0
	for r := range rows {
		line := []rune(s.Line(r))
		for c := min(len(line), cols) - 1; c >= 0; c-- {
			if isWindowBorder(line[c]) {
				if c+1 > widest {
					widest = c + 1
				}
				break
			}
		}
	}
	return widest
}

func isWindowBorder(r rune) bool {
	switch r {
	case '│', '╭', '╮', '╯', '╰', '┃', '║', '┌', '┐', '└', '┘':
		return true
	}
	return false
}

func paneSpanBottom(s tuitest.Screen) int {
	_, rows := s.Size()
	last := 0
	for r := range rows {
		for _, ch := range s.Line(r) {
			if isWindowBorder(ch) {
				last = r + 1
				break
			}
		}
	}
	return last
}

func attachSmall(t *testing.T, base, session string, o startOpts) *tuitest.Terminal {
	t.Helper()
	o.args = append(append([]string{}, o.args...), "attach", session)
	term := startIn(t, base, o)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneSpanRight(s) > 0 || strings.Contains(s.Text(), welcomeText)
	}, bootTimeout); err != nil {
		t.Fatalf("small client never rehydrated %q: %v\n%s", session, err, term.Snapshot())
	}
	return term
}

func twoClientSession(t *testing.T, name string, cols, rows int) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[daemon]\nwindow_size = \"smallest\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create session %s: %v: %s", name, err, out)
	}
	return attachIn(t, base, name, startOpts{cols: cols, rows: rows}), base
}

func waitSpanRight(t *testing.T, term *tuitest.Terminal, want int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneSpanRight(s) == want
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the panes span %d columns, want %d\n%s",
			what, paneSpanRight(term.Screen()), want, term.Snapshot())
	}
}

// TestSessionHoldsOneSizeForTwoClients is the whole of the report, on screen.
//
// A wide client and a narrow one attach to one session. The session's size is
// the minimum over them, so the wide client letterboxes: it draws its panes
// into the narrow client's columns and leaves the rest alone. Interacting must
// not move that. When the narrow client leaves, the columns come back.
//
// NEGATIVE CONTROL: the last stage fails on the unfixed tree. Captured there,
// after the narrow client left, the dock and the separator redrew across all
// 120 columns while the two panes stayed 28 wide in the top-left corner, with
// 20 rows of dead space under them: the departing client's last state sync
// carried its own layout and nothing pulled it back out. That is the
// "the borders don't come back" report.
func TestSessionHoldsOneSizeForTwoClients(t *testing.T) {
	big, base := twoClientSession(t, "shared", bigCols, bigRows)
	newWindow(t, big)
	waitWindowCount(t, big, 2, "two windows on the big client")
	enableTiling(t, big)

	waitSpanRight(t, big, bigCols, "before the narrow client attaches")
	if got := paneSpanBottom(big.Screen()); got < bigRows-config_dockRows {
		t.Fatalf("the panes are only %d rows tall on a %d-row client before anything "+
			"happens; the fixture is not tiled and nothing below would prove anything\n%s",
			got, bigRows, big.Snapshot())
	}

	small := attachSmall(t, base, "shared", startOpts{cols: smallCols, rows: smallRows})
	waitSpanRight(t, big, smallCols, "after the narrow client attached")
	waitSpanRight(t, small, smallCols, "the narrow client's own frame")

	for range 6 {
		if err := big.SendKeys("\t"); err != nil {
			t.Fatalf("focus cycle: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Second)
	if got := paneSpanRight(big.Screen()); got != smallCols {
		t.Errorf("after interacting the wide client's panes span %d columns, want %d: "+
			"typing and focusing re-laid the session out\n%s", got, smallCols, big.Snapshot())
	}
	if got := paneSpanRight(small.Screen()); got != smallCols {
		t.Errorf("the narrow client's panes span %d columns after the other client "+
			"interacted, want %d\n%s", got, smallCols, small.Snapshot())
	}

	if err := small.Close(); err != nil {
		t.Fatalf("close the narrow client: %v", err)
	}
	if err := big.WaitFor(func(s tuitest.Screen) bool {
		return paneSpanRight(s) == bigCols
	}, 20*time.Second); err != nil {
		t.Fatalf("the narrow client left but the wide client's panes still span %d "+
			"columns, want %d: the panes and their borders are inside an edge that has "+
			"moved back out\n%s", paneSpanRight(big.Screen()), bigCols, big.Snapshot())
	}
}

const config_dockRows = 4
