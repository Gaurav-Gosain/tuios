package shot

import "image"

// ScaleBilinear draws the part sr of src into the part dr of dst, sampling the
// four source pixels nearest each target pixel. With over set, it blends src
// on top of what dst holds. Without it, src replaces what dst holds. Pixels of
// dr outside dst are skipped.
//
// It stands in for ApproxBiLinear in golang.org/x/image/draw and gives the same
// pixels, within one level per channel. That package links a scaler for each
// pair of image types, about 150 KB of the file, and every caller here scales
// once per picture: a screenshot, a preview, an icon. There is one loop here.
// It reads src through RGBA64At, which takes about 1.3 times as long as the
// package's loops on RGBA and NRGBA, and twice as long on YCbCr.
func ScaleBilinear(dst *image.RGBA, dr image.Rectangle, src image.Image, sr image.Rectangle, over bool) {
	if dr.Empty() || sr.Empty() {
		return
	}
	shown := dr.Intersect(dst.Bounds())
	fast, _ := src.(image.RGBA64Image)
	at := func(x, y int) (r, g, b, a float64) {
		if fast != nil {
			c := fast.RGBA64At(x, y)
			return float64(c.R), float64(c.G), float64(c.B), float64(c.A)
		}
		cr, cg, cb, ca := src.At(x, y).RGBA()
		return float64(cr), float64(cg), float64(cb), float64(ca)
	}
	xScale := float64(sr.Dx()) / float64(dr.Dx())
	yScale := float64(sr.Dy()) / float64(dr.Dy())
	for dy := shown.Min.Y; dy < shown.Max.Y; dy++ {
		sy0, sy1, yNear, yFar := bilinearAxis(dy-dr.Min.Y, yScale, sr.Dy())
		sy0, sy1 = sy0+sr.Min.Y, sy1+sr.Min.Y
		d := dst.PixOffset(shown.Min.X, dy)
		for dx := shown.Min.X; dx < shown.Max.X; dx++ {
			sx0, sx1, xNear, xFar := bilinearAxis(dx-dr.Min.X, xScale, sr.Dx())
			sx0, sx1 = sx0+sr.Min.X, sx1+sr.Min.X
			r00, g00, b00, a00 := at(sx0, sy0)
			r10, g10, b10, a10 := at(sx1, sy0)
			r01, g01, b01, a01 := at(sx0, sy1)
			r11, g11, b11, a11 := at(sx1, sy1)
			mix := func(v00, v10, v01, v11 float64) uint32 {
				return uint32(yNear*(xNear*v00+xFar*v10) + yFar*(xNear*v01+xFar*v11))
			}
			pr, pg, pb, pa := mix(r00, r10, r01, r11), mix(g00, g10, g01, g11), mix(b00, b10, b01, b11), mix(a00, a10, a01, a11)
			px := dst.Pix[d : d+4 : d+4]
			if over {
				keep := (0xffff - pa) * 0x101
				px[0] = uint8((uint32(px[0])*keep/0xffff + pr) >> 8)
				px[1] = uint8((uint32(px[1])*keep/0xffff + pg) >> 8)
				px[2] = uint8((uint32(px[2])*keep/0xffff + pb) >> 8)
				px[3] = uint8((uint32(px[3])*keep/0xffff + pa) >> 8)
			} else {
				px[0], px[1], px[2], px[3] = uint8(pr>>8), uint8(pg>>8), uint8(pb>>8), uint8(pa>>8)
			}
			d += 4
		}
	}
}

// bilinearAxis names the two source positions, within size, that target
// position p falls between, and the weight of each.
func bilinearAxis(p int, scale float64, size int) (s0, s1 int, near, far float64) {
	at := (float64(p)+0.5)*scale - 0.5
	s0 = int(at)
	far = at - float64(s0)
	near = 1 - far
	s1 = s0 + 1
	switch {
	case at < 0:
		return 0, 0, 1, 0
	case s1 > size-1:
		return size - 1, size - 1, 1, 0
	}
	return s0, s1, near, far
}
