package app

import "testing"

// TestCKCanvasDrawKeepsWideChars guards the cell copy draw() makes from the
// one-line buffer onto the canvas. Copying the zero-width continuation cell
// of a wide character with SetCell splits the wide cell the copy of the wide
// character itself had just written: Line.Set answers a write over a
// placeholder by blanking the whole wide cell, so every CJK glyph a thread or
// the live preview drew through draw() vanished and left ground blanks behind
// (issue #1184).
func TestCKCanvasDrawKeepsWideChars(t *testing.T) {
	c := new(ckCanvas).reset(12, 1, nil)
	c.draw(0, 0, 12, "가나하")

	for i, want := range []string{"가", "나", "하"} {
		cell := c.buf.CellAt(i*2, 0)
		if cell == nil || cell.Content != want || cell.Width != 2 {
			t.Fatalf("cell %d (col %d): want %q width 2, got %+v", i, i*2, want, cell)
		}
	}
}

// TestCKCanvasDrawKeepsWideCharsAfterASCII pins the copy against a glyph that
// does not start at column zero, where the placeholder copy used to erase the
// glyph while the ASCII around it stayed readable — the exact shape the
// thread view showed.
func TestCKCanvasDrawKeepsWideCharsAfterASCII(t *testing.T) {
	c := new(ckCanvas).reset(12, 1, nil)
	c.draw(0, 0, 12, "hi 안녕")

	if cell := c.buf.CellAt(3, 0); cell == nil || cell.Content != "안" || cell.Width != 2 {
		t.Fatalf("cell at col 3: want 안 width 2, got %+v", cell)
	}
	if cell := c.buf.CellAt(4, 0); cell == nil || cell.Width != 0 || cell.Content != "" {
		t.Fatalf("cell at col 4: want the zero-width continuation of 안, got %+v", cell)
	}
}
