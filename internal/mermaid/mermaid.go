// Package mermaid is the tuios side of Mermaid diagrams: it finds the
// diagrams in a file, colours them from the tuios palette, and writes them as
// kitty graphics or as block cells. The drawing itself is tuios.dev/sketch.
package mermaid

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"io"
	"regexp"
	"strconv"
	"strings"

	"tuios.dev/sketch"
	"tuios.dev/shot/mosaic"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Theme is the sketch theme of a tuios palette: the diagram's ground is the
// palette's surface, its boxes the card colour, its lines the dim text, and
// its eight series colours the ANSI colours 1 to 6 with Info and Success.
func Theme(pal overlay.Palette, ansi [16]color.Color) *sketch.Theme {
	t := &sketch.Theme{
		Bg:         pal.Surface,
		Fg:         pal.Fg,
		Muted:      pal.FgDim,
		Line:       pal.FgDim,
		Surface:    pal.Card,
		SurfaceAlt: pal.Panel,
		Border:     pal.Edge,
		Accent:     pal.Accent,
		Critical:   pal.Warn,
	}
	for i := 0; i < 6; i++ {
		t.Series[i] = ansi[i+1]
	}
	t.Series[6] = pal.Info
	t.Series[7] = pal.Success
	return t
}

var reFence = regexp.MustCompile("(?ms)^[ \t]*(```+|~~~+)[ \t]*mermaid[^\n]*\n(.*?)^[ \t]*(```+|~~~+)[ \t]*$")

// Sources lists the diagrams in a file: each mermaid fence of a Markdown
// file, or the whole text of any other.
func Sources(name string, data []byte) []string {
	text := string(data)
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || (name == "-" && strings.Contains(text, "```mermaid")) {
		var out []string
		for _, m := range reFence.FindAllStringSubmatch(text, -1) {
			out = append(out, m[2])
		}
		return out
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []string{text}
}

// Kitty writes a PNG as a kitty graphics image cols cells wide. The terminal
// keeps the picture's aspect and moves the cursor below it.
func Kitty(w io.Writer, png []byte, cols int) error {
	enc := base64.StdEncoding.EncodeToString(png)
	var b bytes.Buffer
	const chunk = 4096
	for i := 0; i < len(enc); i += chunk {
		end := min(i+chunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		b.WriteString("\x1b_G")
		if i == 0 {
			b.WriteString("a=T,f=100,q=2,c=" + strconv.Itoa(cols) + ",")
		}
		b.WriteString("m=" + strconv.Itoa(more) + ";" + enc[i:end] + "\x1b\\")
	}
	b.WriteString("\r\n")
	_, err := w.Write(b.Bytes())
	return err
}

// Mosaic draws an image as cols by rows cells of block glyphs on a ground, as
// lines of styled text.
func Mosaic(img image.Image, cols, rows int, kind mosaic.Kind, colors mosaic.Colors, ground color.Color) []string {
	// Four by eight pixels a cell is enough for every glyph set, octants included.
	const pw, ph = 4, 8
	dst := image.NewRGBA(image.Rect(0, 0, cols*pw, rows*ph))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(ground), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), boxScale(img, cols*pw, rows*ph), image.Point{}, draw.Over)
	cells := mosaic.Encode(indexed(dst), pw, ph, rows, cols, kind, colors)
	if cells == nil {
		return nil
	}
	lines := make([]string, 0, rows)
	var b strings.Builder
	for i, c := range cells {
		if i%cols == 0 && i > 0 {
			b.WriteString("\x1b[m")
			lines = append(lines, b.String())
			b.Reset()
		}
		switch colors {
		case mosaic.ANSI16:
			b.WriteString("\x1b[" + ansiSGR(c.FgIndex, false) + ";" + ansiSGR(c.BgIndex, true) + "m")
		case mosaic.XTerm256:
			b.WriteString("\x1b[38;5;" + strconv.Itoa(xterm256(c.Fg)) + ";48;5;" + strconv.Itoa(xterm256(c.Bg)) + "m")
		default:
			b.WriteString("\x1b[38;2;" + rgb(c.Fg) + ";48;2;" + rgb(c.Bg) + "m")
		}
		b.WriteString(c.Glyph)
	}
	b.WriteString("\x1b[m")
	return append(lines, b.String())
}

func rgb(c color.RGBA) string {
	return strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B))
}

func ansiSGR(i uint8, bg bool) string {
	base := 30
	if i >= 8 {
		base = 90
		i -= 8
	}
	if bg {
		base += 10
	}
	return strconv.Itoa(base + int(i))
}

// xterm256 is the palette entry 16 to 255 that mosaic chose the colour from.
func xterm256(c color.RGBA) int {
	level := func(v uint8) int {
		steps := []int{0, 95, 135, 175, 215, 255}
		best := 0
		for i, s := range steps {
			if abs(int(v)-s) < abs(int(v)-steps[best]) {
				best = i
			}
		}
		return best
	}
	r, g, b := level(c.R), level(c.G), level(c.B)
	cube := 16 + 36*r + 6*g + b
	steps := []int{0, 95, 135, 175, 215, 255}
	cubeErr := abs(int(c.R)-steps[r]) + abs(int(c.G)-steps[g]) + abs(int(c.B)-steps[b])
	gray := (int(c.R) + int(c.G) + int(c.B)) / 3
	gi := max(0, min(23, (gray-8+5)/10))
	gv := 8 + gi*10
	grayErr := abs(int(c.R)-gv) + abs(int(c.G)-gv) + abs(int(c.B)-gv)
	if grayErr < cubeErr {
		return 232 + gi
	}
	return cube
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// boxScale scales src to w by h pixels, each the average of the source pixels
// under it, as the reply previews do.
func boxScale(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return dst
	}
	for y := range dst.Bounds().Dy() {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		y1 = max(y1, y0+1)
		for x := range dst.Bounds().Dx() {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			x1 = max(x1, x0+1)
			var sr, sg, sb, sa, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					r, g, bl, a := src.At(xx, yy).RGBA()
					sr, sg, sb, sa, n = sr+uint64(r), sg+uint64(g), sb+uint64(bl), sa+uint64(a), n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8((sr / n) >> 8)
			dst.Pix[i+1] = uint8((sg / n) >> 8)
			dst.Pix[i+2] = uint8((sb / n) >> 8)
			dst.Pix[i+3] = uint8((sa / n) >> 8)
		}
	}
	return dst
}

// indexed is an opaque picture in the paletted form mosaic takes.
func indexed(img *image.RGBA) mosaic.Indexed {
	b := img.Bounds()
	out := mosaic.Indexed{Width: b.Dx(), Height: b.Dy(), Pix: make([]uint16, b.Dx()*b.Dy())}
	seen := make(map[color.RGBA]uint16)
	for y := range b.Dy() {
		for x := range b.Dx() {
			i := img.PixOffset(x, y)
			c := color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], 255}
			n, ok := seen[c]
			if !ok {
				out.Palette = append(out.Palette, c)
				n = uint16(len(out.Palette))
				seen[c] = n
			}
			out.Pix[y*b.Dx()+x] = n
		}
	}
	return out
}
