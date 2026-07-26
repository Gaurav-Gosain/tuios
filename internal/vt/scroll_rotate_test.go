package vt

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// snapshotCells copies the whole grid, so a scroll can be checked against what
// the screen held before it rather than against the implementation that
// produced the new screen.
func snapshotCells(e *Emulator) [][]uv.Cell {
	w, h := e.Width(), e.Height()
	grid := make([][]uv.Cell, h)
	for y := range h {
		grid[y] = make([]uv.Cell, w)
		for x := range w {
			if c := e.CellAt(x, y); c != nil {
				grid[y][x] = *c
			} else {
				grid[y][x] = uv.EmptyCell
			}
		}
	}
	return grid
}

// cellsEqual compares the two things a scroll must get right: which glyph is in
// a column and how it is styled.
func cellsEqual(a, b uv.Cell) bool {
	return a.Content == b.Content && a.Width == b.Width && a.Style.Equal(&b.Style)
}

// assertScrolledUp checks the grid against the expectation that rows top+n..bottom
// moved up by n, rows outside the region did not move, and the n rows at the
// bottom of the region are blank. It is written against the semantics of the
// sequence rather than against either implementation, so it holds for the
// whole-screen rotation and for the DeleteLineArea path a limited region still
// takes.
func assertScrolledUp(t *testing.T, e *Emulator, before [][]uv.Cell, top, bottom, n int) {
	t.Helper()

	w := e.Width()
	for y := range before {
		for x := range w {
			var want uv.Cell
			switch {
			case y < top || y > bottom:
				want = before[y][x]
			case y+n <= bottom:
				want = before[y+n][x]
			default:
				want = uv.EmptyCell
			}

			var got uv.Cell
			if c := e.CellAt(x, y); c != nil {
				got = *c
			}
			if !cellsEqual(got, want) {
				t.Fatalf("cell (%d,%d) after scrolling %d line(s) in region %d..%d: got %q (w=%d), want %q (w=%d)",
					x, y, n, top, bottom, got.Content, got.Width, want.Content, want.Width)
			}
		}
	}
}

// fillScreen paints every row with distinguishable content and a per-row colour,
// so a scroll that moves the right glyphs but loses their style is still caught.
func fillScreen(e *Emulator, w, h int) {
	for y := 1; y <= h; y++ {
		row := fmt.Sprintf("row%02d-", y) + strings.Repeat("x", max(w-8, 1))
		e.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[38;5;%dm%s\x1b[m", y, 20+y, row[:min(len(row), w)]))
	}
}

// TestScrollUpMovesTheRightRows pins the whole-screen scroll against the
// meaning of the sequence, across the shapes that reach it: an implicit scroll
// from output running off the bottom, an explicit CSI S, IND, a multi-line
// scroll, a scroll of the entire screen, and a scroll inside a DECSTBM region,
// which is the case the rotation deliberately does not handle and which still
// goes through Buffer.DeleteLineArea.
func TestScrollUpMovesTheRightRows(t *testing.T) {
	const w, h = 40, 12

	t.Run("linefeed at the bottom", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		e.WriteString(fmt.Sprintf("\x1b[%d;1H\n", h))
		assertScrolledUp(t, e, before, 0, h-1, 1)
	})

	t.Run("index at the bottom", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		e.WriteString(fmt.Sprintf("\x1b[%d;1H\x1bD", h))
		assertScrolledUp(t, e, before, 0, h-1, 1)
	})

	t.Run("CSI 1 S", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		e.WriteString("\x1b[S")
		assertScrolledUp(t, e, before, 0, h-1, 1)
	})

	t.Run("CSI 7 S", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		e.WriteString("\x1b[7S")
		assertScrolledUp(t, e, before, 0, h-1, 7)
	})

	t.Run("scroll the whole screen away", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		e.WriteString(fmt.Sprintf("\x1b[%dS", h))
		assertScrolledUp(t, e, before, 0, h-1, h)
	})

	t.Run("scroll past the whole screen", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		e.WriteString(fmt.Sprintf("\x1b[%dS", h+5))
		assertScrolledUp(t, e, before, 0, h-1, h)
	})

	t.Run("limited scroll region", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		before := snapshotCells(e)
		// DECSTBM rows 3..9 (1-based), then scroll inside it. Rows outside the
		// region must not move.
		e.WriteString("\x1b[3;9r\x1b[2S")
		assertScrolledUp(t, e, before, 2, 8, 2)
	})

	t.Run("cursor survives the scroll", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		e.WriteString("\x1b[5;7H")
		e.WriteString("\x1b[2S")
		if got, want := e.CursorPosition(), uv.Pos(6, 4); got != want {
			t.Errorf("cursor at %v after CSI 2 S, want %v", got, want)
		}
	})

	t.Run("blank rows inherit the background", func(t *testing.T) {
		e := NewEmulator(w, h)
		fillScreen(e, w, h)
		// Set a background colour on the pen, which the erased rows a scroll
		// leaves behind are supposed to take.
		e.WriteString("\x1b[48;5;52m\x1b[2S")
		bottom := e.CellAt(0, h-1)
		if bottom == nil || bottom.Style.Bg == nil {
			t.Fatalf("bottom row after a scroll with a pen background has no background: %+v", bottom)
		}
	})
}

