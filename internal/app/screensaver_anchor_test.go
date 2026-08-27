package app

import (
	"strings"
	"testing"

	tfx "github.com/Gaurav-Gosain/tuiffects"
)

// The saver must start without moving the screen.
//
// The engine places a capture by anchor: it measures the block the capture
// fills and slides that block to a corner. A screen is not a block. Its top
// rows are often empty, so the block is shorter than the screen, and any anchor
// that pins the block's top edge lifts the whole picture by the difference.
// What the user sees is the screen jumping upwards the moment the saver starts.
//
// These tests measure that jump on real composed screens and require it to be
// zero. The measurement is the offset at which the most captured glyphs line
// up, so a frame that is merely close scores its real displacement instead of
// passing.
//
// Negative control: put AnchorNW back in screensaverBuild and
// TestSaverDoesNotMoveAScreenWithEmptyTopRows reports offset row -3. Drop the
// screensaverFit call and TestSaverKeepsTheTopLeftOnATallerCanvas reports
// offset row +4, TestSaverKeepsTheTopLeftOnAShorterCanvas offset row -4. All
// three compile and all three fail on the number.

// The effect these tests run. highlight is the one effect that never moves a
// character and never hides one, so its first frame is the captured screen and
// any displacement in it is placement, not animation. See effectOpenings.
const anchorTestEffect = "highlight"

// anchorFrame is a rendered frame as plain symbols, one string per row.
func anchorFrame(engine *tfx.Engine) []string {
	rows := engine.FrameRows()
	out := make([]string, len(rows))
	for y, row := range rows {
		var b strings.Builder
		for _, cell := range row {
			if cell == nil || cell.Symbol == "" {
				b.WriteString(" ")
				continue
			}
			b.WriteString(cell.Symbol)
		}
		out[y] = b.String()
	}
	return out
}

// anchorCapture is a capture as plain symbols, one string per row.
func anchorCapture(capture [][]tfx.InputCell) []string {
	out := make([]string, len(capture))
	for y, row := range capture {
		var b strings.Builder
		for _, cell := range row {
			if cell.Symbol == "" {
				b.WriteString(" ")
				continue
			}
			b.WriteString(cell.Symbol)
		}
		out[y] = b.String()
	}
	return out
}

// anchorGlyphs counts the cells of a capture that hold a glyph.
func anchorGlyphs(want []string) int {
	glyphs := 0
	for _, line := range want {
		for _, r := range line {
			if r != ' ' {
				glyphs++
			}
		}
	}
	return glyphs
}

// anchorScore counts the captured glyphs a frame shows at a given shift, and
// how many were in reach of it at all.
func anchorScore(want, got []string, dy, dx int) (match, total int) {
	for y, line := range want {
		gy := y + dy
		if gy < 0 || gy >= len(got) {
			continue
		}
		row := []rune(got[gy])
		for x, r := range []rune(line) {
			if r == ' ' {
				continue
			}
			gx := x + dx
			if gx < 0 || gx >= len(row) {
				continue
			}
			total++
			if row[gx] == r {
				match++
			}
		}
	}
	return match, total
}

// anchorOffset is the shift at which the frame best reproduces the capture, and
// the share of the capture's glyphs that land there. A frame drawn where it was
// captured scores (0, 0) with every glyph.
func anchorOffset(want, got []string) (dy, dx, match, total int) {
	const reach = 6
	best := -1
	for y := -reach; y <= reach; y++ {
		for x := -reach; x <= reach; x++ {
			m, t := anchorScore(want, got, y, x)
			if m > best {
				best, dy, dx, match, total = m, y, x, m, t
			}
		}
	}
	return dy, dx, match, total
}

// requireNoOffset builds the effect over a capture and fails unless the first
// frame reproduces it exactly where it was captured.
func requireNoOffset(t *testing.T, capture [][]tfx.InputCell, width, height int) {
	t.Helper()
	d, ok := tfx.Lookup(anchorTestEffect)
	if !ok {
		t.Fatalf("the engine no longer has %s; pick another effect with keepsScreen", anchorTestEffect)
	}
	engine, ok := screensaverBuild(capture, width, height, d.New(), d.NeedsFillCharacters)
	if !ok {
		t.Fatal("the effect would not build over the capture")
	}
	got := anchorFrame(engine)
	if len(got) != height {
		t.Fatalf("frame is %d rows over a canvas of %d", len(got), height)
	}
	// A canvas too short to hold the capture has to lose rows, and the ones it
	// is allowed to lose are at the bottom.
	want := anchorCapture(capture)
	if len(want) > height {
		want = want[:height]
	}
	dy, dx, match, total := anchorOffset(want, got)
	glyphs := anchorGlyphs(want)
	if dy != 0 || dx != 0 || match != glyphs {
		t.Errorf("the saver drew the screen at offset row %+d column %+d, where %d of the %d glyphs in reach matched, out of %d captured",
			dy, dx, match, total, glyphs)
		t.Logf("captured screen:\n%s", strings.Join(want, "\n"))
		t.Logf("first frame:\n%s", strings.Join(got, "\n"))
	}
}

// anchorTestOS composes a real screen at the given size. The window is floating
// at (x, y), so a y above zero leaves the top rows of the capture empty, which
// is the shape the bug needs.
func anchorTestOS(t *testing.T, id string, cols, rows, x, y int) *OS {
	t.Helper()
	win := newTestWindow(t, id, cols-2*x, rows-y-5)
	m := newTestOS(win)
	m.Width, m.Height = cols, rows
	m.EffectiveWidth, m.EffectiveHeight = cols, rows
	if x != 0 || y != 0 {
		win.IsFloating = true
		win.X, win.Y = x, y
		win.Width, win.Height = cols-2*x, rows-y-5
	}
	win.LockIO()
	measureReferenceScreen(t, win.Terminal)
	win.UnlockIO()
	win.MarkContentDirty()
	return m
}

