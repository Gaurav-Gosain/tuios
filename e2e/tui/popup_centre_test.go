package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// popupBox finds the popup whose row holds title: the run of cells around the
// title with the title's ground, across and down. It returns the box's
// first and last column and row.
func popupBox(t *testing.T, s tuitest.Screen, w, h int, title string) (x0, y0, x1, y1 int) {
	t.Helper()
	// The title is found by cells, not by the text's runes: a wide glyph on
	// the rail would shift a rune index off its column.
	row, col := -1, -1
	first := []rune(title)[0]
	for y := 0; y < h && row < 0; y++ {
		for x := 0; x+len(title) <= w; x++ {
			if s.Cell(x, y).Rune != first {
				continue
			}
			var b strings.Builder
			for i := 0; i < len(title); i++ {
				b.WriteString(s.Cell(x+i, y).Content)
			}
			if b.String() == title {
				row, col = y, x
				break
			}
		}
	}
	if row < 0 {
		t.Fatalf("no row holds %q:\n%s", title, s.Text())
	}
	// The panel's ground is read on the row under the title, which is the
	// panel's own blank ground in both popups: the title itself may sit on
	// an accent label of its own.
	gy := row + 1
	bg := s.Cell(col, gy).Bg
	x0, x1 = col, col
	for x0 > 0 && s.Cell(x0-1, gy).Bg == bg {
		x0--
	}
	for x1 < w-1 && s.Cell(x1+1, gy).Bg == bg {
		x1++
	}
	y0, y1 = gy, gy
	for y0 > 0 && s.Cell(x0, y0-1).Bg == bg {
		y0--
	}
	for y1 < h-1 && s.Cell(x0, y1+1).Bg == bg {
		y1++
	}
	return x0, y0, x1, y1
}

// TestPopupsShareTheScreenCentre opens the Add a machine sheet and the command
// palette with the rail open, and checks that both are centred on the whole
// screen. Both were centred in the columns beside the rail, half the rail's
// width right of the screen centre the modal dialogs use.
//
// Negative control: on origin/main the sheet's centre is 14 columns right
// of the screen centre at this size, and the palette's is too.
func TestPopupsShareTheScreenCentre(t *testing.T) {
	const cols, rows = 160, 48
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "centre", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	// Enough terminals that the rail's rows run down into the band the
	// popups cover, so the frames show how a covered rail row reads.
	for range 7 {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "centre"); err != nil {
			t.Fatalf("open a window: %v\n%s", err, out)
		}
	}
	term := startIn(t, base, startOpts{cols: cols, rows: rows, args: []string{"attach", "centre"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 8 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}

	check := func(name, title string) {
		t.Helper()
		time.Sleep(insertGuard)
		s := term.Screen()
		railShot(t, term, name)
		x0, y0, x1, y1 := popupBox(t, s, cols, rows, title)
		// Doubled, so a box of odd size compares exactly.
		cx, cy := x0+x1, y0+y1
		t.Logf("%s: box %d,%d to %d,%d, centre %.1f,%.1f on a %dx%d screen", name, x0, y0, x1, y1, float64(cx)/2, float64(cy)/2, cols, rows)
		if d := cx - (cols - 1); d < -2 || d > 2 {
			t.Errorf("ASSERTION: %s is not centred across the screen: its centre is column %.1f, the screen's %.1f", name, float64(cx)/2, float64(cols-1)/2)
		}
		if d := cy - (rows - 1); d < -2 || d > 2 {
			t.Errorf("ASSERTION: %s is not centred down the screen: its centre is row %.1f, the screen's %.1f", name, float64(cy)/2, float64(rows-1)/2)
		}
	}

	openAddSheet(t, term)
	if err := term.WaitForText("user@host", uiTimeout); err != nil {
		t.Fatalf("the sheet never opened: %v\n%s", err, term.Snapshot())
	}
	check("machine-sheet", "Add a machine")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "user@host") }, uiTimeout); err != nil {
		t.Fatalf("the sheet never closed: %v\n%s", err, term.Snapshot())
	}

	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "for the centre check")
	check("palette", paletteTitle)
}
