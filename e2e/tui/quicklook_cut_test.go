package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// quickLookCutOpen writes zzcut.log into its own listing of the quick look
// fixture, opens it with space and goes inside with tab. A .log does not wrap
// when it opens. Its line 1 is a Latin letter with U+16FF0 a hundred times,
// then ten digits: each pair is one cell on screen and three where a line is
// cut, so a prefix of the line is far narrower than its bytes suggest. Lines
// 2 to 100 are short, so the gutter takes 3 digits and the code column is 81
// cells.
func quickLookCutOpen(t *testing.T) *tuitest.Terminal {
	t.Helper()
	var text strings.Builder
	text.WriteString(strings.Repeat("q\U00016FF0", 100))
	text.WriteString("0123456789\n")
	for i := 2; i <= 100; i++ {
		fmt.Fprintf(&text, "end %d\n", i)
	}
	term, dir := quickLookListed(t, []string{"TUIOS_KITTY_GRAPHICS=0"}, nil)
	if err := os.WriteFile(filepath.Join(dir, "zzcut.log"), []byte(text.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	railLists(t, term, "the rail did not list zzcut.log", []string{"zzcut.log"}, nil)
	quickLookFocusRail(t, term, "zzcut.log")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show zzcut.log", []string{"zzcut.log ", "go inside"}, nil)
	sendKeys(t, term, tuitest.Tab)
	railLists(t, term, "tab did not go inside zzcut.log", []string{"Inside the file"}, nil)
	return term
}

// TestQuickLookCutKeepsTheEllipsis cuts lines longer than the code column
// with wrap off, and each cut row shows the ellipsis that says the line goes
// on. The lines are ones whose characters are narrower on screen than where a
// line is cut: a letter with a halfwidth sound mark, and a letter with
// U+16FF0. A row drawn from too short a part of such a line fits the column
// and loses its ellipsis. Line 1 of zzwrap.txt, wide characters, keeps its
// ellipsis too, and the short line 4 shows whole.
//
// How this could pass wrongly: the ellipsis can come early, before blanks at
// the row's end, so the test looks for it anywhere on the row, not in the
// last cell.
func TestQuickLookCutKeepsTheEllipsis(t *testing.T) {
	// rowText is the text of a quick look row with its blanks at the
	// end dropped. The second cell of a wide character adds nothing.
	rowText := func(row []tuitest.Cell) string {
		var b strings.Builder
		for _, c := range row {
			if c.Width != 0 {
				b.WriteString(c.Content)
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	term, _ := quickLookWrapOpen(t, "zzwrap.txt")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return quickLookLineRows(s, 3) != nil }, uiTimeout); err != nil {
		t.Fatalf("lines 1 to 3 are not on screen: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "w")
	// Wrap off: lines 1 and 2 take one row each, and line 4 is on screen.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return len(quickLookLineRows(s, 1)) == 1 && len(quickLookLineRows(s, 2)) == 1 && len(quickLookLineRows(s, 4)) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("w did not cut lines 1 and 2 to one row each: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "quick-look-cut-marks")
	var one, two, four string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		one, two, four = "", "", ""
		if rows := quickLookLineRows(s, 1); len(rows) == 1 {
			one = rowText(rows[0])
		}
		if rows := quickLookLineRows(s, 2); len(rows) == 1 {
			two = rowText(rows[0])
		}
		if rows := quickLookLineRows(s, 4); len(rows) == 1 {
			four = rowText(rows[0])
		}
		return strings.Contains(one, "…") && strings.Contains(two, "…") && four == "end 4"
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: with wrap off line 1's row is %q and line 2's row is %q, want each to hold \"…\"; line 4's row is %q, want \"end 4\": %v\n%s",
			one, two, four, err, term.Snapshot())
	}

	term = quickLookCutOpen(t)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return len(quickLookLineRows(s, 1)) == 1 }, uiTimeout); err != nil {
		t.Fatalf("line 1 of zzcut.log is not on one row: %v\n%s", err, term.Snapshot())
	}
	copiesFrame(t, term, "quick-look-cut-spacing-marks")
	var row string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		row = ""
		if rows := quickLookLineRows(s, 1); len(rows) == 1 {
			row = rowText(rows[0])
		}
		return strings.Contains(row, "…")
	}, uiTimeout); err != nil {
		t.Errorf("ASSERTION: with wrap off line 1's row of zzcut.log is %q, want it to hold \"…\": %v\n%s", row, err, term.Snapshot())
	}
}
