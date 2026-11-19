package mermaid

import (
	"image"
	"testing"

	sketch "tuios.dev/sketch/core"
)

// TestDrawTakesTheRowsTheDiagramDraws draws a two-box flowchart with no
// background, at 8 by 16 pixel cells. The picture's box is the rows its boxes
// take: sketch's clear margin is cut, so the box is no more than 3 rows. With
// a background the margin is the background's, and it is kept.
func TestDrawTakesTheRowsTheDiagramDraws(t *testing.T) {
	src := "flowchart LR\n  A[Plan] --> B[Ship]"
	var th *sketch.Theme
	clear, err := Draw(src, th, PictureOptions{Cols: 80, CellW: 8, CellH: 16})
	if err != nil {
		t.Fatal(err)
	}
	if clear.Rows > 3 {
		t.Fatalf("the clear picture takes %d rows, want at most 3", clear.Rows)
	}
	ground, err := Draw(src, th, PictureOptions{Cols: 80, CellW: 8, CellH: 16, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if ground.Rows <= clear.Rows {
		t.Fatalf("the picture with a background takes %d rows, the clear one %d: the margin is cut from both", ground.Rows, clear.Rows)
	}
}

// TestTrimClearKeepsAPad cuts a 10 by 10 clear image with one pixel drawn at
// 4,5 to that pixel and a pad of 2 around it.
func TestTrimClearKeepsAPad(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Pix[img.PixOffset(4, 5)+3] = 0xff
	if got, want := trimClear(img, 2).Bounds(), image.Rect(2, 3, 7, 8); got != want {
		t.Fatalf("trimmed to %v, want %v", got, want)
	}
	if got := trimClear(image.NewRGBA(image.Rect(0, 0, 4, 4)), 2).Bounds(); got != image.Rect(0, 0, 4, 4) {
		t.Fatalf("an empty image is trimmed to %v", got)
	}
}
