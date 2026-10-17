package shot

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

// Image is a picture drawn on the grid, such as one a program shows with the
// kitty graphics protocol or with sixel.
//
// The picture fills a box of cells. Source is the part of Pixels that goes
// into the box, scaled to fit it. Part of the box outside the grid is cut
// off, so a picture that scrolled half off the screen draws only the half
// that is on it.
//
// PNG, SVG and HTML draw images. ANSI and text output have no way to show a
// picture, so they leave images out.
type Image struct {
	// Pixels is the decoded picture.
	Pixels image.Image
	// Source is the rectangle of Pixels that fills the box. An empty Source
	// is all of Pixels. A Source that reaches past the edges of Pixels draws
	// nothing there, which is how a picture smaller than its box keeps its
	// own size: kitty draws an image at its own pixel size from the top-left
	// corner of its first cell.
	Source image.Rectangle
	// Col and Row are the cell at the top-left corner of the box. They can
	// be outside the grid.
	Col, Row int
	// Cols and Rows are the size of the box in cells.
	Cols, Rows int
	// Below draws the picture under the text and over the cell backgrounds,
	// which is what kitty does with a negative z-index. Otherwise the picture
	// goes over the text.
	Below bool
}

// imageCut is the part of an image that lands on the grid, in two spaces:
// cells for where it goes, and Pixels' own pixels for what it shows.
type imageCut struct {
	// col0, row0, col1, row1 are the cells it covers, as fractions, so a
	// source rectangle that rounds to whole pixels lands where it should.
	col0, row0, col1, row1 float64
	// src is the rectangle of Pixels drawn into them.
	src image.Rectangle
}

// cut works out what of img lands on the grid. It reports false when nothing
// does, so a backend skips the image without touching its pixels.
func (img Image) cut(cols, rows int) (imageCut, bool) {
	if img.Pixels == nil || img.Cols <= 0 || img.Rows <= 0 {
		return imageCut{}, false
	}
	bounds := img.Pixels.Bounds()
	source := img.Source
	if source.Empty() {
		source = bounds
	}
	if source.Empty() {
		return imageCut{}, false
	}
	// The box, clipped to the grid, as a fraction of the box on each axis.
	fx0 := float64(max(img.Col, 0)-img.Col) / float64(img.Cols)
	fy0 := float64(max(img.Row, 0)-img.Row) / float64(img.Rows)
	fx1 := float64(min(img.Col+img.Cols, cols)-img.Col) / float64(img.Cols)
	fy1 := float64(min(img.Row+img.Rows, rows)-img.Row) / float64(img.Rows)
	if fx1 <= fx0 || fy1 <= fy0 {
		return imageCut{}, false
	}
	// The same fractions of the source, kept to the pixels Pixels has.
	sw, sh := float64(source.Dx()), float64(source.Dy())
	sx0 := math.Max(float64(source.Min.X)+fx0*sw, float64(bounds.Min.X))
	sy0 := math.Max(float64(source.Min.Y)+fy0*sh, float64(bounds.Min.Y))
	sx1 := math.Min(float64(source.Min.X)+fx1*sw, float64(bounds.Max.X))
	sy1 := math.Min(float64(source.Min.Y)+fy1*sh, float64(bounds.Max.Y))
	src := image.Rect(int(math.Floor(sx0)), int(math.Floor(sy0)), int(math.Ceil(sx1)), int(math.Ceil(sy1)))
	if src.Empty() {
		return imageCut{}, false
	}
	// Back from the whole pixels to cells, so a rounded-out pixel lands on
	// the fraction of a cell it stands for and the picture is not stretched.
	toCol := func(x int) float64 {
		return float64(img.Col) + (float64(x)-float64(source.Min.X))/sw*float64(img.Cols)
	}
	toRow := func(y int) float64 {
		return float64(img.Row) + (float64(y)-float64(source.Min.Y))/sh*float64(img.Rows)
	}
	return imageCut{
		col0: toCol(src.Min.X), row0: toRow(src.Min.Y),
		col1: toCol(src.Max.X), row1: toRow(src.Max.Y),
		src: src,
	}, true
}

// drawPNGImages draws the images of one layer onto the raster, clipped to the
// grid.
func drawPNGImages(dst *image.RGBA, g *Grid, l layout, below bool) {
	if len(g.Images) == 0 {
		return
	}
	clip := image.Rect(
		int(math.Round(l.gridX)), int(math.Round(l.gridY)),
		int(math.Round(l.gridX+float64(g.Cols)*l.cw)), int(math.Round(l.gridY+float64(g.Rows)*l.ch)),
	).Intersect(dst.Bounds())
	canvas, ok := dst.SubImage(clip).(*image.RGBA)
	if !ok {
		return
	}
	for _, img := range g.Images {
		if img.Below != below {
			continue
		}
		c, ok := img.cut(g.Cols, g.Rows)
		if !ok {
			continue
		}
		to := image.Rect(
			int(math.Round(l.gridX+c.col0*l.cw)), int(math.Round(l.gridY+c.row0*l.ch)),
			int(math.Round(l.gridX+c.col1*l.cw)), int(math.Round(l.gridY+c.row1*l.ch)),
		)
		if to.Empty() {
			continue
		}
		xdraw.ApproxBiLinear.Scale(canvas, to, img.Pixels, c.src, xdraw.Over, nil)
	}
}

// imageDataURI encodes the part of an image a backend shows as a PNG data
// URI. Only the visible part is encoded, so a picture that is mostly off the
// grid costs only what is on it.
func imageDataURI(pixels image.Image, src image.Rectangle) (string, bool) {
	// A copy rather than SubImage: asking for SubImage through an interface
	// links the method of every image type in the standard library.
	part := image.NewNRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	xdraw.Copy(part, image.Point{}, pixels, src, xdraw.Src, nil)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, part); err != nil {
		return "", false
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), true
}
