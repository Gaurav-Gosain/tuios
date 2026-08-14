package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestNarrowedPaneKeepsItsWideRunesInsideItsBox is the regression test for the
// guest's text appearing outside the pane that wrote it.
//
// A pane whose guest had filled a row with double-width runes was narrowed by
// the tiler when another pane appeared. Dropping a column takes away the
// continuation cell the rune on the edge needs and leaves the lead behind, so
// the pane rendered a row one cell wider than its own box and the compositor
// laid that cell over whatever was next to it. The frame showed half a rune the
// guest had written somewhere else, which is the "random text" a user reports.
//
// The assertion is on the composed frame as well as on the pane's own render,
// because the pane's render looks reasonable until it is measured against the
// box it has to fit.
func TestNarrowedPaneKeepsItsWideRunesInsideItsBox(t *testing.T) {
	m := gapTestOS(t, 2)
	m.UseBSPLayout = true
	m.TileAllWindows()

	// A full row of wide runes, so whichever column the tiler cuts at lands on
	// one of them.
	filled := m.Windows[0]
	filled.LockIO()
	_, _ = filled.Terminal.Write([]byte("\x1b[H\x1b[2J" + strings.Repeat("世", filled.ContentWidth()/2)))
	filled.UnlockIO()
	filled.MarkContentDirty()
	filled.CachedContent = ""

	// One column back, which is what the tiler hands a pane when the region it
	// is splitting is odd or a neighbour appears beside it.
	filled.Resize(filled.Width-1, filled.Height)
	for _, w := range m.Windows {
		w.MarkContentDirty()
		w.CachedContent = ""
	}

	for _, w := range m.Windows {
		if got, want := lipgloss.Width(m.renderTerminal(w, false, false)), w.ContentWidth(); got > want {
			t.Errorf("%s renders %d cells into a %d-cell box", w.ID, got, want)
		}
	}

	rows := strings.Split(lipgloss.Sprint(m.GetCanvas(true).Render()), "\n")
	y := filled.Y + filled.BorderOffset()
	if y >= len(rows) {
		t.Fatalf("the filled pane's first row %d is past the %d-row frame", y, len(rows))
	}
	cols := rowColumns(ansi.Strip(rows[y]))
	for x := filled.X + filled.Width; x < len(cols); x++ {
		if cols[x] == "世" {
			t.Fatalf("frame row %d carries the pane's wide rune at column %d, outside its box [%d,%d)",
				y, x, filled.X, filled.X+filled.Width)
		}
	}
}

// rowColumns splits a rendered row into one entry per display column, so a
// column index means the same thing it does to the compositor. Indexing the
// runes instead counts a double-width rune once and reports every later column
// one cell to the left of where it is drawn.
func rowColumns(row string) []string {
	var cols []string
	for _, r := range row {
		w := ansi.StringWidth(string(r))
		if w <= 0 {
			continue
		}
		cols = append(cols, string(r))
		for range w - 1 {
			cols = append(cols, "")
		}
	}
	return cols
}