// anchorTestCapture composes the screen and hands back the capture the saver
// would take of it.
func anchorTestCapture(t *testing.T, m *OS, cols, rows int) [][]tfx.InputCell {
	t.Helper()
	grid := m.composedGrid(0, 0, cols, rows)
	if grid == nil {
		t.Fatal("there was no composed screen to capture")
	}
	if grid.Cols != cols || grid.Rows != rows {
		t.Fatalf("composed %dx%d for a %dx%d screen", grid.Cols, grid.Rows, cols, rows)
	}
	return screensaverCells(grid)
}

// anchorLeadingEmptyRows counts the rows at the top of a capture that hold no
// glyph and no background, which are the rows the engine places nothing in.
func anchorLeadingEmptyRows(capture [][]tfx.InputCell) int {
	for y, row := range capture {
		for _, cell := range row {
			if (cell.Symbol != "" && cell.Symbol != " ") || cell.HasBg {
				return y
			}
		}
	}
	return len(capture)
}

// TestSaverDoesNotMoveAScreenWithEmptyTopRows is the regression test. A window
// floating three rows down leaves three empty rows at the top of the capture,
// and the saver used to start by lifting the screen into them.
func TestSaverDoesNotMoveAScreenWithEmptyTopRows(t *testing.T) {
	const cols, rows = 100, 30
	m := anchorTestOS(t, "anchor-0001", cols, rows, 4, 3)
	capture := anchorTestCapture(t, m, cols, rows)
	if empty := anchorLeadingEmptyRows(capture); empty != 3 {
		t.Fatalf("this screen needs empty top rows to test anything, and it has %d", empty)
	}
	requireNoOffset(t, capture, cols, rows)
}

// TestSaverDoesNotMoveAScreenThatStartsAtTheTop is the other half. A screen
// whose first row is already full has nothing for an anchor to take up, so it
// passed before the fix and must go on passing after it.
func TestSaverDoesNotMoveAScreenThatStartsAtTheTop(t *testing.T) {
	const cols, rows = 100, 30
	m := anchorTestOS(t, "anchor-0002", cols, rows, 0, 0)
	capture := anchorTestCapture(t, m, cols, rows)
	if empty := anchorLeadingEmptyRows(capture); empty != 0 {
		t.Fatalf("this screen was meant to start at row 0, and it starts at row %d", empty)
	}
	requireNoOffset(t, capture, cols, rows)
}

// TestSaverKeepsTheTopLeftOnATallerCanvas covers the case the anchor was
// originally chosen for.
//
// A canvas bigger than the capture has to put the screen in one corner, and it
// belongs in the top left one it was drawn from. The engine reads a capture
// from the bottom up, so on its own it would rest the screen on the floor;
// screensaverFit is what holds it against the ceiling instead. The capture here
// also has empty top rows, so nothing passes this by lifting the screen.
func TestSaverKeepsTheTopLeftOnATallerCanvas(t *testing.T) {
	const cols, rows = 100, 30
	m := anchorTestOS(t, "anchor-0003", cols, rows, 4, 3)
	capture := anchorTestCapture(t, m, cols, rows)
	requireNoOffset(t, capture, cols+6, rows+4)
}

// TestSaverKeepsTheTopLeftOnAShorterCanvas is the same argument downwards. A
// canvas too small to hold the capture has to lose rows, and it loses them off
// the bottom, because the top left is the corner the screen was drawn from.
func TestSaverKeepsTheTopLeftOnAShorterCanvas(t *testing.T) {
	const cols, rows = 100, 30
	m := anchorTestOS(t, "anchor-0004", cols, rows, 4, 3)
	capture := anchorTestCapture(t, m, cols, rows)
	requireNoOffset(t, capture, cols, rows-4)
}

// TestEffectPreviewDoesNotMoveTheScreen holds the picker to the same rule.
//
// The preview animates the screen the picker was opened over, drawn under the
// panel, so a preview that sits three rows off its own background is the same
// fault the saver had. It shares screensaverBuild, and this is what says so.
func TestEffectPreviewDoesNotMoveTheScreen(t *testing.T) {
	const cols, rows = 100, 30
	m := anchorTestOS(t, "anchor-0005", cols, rows, 4, 3)

	m.OpenEffectPicker()
	p := &m.effectPreview
	if p.capture == nil {
		t.Fatal("the picker opened without capturing a screen")
	}
	if empty := anchorLeadingEmptyRows(p.capture); empty != 3 {
		t.Fatalf("this screen needs empty top rows to test anything, and it has %d", empty)
	}

	items := m.effectPickerItems()
	m.EffectPickerSelected = -1
	for i, id := range items {
		if id == anchorTestEffect {
			m.EffectPickerSelected = i
			break
		}
	}
	if m.EffectPickerSelected < 0 {
		t.Fatalf("the picker no longer offers %s", anchorTestEffect)
	}
	m.buildEffectPreview()
	if p.engine == nil {
		t.Fatal("the preview would not build")
	}

	want, got := anchorCapture(p.capture), anchorFrame(p.engine)
	dy, dx, match, total := anchorOffset(want, got)
	glyphs := anchorGlyphs(want)
	if dy != 0 || dx != 0 || match != glyphs {
		t.Errorf("the preview drew the screen at offset row %+d column %+d, where %d of the %d glyphs in reach matched, out of %d captured",
			dy, dx, match, total, glyphs)
		t.Logf("captured screen:\n%s", strings.Join(want, "\n"))
		t.Logf("first frame:\n%s", strings.Join(got, "\n"))
	}
}
