package vt

import (
	"image"
	"image/color"
)

// SixelImage is a decoded sixel picture: one palette index per pixel, and the
// palette.
//
// It is kept indexed rather than expanded to RGBA for two reasons. A sixel
// image is already paletted, so a crop re-encoded from the indices keeps every
// colour exactly as the program chose it, with no quantising step. And it is
// half the memory of RGBA at two bytes a pixel, which matters because a pane
// can hold a screenful of pictures in its scrollback.
type SixelImage struct {
	Width, Height int
	// Pix holds one entry per pixel, row by row. Zero is a pixel no sixel
	// painted, which is drawn transparent; n is palette register n-1.
	Pix []uint16
	// Palette is indexed by register. Registers the image never set keep the
	// VT340 defaults, as a terminal would.
	Palette []color.RGBA
}

// Sixel limits. A guest controls every one of these numbers, so each is
// bounded before anything is allocated.
const (
	// SixelMaxDimension bounds either side of a decoded image in pixels. It
	// is larger than any screen, so a real picture is never cut by it.
	SixelMaxDimension = 8192
	// SixelMaxPixels bounds the area, at two bytes a pixel: 32 MiB.
	SixelMaxPixels = 16 << 20
)

// sixelDefaultPalette is the VT340's sixteen colours, which a sixel image that
// selects a register without defining it gets.
var sixelDefaultPalette = [16]color.RGBA{
	{0, 0, 0, 255}, {51, 51, 204, 255}, {204, 36, 36, 255}, {51, 204, 51, 255},
	{204, 51, 204, 255}, {51, 204, 204, 255}, {204, 204, 51, 255}, {120, 120, 120, 255},
	{69, 69, 69, 255}, {87, 87, 153, 255}, {153, 69, 69, 255}, {87, 153, 87, 255},
	{153, 87, 153, 255}, {87, 153, 153, 255}, {153, 153, 87, 255}, {204, 204, 204, 255},
}

// DecodeSixel paints cmd's sixel data into an image of cmd.Width by
// cmd.Height pixels, the size the emulator already used to reserve the image's
// cells. Pixels drawn past that size are dropped, so the picture and the cells
// it owns always agree. It returns nil when the image is empty or larger than
// the limits above.
func DecodeSixel(cmd *SixelCommand) *SixelImage {
	if cmd == nil {
		return nil
	}
	w, h := cmd.Width, cmd.Height
	if w <= 0 || h <= 0 || w > SixelMaxDimension || h > SixelMaxDimension || w*h > SixelMaxPixels {
		return nil
	}
	img := &SixelImage{
		Width:   w,
		Height:  h,
		Pix:     make([]uint16, w*h),
		Palette: make([]color.RGBA, SixelMaxRegisters),
	}
	for i := range img.Palette {
		img.Palette[i] = sixelDefaultPalette[i%len(sixelDefaultPalette)]
	}

	data := cmd.Data
	x, y := 0, 0
	reg := 0
	i := 0
	// num reads a decimal parameter, bounded so a long digit run cannot
	// overflow.
	num := func() (int, bool) {
		start := i
		n := 0
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			if n < 1<<24 {
				n = n*10 + int(data[i]-'0')
			}
			i++
		}
		return n, i > start
	}
	paint := func(bits byte, count int) {
		if bits == 0 || y >= h {
			x += count
			return
		}
		for dy := 0; dy < 6; dy++ {
			if bits&(1<<dy) == 0 {
				continue
			}
			py := y + dy
			if py >= h {
				break
			}
			row := img.Pix[py*w : py*w+w]
			end := min(x+count, w)
			for px := x; px < end; px++ {
				row[px] = uint16(reg + 1)
			}
		}
		x += count
	}

	for i < len(data) {
		c := data[i]
		switch {
		case c >= '?' && c <= '~':
			paint(c-'?', 1)
			i++
		case c == '!':
			i++
			n, _ := num()
			if n < 1 {
				n = 1
			}
			if i < len(data) && data[i] >= '?' && data[i] <= '~' {
				// A repeat past the right edge only advances x; bound it so
				// a huge count costs nothing.
				paint(data[i]-'?', min(n, w+1))
				i++
			}
		case c == '#':
			i++
			var params [5]int
			np := 0
			for np < 5 {
				v, _ := num()
				params[np] = v
				np++
				if i < len(data) && data[i] == ';' {
					i++
					continue
				}
				break
			}
			reg = params[0] % SixelMaxRegisters
			if np >= 5 {
				img.Palette[reg] = sixelColor(params[1], params[2], params[3], params[4])
			}
		case c == '$':
			x = 0
			i++
		case c == '-':
			x = 0
			y += 6
			i++
		case c == '"':
			// Raster attributes. The size they state is already in cmd; skip
			// the parameters.
			i++
			for i < len(data) && (data[i] >= '0' && data[i] <= '9' || data[i] == ';') {
				i++
			}
		default:
			i++
		}
	}
	return img
}

// sixelColor turns a colour introducer's coordinates into RGB. Space 1 is HLS
// (hue 0-360, lightness and saturation 0-100) and space 2 is RGB in percent.
func sixelColor(space, a, b, c int) color.RGBA {
	pct := func(v int) uint8 {
		v = max(0, min(v, 100))
		return uint8((v*255 + 50) / 100)
	}
	if space != 1 {
		return color.RGBA{pct(a), pct(b), pct(c), 255}
	}
	// HLS as the VT340 defines it: hue 0 is blue, not red.
	h := float64((a+240)%360) / 360
	l := float64(max(0, min(b, 100))) / 100
	s := float64(max(0, min(c, 100))) / 100
	if s == 0 {
		v := uint8(l*255 + 0.5)
		return color.RGBA{v, v, v, 255}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hue := func(t float64) uint8 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		var v float64
		switch {
		case t < 1.0/6:
			v = p + (q-p)*6*t
		case t < 0.5:
			v = q
		case t < 2.0/3:
			v = p + (q-p)*(2.0/3-t)*6
		default:
			v = p
		}
		return uint8(v*255 + 0.5)
	}
	return color.RGBA{hue(h + 1.0/3), hue(h), hue(h - 1.0/3), 255}
}

// Bytes is the memory the image holds, for the per-pane budget.
func (img *SixelImage) Bytes() int {
	if img == nil {
		return 0
	}
	return len(img.Pix)*2 + len(img.Palette)*4
}

// At returns the colour of one pixel, and false for a transparent one.
func (img *SixelImage) At(x, y int) (color.RGBA, bool) {
	if x < 0 || y < 0 || x >= img.Width || y >= img.Height {
		return color.RGBA{}, false
	}
	v := img.Pix[y*img.Width+x]
	if v == 0 {
		return color.RGBA{}, false
	}
	return img.Palette[v-1], true
}

// RGBA expands the part of the image inside r to 8-bit RGBA, four bytes a
// pixel, with unpainted pixels fully transparent. It is what a kitty host is
// sent when the image has to go to it in kitty's format.
func (img *SixelImage) RGBA(r image.Rectangle) []byte {
	r = r.Intersect(image.Rect(0, 0, img.Width, img.Height))
	out := make([]byte, 0, r.Dx()*r.Dy()*4)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[y*img.Width : (y+1)*img.Width]
		for x := r.Min.X; x < r.Max.X; x++ {
			v := row[x]
			if v == 0 {
				out = append(out, 0, 0, 0, 0)
				continue
			}
			c := img.Palette[v-1]
			out = append(out, c.R, c.G, c.B, 255)
		}
	}
	return out
}
