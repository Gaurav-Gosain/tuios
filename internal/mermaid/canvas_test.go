package mermaid

import "testing"

// TestCanvasIsBounded asks for the frame of a terminal that reports cells of
// 65,535 pixels. Without the bound that is one image.NewRGBA of about 17 GB
// a frame.
func TestCanvasIsBounded(t *testing.T) {
	for _, a := range []Area{
		{Cols: 200, Rows: 60, CellW: 65535, CellH: 65535},
		{Cols: 65535, Rows: 65535, CellW: 65535, CellH: 65535},
		{Cols: 1, Rows: 1, CellW: 1 << 30, CellH: 1},
	} {
		w, h := canvas(a, 1)
		if w < 1 || h < 1 || float64(w)*float64(h) > MaxFramePixels {
			t.Fatalf("%+v: canvas %dx%d, want at most %d pixels", a, w, h, MaxFramePixels)
		}
	}
	if w, h := canvas(Area{Cols: 100, Rows: 30, CellW: 10, CellH: 20}, 1); w != 1000 || h != 600 {
		t.Fatalf("an ordinary terminal: canvas %dx%d, want 1000x600", w, h)
	}
}
