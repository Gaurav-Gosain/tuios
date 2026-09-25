package vt

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The grid replaces uv.Buffer, and its cell semantics are claimed to be
// uv's. The claim is checked here the only way it can be: by driving both
// with the same random operations and comparing every cell after each one.
// uv.Buffer is the reference; a divergence is a bug in the grid.

type gridOp func(g *grid, b *uv.Buffer)

func randomGridCell(rng *rand.Rand) *uv.Cell {
	switch rng.Intn(8) {
	case 0:
		return nil
	case 1:
		c := uv.EmptyCell
		return &c
	case 2:
		return &uv.Cell{Content: "漢", Width: 2, Style: uv.Style{Fg: ansi.BasicColor(rng.Intn(16))}}
	case 3:
		return &uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: ansi.IndexedColor(rng.Intn(256))}}
	default:
		return &uv.Cell{Content: string(rune('a' + rng.Intn(26))), Width: 1, Style: uv.Style{Attrs: uint8(rng.Intn(4))}}
	}
}

func randomArea(rng *rand.Rand, w, h int) uv.Rectangle {
	if rng.Intn(2) == 0 {
		return uv.Rect(0, 0, w, h)
	}
	x0, x1 := rng.Intn(w), rng.Intn(w)
	y0, y1 := rng.Intn(h), rng.Intn(h)
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return uv.Rect(x0, y0, x1-x0+1, y1-y0+1)
}

func randomGridOp(rng *rand.Rand, w, h int) (string, gridOp) {
	switch rng.Intn(9) {
	case 0, 1, 2:
		x, y, c := rng.Intn(w+1)-1, rng.Intn(h+1)-1, randomGridCell(rng)
		return fmt.Sprintf("SetCell(%d,%d,%v)", x, y, c), func(g *grid, b *uv.Buffer) {
			g.SetCell(x, y, c)
			b.SetCell(x, y, c)
		}
	case 3:
		c, area := randomGridCell(rng), randomArea(rng, w, h)
		return fmt.Sprintf("FillArea(%v,%v)", c, area), func(g *grid, b *uv.Buffer) {
			g.FillArea(c, area)
			b.FillArea(c, area)
		}
	case 4:
		area := randomArea(rng, w, h)
		return fmt.Sprintf("ClearArea(%v)", area), func(g *grid, b *uv.Buffer) {
			g.ClearArea(area)
			b.ClearArea(area)
		}
	case 5:
		y, n, c, area := rng.Intn(h), rng.Intn(h+1), randomGridCell(rng), randomArea(rng, w, h)
		return fmt.Sprintf("InsertLineArea(%d,%d,%v,%v)", y, n, c, area), func(g *grid, b *uv.Buffer) {
			g.InsertLineArea(y, n, c, area)
			b.InsertLineArea(y, n, c, area)
		}
	case 6:
		y, n, c, area := rng.Intn(h), rng.Intn(h+1), randomGridCell(rng), randomArea(rng, w, h)
		return fmt.Sprintf("DeleteLineArea(%d,%d,%v,%v)", y, n, c, area), func(g *grid, b *uv.Buffer) {
			g.DeleteLineArea(y, n, c, area)
			b.DeleteLineArea(y, n, c, area)
		}
	case 7:
		return "Clear()", func(g *grid, b *uv.Buffer) {
			g.Clear()
			b.Clear()
		}
	default:
		nw, nh := 1+rng.Intn(w+3), 1+rng.Intn(h+3)
		return fmt.Sprintf("Resize(%d,%d)", nw, nh), func(g *grid, b *uv.Buffer) {
			g.Resize(nw, nh)
			b.Resize(nw, nh)
		}
	}
}

