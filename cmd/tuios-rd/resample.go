//go:build linux || darwin

package main

import "sync"

// An exact area filter: each output sample is the mean of the input
// samples it covers, with partial weights at the edges. For downscaling it
// is as sharp as a filter can be without aliasing.

const wBits = 14

type taps struct {
	first []int32 // first input index of each output sample
	w     [][]int32
}

var tapCache sync.Map // [2]int{src, dst} -> *taps

func areaTaps(src, dst int) *taps {
	key := [2]int{src, dst}
	if t, ok := tapCache.Load(key); ok {
		return t.(*taps)
	}
	t := &taps{first: make([]int32, dst), w: make([][]int32, dst)}
	scale := float64(src) / float64(dst)
	for i := 0; i < dst; i++ {
		a, b := float64(i)*scale, float64(i+1)*scale
		j0 := int(a)
		j1 := min(int(b+0.999999), src)
		ws := make([]int32, 0, j1-j0)
		var sum int32
		big := 0
		for j := j0; j < j1; j++ {
			lo, hi := max(a, float64(j)), min(b, float64(j+1))
			w := int32((hi - lo) / scale * (1 << wBits))
			ws = append(ws, w)
			sum += w
			if w > ws[big] {
				big = len(ws) - 1
			}
		}
		ws[big] += 1<<wBits - sum
		t.first[i], t.w[i] = int32(j0), ws
	}
	tapCache.Store(key, t)
	return t
}

// areaScale resamples an 8-bit plane from sw x sh to dw x dh. tmp is reused.
func areaScale(dst []byte, dw, dh int, src []byte, sw, sh int, tmp *[]uint16, jobs int) {
	tx, ty := areaTaps(sw, dw), areaTaps(sh, dh)
	if len(*tmp) < sh*dw {
		*tmp = make([]uint16, sh*dw)
	}
	t := (*tmp)[:sh*dw]
	// Horizontal: sh rows of sw to sh rows of dw, kept with 8 more bits.
	parallel(sh, jobs, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := src[y*sw : (y+1)*sw]
			out := t[y*dw : (y+1)*dw]
			for x := range out {
				f := int(tx.first[x])
				var acc int32
				for k, w := range tx.w[x] {
					acc += int32(row[f+k]) * w
				}
				out[x] = uint16((acc + 1<<(wBits-9)) >> (wBits - 8))
			}
		}
	})
	// Vertical.
	parallel(dh, jobs, func(y0, y1 int) {
		acc := make([]int32, dw)
		for y := y0; y < y1; y++ {
			for i := range acc {
				acc[i] = 0
			}
			f := int(ty.first[y])
			for k, w := range ty.w[y] {
				row := t[(f+k)*dw : (f+k+1)*dw]
				for x, v := range row {
					acc[x] += int32(v) * w
				}
			}
			out := dst[y*dw : (y+1)*dw]
			for x, v := range acc {
				out[x] = byte((v + 1<<(wBits+7)) >> (wBits + 8))
			}
		}
	})
}