// TestScrollUpFillsScrollback pins the other half of a whole-screen scroll: the
// rows leaving the top are what lands in the scrollback ring, in order and with
// their content intact. The scroll path hands the ring a line it allocated
// itself rather than a copy, so this is also the guard against that line being
// aliased to something still being written.
func TestScrollUpFillsScrollback(t *testing.T) {
	const w, h = 40, 8

	e := NewEmulator(w, h)
	e.SetScrollbackMaxLines(100)

	// Print more rows than fit, so the early ones scroll off the top.
	const printed = 20
	for i := range printed {
		e.WriteString(fmt.Sprintf("line-%02d\r\n", i))
	}

	wantScrollback := printed + 1 - h
	if got := e.ScrollbackLen(); got != wantScrollback {
		t.Fatalf("scrollback holds %d lines, want %d", got, wantScrollback)
	}

	for i := range wantScrollback {
		line := e.ScrollbackLine(i)
		if line == nil {
			t.Fatalf("scrollback line %d is nil", i)
		}
		var b strings.Builder
		for _, c := range line {
			b.WriteString(c.Content)
		}
		want := fmt.Sprintf("line-%02d", i)
		if got := strings.TrimRight(b.String(), " "); got != want {
			t.Errorf("scrollback line %d is %q, want %q", i, got, want)
		}
	}

	// Writing on after the scrolls must not reach back into a retained line,
	// which is what an aliased push would allow.
	e.WriteString("\x1b[H\x1b[2Joverwritten")
	line := e.ScrollbackLine(0)
	var b strings.Builder
	for _, c := range line {
		b.WriteString(c.Content)
	}
	if got, want := strings.TrimRight(b.String(), " "), "line-00"; got != want {
		t.Errorf("scrollback line 0 became %q after later writes, want %q", got, want)
	}
}

// TestScrollUpAllocatesOnlyTheRetainedLine pins the allocation cost of a
// whole-screen scroll at exactly the line being retained.
//
// A scroll allocates once, in extractLine, for the row moving into the
// scrollback ring. It used to allocate twice, because the ring then made a
// defensive copy of a line that had no other referent; at 112 bytes per cell
// and one line per terminal width that second copy was half of everything the
// write path allocated. The rotation itself must add nothing: it moves line
// headers and reuses the slices that fall off the top as the new blank rows.
func TestScrollUpAllocatesOnlyTheRetainedLine(t *testing.T) {
	e := NewEmulator(80, 24)
	fillScreen(e, 80, 24)

	got := testing.AllocsPerRun(200, func() {
		e.WriteString("\x1b[S")
	})
	if got > 1 {
		t.Errorf("a whole-screen scroll allocates %.1f times per call, want 1 (the retained scrollback line)", got)
	}
}

// TestAltScreenRetainsNothing pins the alternate screen keeping no scrollback,
// which is what every accessor on Emulator already assumes: Scrollback,
// ScrollbackLen, ScrollbackLine, ClearScrollback and SetScrollbackMaxLines all
// read the main screen. Its ring was filled anyway, by every scroll a
// full-screen application made, and no line of it could be read back.
//
// A scroll in the alternate screen must therefore allocate nothing at all, and
// the main screen's scrollback must survive the excursion untouched.
func TestAltScreenRetainsNothing(t *testing.T) {
	const w, h = 80, 24

	e := NewEmulator(w, h)
	for i := range 40 {
		e.WriteString(fmt.Sprintf("main-%02d\r\n", i))
	}
	mainLines := e.ScrollbackLen()
	if mainLines == 0 {
		t.Fatal("main screen retained no scrollback, the test proves nothing")
	}

	e.WriteString("\x1b[?1049h")
	fillScreen(e, w, h)

	if got := testing.AllocsPerRun(200, func() {
		e.WriteString("\x1b[S")
	}); got > 0 {
		t.Errorf("a scroll in the alternate screen allocates %.1f times per call, want 0", got)
	}

	e.WriteString("\x1b[?1049l")
	if got := e.ScrollbackLen(); got != mainLines {
		t.Errorf("main scrollback holds %d lines after an alternate-screen excursion, want %d", got, mainLines)
	}
	line := e.ScrollbackLine(0)
	var b strings.Builder
	for _, c := range line {
		b.WriteString(c.Content)
	}
	if got, want := strings.TrimRight(b.String(), " "), "main-00"; got != want {
		t.Errorf("oldest scrollback line is %q after the excursion, want %q", got, want)
	}
}