func gridsAgree(t *testing.T, g *grid, b *uv.Buffer, what string) {
	t.Helper()
	if g.Width() != b.Width() || g.Height() != b.Height() {
		t.Fatalf("after %s: grid is %dx%d, uv.Buffer is %dx%d", what, g.Width(), g.Height(), b.Width(), b.Height())
	}
	for y := range b.Height() {
		for x := range b.Width() {
			got, want := g.CellAt(x, y), b.CellAt(x, y)
			if (got == nil) != (want == nil) || (got != nil && !got.Equal(want) && !(got.IsZero() && want.IsZero())) {
				t.Fatalf("after %s: cell (%d,%d) is %#v in the grid, %#v in uv.Buffer", what, x, y, got, want)
			}
		}
	}
	if bad := extentHolds(g); bad != "" {
		t.Fatalf("after %s: %s", what, bad)
	}
	if gs, bs := g.String(), b.String(); gs != bs {
		t.Fatalf("after %s: String differs:\n grid %q\n   uv %q", what, gs, bs)
	}
	if gs, bs := g.Render(), b.Render(); gs != bs {
		t.Fatalf("after %s: Render differs:\n grid %q\n   uv %q", what, gs, bs)
	}
}

func TestGridMatchesUVBufferUnderRandomOperations(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		w, h := 1+rng.Intn(12), 1+rng.Intn(8)
		g := newGrid(w, h)
		b := uv.NewBuffer(w, h)
		gridsAgree(t, g, b, "construction")
		for i := range 300 {
			name, op := randomGridOp(rng, g.Width(), g.Height())
			op(g, b)
			gridsAgree(t, g, b, fmt.Sprintf("seed %d op %d %s", seed, i, name))
		}
	}
}

// TestGridFillAreaMatchesUVBuffer drives FillArea harder than the mixed
// sweep above, because FillArea only calls Set on the two end cells of each
// row's span and stores the rest directly. The claim that this matches a Set
// on every cell rests on uv's wide-character repair, so rows are seeded with
// characters of width 2 and 3, and fills use cells of width 0 to 3 and areas
// that start left of the grid and end past it.
func TestGridFillAreaMatchesUVBuffer(t *testing.T) {
	cellOfWidth := func(rng *rand.Rand) *uv.Cell {
		switch rng.Intn(6) {
		case 0:
			return &uv.Cell{Content: "\u200b", Width: 0}
		case 1:
			return &uv.Cell{Content: "漢", Width: 2, Style: uv.Style{Attrs: 1}}
		case 2:
			return &uv.Cell{Content: "\U0001F468\u200d\U0001F469\u200d\U0001F467", Width: 3}
		default:
			return randomGridCell(rng)
		}
	}
	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		w, h := 1+rng.Intn(14), 1+rng.Intn(4)
		g := newGrid(w, h)
		b := uv.NewBuffer(w, h)
		for i := range 60 {
			if rng.Intn(2) == 0 {
				x, y, c := rng.Intn(w+2)-1, rng.Intn(h), cellOfWidth(rng)
				g.SetCell(x, y, c)
				b.SetCell(x, y, c)
				gridsAgree(t, g, b, fmt.Sprintf("seed %d op %d SetCell(%d,%d,%v)", seed, i, x, y, c))
				continue
			}
			c := cellOfWidth(rng)
			x0, x1 := rng.Intn(w+4)-2, rng.Intn(w+4)-2
			if x0 > x1 {
				x0, x1 = x1, x0
			}
			area := uv.Rect(x0, rng.Intn(h+1)-1, x1-x0+1, 1+rng.Intn(h+1))
			g.FillArea(c, area)
			b.FillArea(c, area)
			gridsAgree(t, g, b, fmt.Sprintf("seed %d op %d FillArea(%v,%v)", seed, i, c, area))
		}
	}
}

