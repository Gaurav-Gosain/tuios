package app

import (
	"image/color"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestDimSurvivesAWrappedNilCellColour(t *testing.T) {
	// color.Color's RGBA has a value receiver, so calling it through an
	// interface holding a nil pointer panics rather than returning zeros. Cells
	// arrive that way, which is why the rest of this package screens with
	// isNilColor.
	var dst, src uv.Cell
	src.Content = "x"
	src.Style.Fg = (*color.RGBA)(nil)
	src.Style.Bg = (*color.RGBA)(nil)

	fg, bg := color.RGBA{R: 200, G: 200, B: 200, A: 255}, color.RGBA{R: 20, G: 20, B: 30, A: 255}
	got := dimCell(&dst, &src, fg, bg, 0.5)
	if got == nil {
		t.Fatal("dimCell returned nil")
	}
	if isNilColor(got.Style.Fg) {
		t.Error("a wrapped-nil fg was not replaced by the ground's own ink")
	}
}
