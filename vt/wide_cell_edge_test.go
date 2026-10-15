package vt

import (
	"testing"
)

// TestNarrowingResizeBlanksTheWideRuneItCuts pins the grid invariant a reader
// depends on: no cell in the last column is wider than the one column it has.
//
// Narrowing drops columns from the right. When the column it drops is the
// continuation half of a double-width rune, the lead is left in the last column
// with nothing to spill into, and every reader that walks cells draws it whole
// and returns a row one cell wider than the screen it came from. Blanking the
// half left standing is what the insert and delete paths already do to a rune
// they cut, and what ghostty does.
func TestNarrowingResizeBlanksTheWideRuneItCuts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		from, to   int
		wantLast   string
		wantRowSum int // display cells the row is worth
	}{
		// The cut cases: the new last column held a lead.
		{"6 to 5", 6, 5, " ", 4},
		{"6 to 3", 6, 3, " ", 2},
		{"8 to 3", 8, 3, " ", 2},
		{"20 to 3", 20, 3, " ", 2},
		// The clean cases: the new edge falls between two runes, so nothing is
		// cut and the rune on the edge stays.
		{"6 to 4", 6, 4, "", 4},
		{"6 to 6", 6, 6, "", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(tc.from, 3)
			e.WriteString("世世世")
			e.Resize(tc.to, 3)

			last := e.CellAt(e.Width()-1, 0)
			if last == nil {
				t.Fatalf("the last column of a %d-wide screen has no cell", e.Width())
			}
			if last.Width > 1 {
				t.Errorf("last column holds %q, %d cells wide, in the one column it has",
					last.Content, last.Width)
			}
			if tc.wantLast != "" && last.Content != tc.wantLast {
				t.Errorf("last column is %q, want %q", last.Content, tc.wantLast)
			}

			cells := 0
			for x := range e.Width() {
				if c := e.CellAt(x, 0); c != nil && c.Content != " " {
					cells += c.Width
				}
			}
			if cells != tc.wantRowSum {
				t.Errorf("row is worth %d display cells, want %d", cells, tc.wantRowSum)
			}
			if cells > e.Width() {
				t.Errorf("row is worth %d display cells on a %d-wide screen", cells, e.Width())
			}
		})
	}
}