// TestGridRowsStayUnwrittenUntilWritten pins the reason the grid exists: a
// row nothing has printed on costs nothing, and a write that would leave it
// blank does not allocate it.
func TestGridRowsStayUnwrittenUntilWritten(t *testing.T) {
	g := newGrid(207, 55)
	for y := range 55 {
		if g.Row(y) != nil {
			t.Fatalf("row %d of a new grid is allocated", y)
		}
	}
	blank := uv.EmptyCell
	g.SetCell(3, 3, nil)
	g.SetCell(4, 3, &blank)
	g.ClearArea(g.Bounds())
	g.FillArea(nil, uv.Rect(0, 0, 207, 55))
	g.DeleteLineArea(0, 3, nil, g.Bounds())
	g.InsertLineArea(0, 3, nil, g.Bounds())
	g.Clear()
	for y := range 55 {
		if g.Row(y) != nil {
			t.Fatalf("row %d was allocated by writes of blanks", y)
		}
	}
	if c := g.CellAt(10, 10); c == nil || !c.Equal(&uv.EmptyCell) {
		t.Fatalf("an unwritten cell reads as %#v, want a blank", c)
	}

	g.SetCell(5, 7, &uv.Cell{Content: "x", Width: 1})
	written := 0
	for y := range 55 {
		if g.Row(y) != nil {
			written++
		}
	}
	if written != 1 {
		t.Fatalf("%d rows allocated after one write, want 1", written)
	}
	bg := uv.EmptyCell
	bg.Style.Bg = ansi.BasicColor(4)
	g.SetCell(0, 9, &bg)
	if g.Row(9) == nil {
		t.Fatal("a painted blank did not allocate its row: the colour would be lost")
	}
}

// TestEmptyPaneCostsNoGrid pins the number the maintainer asked for: what an
// empty pane holds before it prints anything. The grid used to be 1.3 MB of
// it at 207x55.
func TestEmptyPaneCostsNoGrid(t *testing.T) {
	const w, h, n = 207, 55, 8
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	emus := make([]*Emulator, n)
	for i := range emus {
		emus[i] = NewEmulator(w, h)
		emus[i].SetScrollbackMaxLines(DefaultScrollbackSize)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(emus)
	perPane := (int64(after.HeapAlloc) - int64(before.HeapAlloc)) / n
	const oneGrid = w * h * 112
	if perPane > oneGrid/4 {
		t.Fatalf("an empty %dx%d pane holds %d bytes, want well under the %d one grid of cells costs", w, h, perPane, oneGrid)
	}
	t.Logf("an empty %dx%d pane holds %d bytes", w, h, perPane)
}

// TestScrollRetainsUnwrittenRowsAsBlankLines pins that a row nothing was
// printed on still counts as a line when it scrolls off: a guest that prints
// three lines and then a screenful of newlines has a scrollback of all of
// them, not just the three.
func TestScrollRetainsUnwrittenRowsAsBlankLines(t *testing.T) {
	const w, h = 20, 5
	e := NewEmulator(w, h)
	e.WriteString("one\r\ntwo\r\nthree\r\n")
	for range h + 2 {
		e.WriteString("\r\n")
	}
	// The first newline only moves the cursor to the last row; each of the
	// other h+1 scrolls a line off.
	if got, want := e.ScrollbackLen(), h+1; got != want {
		t.Fatalf("scrollback holds %d lines, want %d", got, want)
	}
	if got := scrollbackText(e, 0); got != "one" {
		t.Fatalf("oldest line is %q, want one", got)
	}
	for i := 3; i < e.ScrollbackLen(); i++ {
		if line := e.ScrollbackLine(i); len(line) != w {
			t.Fatalf("blank line %d came back %d wide, want %d", i, len(line), w)
		} else if got := scrollbackText(e, i); got != "" {
			t.Fatalf("blank line %d reads %q", i, got)
		}
	}
}

// TestRenderKeepsUnwrittenRowsWide pins that a row nothing has printed on
// renders as a full row of blanks, as it did when every row was allocated:
// the app places each rendered row as a line of the pane's width, and a row
// that came out empty would be a row the compositor draws short.
func TestRenderKeepsUnwrittenRowsWide(t *testing.T) {
	const w, h = 12, 4
	e := NewEmulator(w, h)
	e.WriteString("hi")
	rows := strings.Split(e.Render(), "\n")
	if len(rows) != h {
		t.Fatalf("Render gave %d rows, want %d", len(rows), h)
	}
	for y, row := range rows {
		if got := ansi.StringWidth(row); got != w {
			t.Fatalf("row %d renders %d columns wide (%q), want %d", y, got, row, w)
		}
	}
	if rows[3] != strings.Repeat(" ", w) {
		t.Fatalf("an unwritten row renders as %q, want %d spaces", rows[3], w)
	}
}
