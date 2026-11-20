package mermaid

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"math"

	sketch "tuios.dev/sketch/core"
	"tuios.dev/sketch/raster"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// Picture is a diagram drawn for a terminal: a PNG that fills a box of whole
// cells, Cols wide and Rows tall.
type Picture struct {
	PNG        []byte
	Cols, Rows int
}

// PictureOptions say how large a picture may be. Cols bounds its width in
// cells, and Rows its height when above 0. CellW and CellH are the size of a
// cell in pixels. Background paints the theme's background behind the
// diagram; without it the picture is clear where the diagram draws nothing.
// Plugin draws a type this build does not draw as SVG, with the sketch
// plugin: the SVG and its size in SVG units. Nil draws only the types of this
// build.
type PictureOptions struct {
	Cols, Rows   int
	CellW, CellH int
	Background   bool
	Plugin       func(src string) (svg string, width, height float64, err error)
}

// Draw draws a Mermaid source as a picture for a terminal. tuios mermaid
// --format kitty and the agents view both draw with it. A type this build
// does not draw comes from o.Plugin, in the colours of th.
func Draw(src string, th *sketch.Theme, o PictureOptions) (Picture, error) {
	if typ, known := sketch.Detect(src); known && !sketch.Supports(src) {
		if o.Plugin == nil {
			return Picture{}, errfmt.Errorf("the sketch plugin draws %s diagrams", typ)
		}
		data, w, h, err := o.Plugin(src)
		if err != nil {
			return Picture{}, err
		}
		return DrawSVG(sketch.SVG{Data: data, Width: w, Height: h}, th, o)
	}
	out, err := sketch.Render(src, sketch.RenderOptions{Theme: th})
	if err != nil {
		return Picture{}, err
	}
	return DrawSVG(out, th, o)
}

// DrawSVG is Draw for a diagram sketch has already drawn as SVG.
func DrawSVG(out sketch.SVG, th *sketch.Theme, o PictureOptions) (Picture, error) {
	cellW, cellH := max(o.CellW, 1), max(o.CellH, 1)
	scale := Scale(out.Width, out.Height, o.Cols, o.Rows, cellW, cellH)
	// The theme sets the --pele-* tokens, so an SVG drawn in another theme,
	// such as the sketch plugin's, takes th's colours.
	img, err := raster.Draw(out.Data, raster.Options{Scale: scale, Theme: th, Background: o.Background, Readable: true})
	if err != nil {
		return Picture{}, err
	}
	if !o.Background {
		// Clear around the diagram is the margin sketch leaves: the box
		// would take rows that show nothing.
		img = trimClear(img, 2)
	}
	box, c, r := CellBox(img, cellW, cellH, o.Cols, o.Rows, o.Background, th)
	var b bytes.Buffer
	if err := png.Encode(&b, box); err != nil {
		return Picture{}, err
	}
	return Picture{PNG: b.Bytes(), Cols: c, Rows: r}, nil
}

// trimClear cuts the clear rows and columns off the sides of img, and keeps
// pad pixels of them. An image with nothing drawn is kept whole.
func trimClear(img *image.RGBA, pad int) *image.RGBA {
	b := img.Bounds()
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y):]
		for x := range b.Dx() {
			if row[4*x+3] != 0 {
				x0, y0, x1, y1 = min(x0, b.Min.X+x), min(y0, y), max(x1, b.Min.X+x+1), max(y1, y+1)
			}
		}
	}
	if x0 >= x1 || y0 >= y1 {
		return img
	}
	return img.SubImage(image.Rect(x0-pad, y0-pad, x1+pad, y1+pad).Intersect(b)).(*image.RGBA)
}

// diagramFontSize is the size in SVG units of a diagram's body text. sketch
// draws labels at 16px, as pele does.
const diagramFontSize = 16

// Scale is the pixels a diagram unit takes in a picture of the terminal.
// Its body text gets the size of the terminal's own text, about four fifths of
// a cell's height, so every diagram of a file has one text size. It is
// smaller only where the diagram would be wider than cols cells, or taller
// than rows cells when rows is above 0, or larger than a picture can be. A
// small diagram is not made larger than that.
func Scale(width, height float64, cols, rows, cellW, cellH int) float64 {
	width, height = math.Max(1, width), math.Max(1, height)
	scale := math.Min(0.8*float64(cellH)/diagramFontSize, float64(cols*cellW)/width)
	if rows > 0 {
		scale = math.Min(scale, float64(rows*cellH)/height)
	}
	// raster.Draw refuses a side past 32768 pixels or 40 million pixels in all.
	return math.Min(scale, math.Min(32000/math.Max(width, height), math.Sqrt(39e6/(width*height))))
}

// CellBox pads a picture to whole cells, so the box of c by r cells the
// terminal draws it into has the picture's own aspect. The padding is the
// diagram's background, or clear when there is none. The box is at most cols
// cells wide, and rows cells tall when rows is above 0.
func CellBox(img image.Image, cellW, cellH, cols, rows int, background bool, th *sketch.Theme) (image.Image, int, int) {
	b := img.Bounds()
	c := min(max(int(math.Ceil(float64(b.Dx())/float64(cellW))), 1), max(cols, 1))
	r := max(int(math.Ceil(float64(b.Dy())/float64(cellH))), 1)
	if rows > 0 {
		r = min(r, rows)
	}
	w, h := c*cellW, r*cellH
	if w == b.Dx() && h == b.Dy() {
		return img, c, r
	}
	dst := image.NewNRGBA(image.Rect(0, 0, max(w, b.Dx()), max(h, b.Dy())))
	if background && th != nil && th.Bg != nil {
		draw.Draw(dst, dst.Bounds(), &image.Uniform{th.Bg}, image.Point{}, draw.Src)
	}
	draw.Draw(dst, b.Sub(b.Min), img, b.Min, draw.Over)
	return dst, c, r
}
