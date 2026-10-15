package vt

import (
	"image/color"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// TestPenStyleEqualAgreesWithUV checks the render loop's style comparison
// against uv.Style.Equal, which it replaces. The value shortcut must never
// answer differently from comparing RGBA, including for values of one type
// that differ but look the same and for values of different types.
func TestPenStyleEqualAgreesWithUV(t *testing.T) {
	colors := []color.Color{
		nil,
		ansi.BasicColor(1),
		ansi.BasicColor(9),
		ansi.IndexedColor(1),
		ansi.IndexedColor(15),
		ansi.IndexedColor(231),
		ansi.RGBColor{R: 0xff, G: 0xff, B: 0xff},
		ansi.RGBColor{R: 0x80, G: 0x00, B: 0x00},
		color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		color.RGBA{R: 0x80, A: 0xff},
		color.NRGBA{R: 0x80, A: 0xff},
		ansi.RGBColor{R: 0xff, G: 0xff, B: 0xff},
	}
	for _, a := range colors {
		for _, b := range colors {
			for _, field := range []string{"fg", "bg", "ul"} {
				sa, sb := uv.Style{}, uv.Style{}
				switch field {
				case "fg":
					sa.Fg, sb.Fg = a, b
				case "bg":
					sa.Bg, sb.Bg = a, b
				case "ul":
					sa.UnderlineColor, sb.UnderlineColor = a, b
				}
				if got, want := penStyleEqual(&sa, &sb), sa.Equal(&sb); got != want {
					t.Errorf("%s %#v vs %#v: penStyleEqual %v, uv says %v", field, a, b, got, want)
				}
			}
		}
	}

	attrs := []uv.Style{
		{},
		{Attrs: uv.AttrBold},
		{Attrs: uv.AttrItalic},
		{Underline: uv.UnderlineSingle},
		{Underline: uv.UnderlineCurly},
	}
	for _, a := range attrs {
		for _, b := range attrs {
			if got, want := penStyleEqual(&a, &b), a.Equal(&b); got != want {
				t.Errorf("%+v vs %+v: penStyleEqual %v, uv says %v", a, b, got, want)
			}
		}
	}
}

// TestIsBlankCellMatchesEmptyCell checks the render loop's blank test against
// the uv comparison it replaces.
func TestIsBlankCellMatchesEmptyCell(t *testing.T) {
	cells := []uv.Cell{
		uv.EmptyCell,
		{Content: " ", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(1)}},
		{Content: " ", Width: 1, Style: uv.Style{Bg: ansi.BasicColor(1)}},
		{Content: " ", Width: 1, Style: uv.Style{UnderlineColor: ansi.BasicColor(1)}},
		{Content: " ", Width: 1, Style: uv.Style{Underline: uv.UnderlineSingle}},
		{Content: " ", Width: 1, Style: uv.Style{Attrs: uv.AttrReverse}},
		{Content: " ", Width: 1, Link: uv.Link{URL: "https://example.com"}},
		{Content: " ", Width: 1, Link: uv.Link{Params: "id=1"}},
		{Content: "a", Width: 1},
		{Content: " ", Width: 2},
		{},
	}
	for _, c := range cells {
		if got, want := isBlankCell(&c), c.Equal(&uv.EmptyCell); got != want {
			t.Errorf("%+v: isBlankCell %v, Equal(EmptyCell) %v", c, got, want)
		}
	}
}
