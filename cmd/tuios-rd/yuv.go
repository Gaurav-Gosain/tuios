//go:build linux || darwin

package main

import "sync"

// yuv is an I420 picture copied out of the decoder, tightly packed.
type yuv struct {
	w, h    int
	cw, ch  int
	y, u, v []byte
	// reduced planes for downscaling, reused between frames
	ry, ru, rv []byte
	tmp        []uint16
	hy, hu, hv []byte // 2x box steps
}

func (b *yuv) from(p picture) {
	b.w, b.h = p.w, p.h
	b.cw, b.ch = (p.w+1)/2, (p.h+1)/2
	if len(b.y) != p.w*p.h {
		b.y = make([]byte, p.w*p.h)
		b.u = make([]byte, b.cw*b.ch)
		b.v = make([]byte, b.cw*b.ch)
	}
	copyPlane(b.y, p.y, p.ys, p.w, p.h)
	copyPlane(b.u, p.u, p.uvs, b.cw, b.ch)
	copyPlane(b.v, p.v, p.uvs, b.cw, b.ch)
}

func copyPlane(dst, src []byte, stride, w, h int) {
	for y := 0; y < h; y++ {
		copy(dst[y*w:(y+1)*w], src[y*stride:y*stride+w])
	}
}

func clamp8(v int32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// BT.709 limited range to RGB, 8-bit fixed point.
func px(dst []byte, yv, u, v int32) {
	c := (yv - 16) * 298
	u -= 128
	v -= 128
	dst[0] = clamp8((c + 459*v + 128) >> 8)
	dst[1] = clamp8((c - 55*u - 136*v + 128) >> 8)
	dst[2] = clamp8((c + 541*u + 128) >> 8)
}

func parallel(h, jobs int, f func(y0, y1 int)) {
	if jobs < 1 {
		jobs = 1
	}
	if h < jobs*8 {
		f(0, h)
		return
	}
	var wg sync.WaitGroup
	for j := 0; j < jobs; j++ {
		y0 := (h * j / jobs) &^ 1
		y1 := (h * (j + 1) / jobs) &^ 1
		if j == jobs-1 {
			y1 = h
		}
		wg.Add(1)
		go func() { defer wg.Done(); f(y0, y1) }()
	}
	wg.Wait()
}

// box2rows writes output rows [r0, r1) of a 2x2 box halving of a w-wide plane.
func box2rows(dst, src []byte, w, r0, r1 int) {
	nw := w / 2
	for y := r0; y < r1; y++ {
		a := src[2*y*w : 2*y*w+2*nw]
		b := src[(2*y+1)*w : (2*y+1)*w+2*nw]
		o := dst[y*nw : (y+1)*nw]
		for x := range o {
			o[x] = byte((int(a[2*x]) + int(a[2*x+1]) + int(b[2*x]) + int(b[2*x+1]) + 2) >> 2)
		}
	}
}

// rgb writes the picture as packed RGB of size tw x th into dst.
// It downscales or upscales as needed: a 2x2 box filter while the source
// is at least 1.5 times the target, then bilinear sampling.
func (b *yuv) rgb(dst []byte, tw, th, jobs int) {
	if tw == b.w && th == b.h {
		w, cw := b.w, b.cw
		parallel(b.h, jobs, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				yr := b.y[y*w : (y+1)*w]
				ur := b.u[(y>>1)*cw:]
				vr := b.v[(y>>1)*cw:]
				o := dst[y*w*3 : (y+1)*w*3]
				for x := 0; x < w; x++ {
					px(o[x*3:x*3+3], int32(yr[x]), int32(ur[x>>1]), int32(vr[x>>1]))
				}
			}
		})
		return
	}
	if tw <= b.w && th <= b.h {
		// Downscale: an area filter on each plane, then the colour conversion.
		cw, ch := (tw+1)/2, (th+1)/2
		if len(b.ry) < tw*th {
			b.ry = make([]byte, tw*th)
		}
		if len(b.ru) < cw*ch {
			b.ru = make([]byte, cw*ch)
			b.rv = make([]byte, cw*ch)
		}
		Y, U, V := b.ry[:tw*th], b.ru[:cw*ch], b.rv[:cw*ch]
		// Exact 2x box steps first, while the plane is still at least twice
		// the target: they cost far less than the general filter.
		reduce := func(dst []byte, dw, dh int, src []byte, sw, sh int, half *[]byte) {
			for sw >= 2*dw && sh >= 2*dh {
				if len(*half) < (sw/2)*(sh/2) {
					*half = make([]byte, (sw/2)*(sh/2))
				}
				h := (*half)[:(sw/2)*(sh/2)]
				s, w0, h0 := src, sw, sh
				parallel(h0/2, jobs, func(r0, r1 int) {
					box2rows(h, s, w0, r0, r1)
				})
				sw, sh = w0/2, h0/2
				src = h
			}
			areaScale(dst, dw, dh, src, sw, sh, &b.tmp, jobs)
		}
		reduce(Y, tw, th, b.y, b.w, b.h, &b.hy)
		reduce(U, cw, ch, b.u, b.cw, b.ch, &b.hu)
		reduce(V, cw, ch, b.v, b.cw, b.ch, &b.hv)
		parallel(th, jobs, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				yr := Y[y*tw : (y+1)*tw]
				ur := U[(y>>1)*cw:]
				vr := V[(y>>1)*cw:]
				o := dst[y*tw*3 : (y+1)*tw*3]
				for x := 0; x < tw; x++ {
					px(o[x*3:x*3+3], int32(yr[x]), int32(ur[x>>1]), int32(vr[x>>1]))
				}
			}
		})
		return
	}
	// Upscale (not used by the layout today): bilinear on luma.
	Y, U, V := b.y, b.u, b.v
	yw, yh, cw, ch := b.w, b.h, b.cw, b.ch
	// Bilinear from (yw,yh) luma and (cw,ch) chroma to (tw,th).
	type tap struct {
		x0, x1 int
		f      int32 // weight of x1, 0..256
		c      int
	}
	taps := make([]tap, tw)
	for x := 0; x < tw; x++ {
		sx := (float64(x)+0.5)*float64(yw)/float64(tw) - 0.5
		if sx < 0 {
			sx = 0
		}
		x0 := int(sx)
		x1 := min(x0+1, yw-1)
		cx := min(int((float64(x)+0.5)*float64(cw)/float64(tw)), cw-1)
		taps[x] = tap{x0, x1, int32((sx - float64(x0)) * 256), cx}
	}
	parallel(th, jobs, func(r0, r1 int) {
		for y := r0; y < r1; y++ {
			sy := (float64(y)+0.5)*float64(yh)/float64(th) - 0.5
			if sy < 0 {
				sy = 0
			}
			y0 := int(sy)
			y1 := min(y0+1, yh-1)
			fy := int32((sy - float64(y0)) * 256)
			cy := min(int((float64(y)+0.5)*float64(ch)/float64(th)), ch-1)
			a := Y[y0*yw : (y0+1)*yw]
			bb := Y[y1*yw : (y1+1)*yw]
			ur := U[cy*cw : (cy+1)*cw]
			vr := V[cy*cw : (cy+1)*cw]
			o := dst[y*tw*3 : (y+1)*tw*3]
			for x, t := range taps {
				top := int32(a[t.x0])*(256-t.f) + int32(a[t.x1])*t.f
				bot := int32(bb[t.x0])*(256-t.f) + int32(bb[t.x1])*t.f
				yv := (top*(256-fy) + bot*fy + 32768) >> 16
				px(o[x*3:x*3+3], yv, int32(ur[t.c]), int32(vr[t.c]))
			}
		}
	})
}
