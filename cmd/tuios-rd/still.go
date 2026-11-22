//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"time"
)

// A still picture: when no new frame comes for a moment, tuios-rd draws the
// engine's lossless screenshot instead of the last H.264 frame. Text is then
// exact, with full colour resolution.

var errStillBusy = errors.New("the engine is busy with another screenshot")

type stillPicture struct {
	shownN  int
	w, h    int
	r, g, b []byte // planes at the desktop size
	tmp     []uint16
	err     error
}

func (a *app) fetchStill(shownN int, out chan<- stillPicture) {
	sp := stillPicture{shownN: shownN}
	for try := 0; ; try++ {
		req, _ := http.NewRequestWithContext(a.ctx, "GET", a.cfg.base+"/screenshot.png"+a.outputQuery("?"), nil)
		if a.cfg.token != "" {
			req.Header.Set("Authorization", "Bearer "+a.cfg.token)
		}
		cl := &http.Client{Timeout: 5 * time.Second}
		resp, err := cl.Do(req)
		if err != nil {
			sp.err = err
			break
		}
		if resp.StatusCode == http.StatusServiceUnavailable && try < 2 {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			time.Sleep(150 * time.Millisecond)
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			sp.err = errStillBusy
			if resp.StatusCode != http.StatusServiceUnavailable {
				sp.err = fmt.Errorf("screenshot: %s", resp.Status)
			}
			break
		}
		img, err := png.Decode(resp.Body)
		resp.Body.Close()
		if err != nil {
			sp.err = err
			break
		}
		sp.fromImage(img)
		break
	}
	a.stillMS.Store(0)
	out <- sp
}

func (sp *stillPicture) fromImage(img image.Image) {
	b := img.Bounds()
	sp.w, sp.h = b.Dx(), b.Dy()
	n := sp.w * sp.h
	sp.r, sp.g, sp.b = make([]byte, n), make([]byte, n), make([]byte, n)
	switch m := img.(type) {
	case *image.RGBA:
		for y := 0; y < sp.h; y++ {
			row := m.Pix[y*m.Stride:]
			for x := 0; x < sp.w; x++ {
				i := y*sp.w + x
				sp.r[i], sp.g[i], sp.b[i] = row[x*4], row[x*4+1], row[x*4+2]
			}
		}
	case *image.NRGBA:
		for y := 0; y < sp.h; y++ {
			row := m.Pix[y*m.Stride:]
			for x := 0; x < sp.w; x++ {
				i := y*sp.w + x
				sp.r[i], sp.g[i], sp.b[i] = row[x*4], row[x*4+1], row[x*4+2]
			}
		}
	default:
		for y := 0; y < sp.h; y++ {
			for x := 0; x < sp.w; x++ {
				r, g, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				i := y*sp.w + x
				sp.r[i], sp.g[i], sp.b[i] = byte(r>>8), byte(g>>8), byte(bb>>8)
			}
		}
	}
}

// scale writes the picture as packed RGB of w x h into dst, with the same
// area filter as the video frames.
func (sp *stillPicture) scale(dst []byte, w, h, jobs int) {
	planes := [3][]byte{sp.r, sp.g, sp.b}
	n := w * h
	for c, p := range planes {
		out := p
		if w != sp.w || h != sp.h {
			out = make([]byte, n)
			src, sw, sh := p, sp.w, sp.h
			for sw >= 2*w && sh >= 2*h {
				half := make([]byte, (sw/2)*(sh/2))
				s, w0 := src, sw
				parallel(sh/2, jobs, func(r0, r1 int) { box2rows(half, s, w0, r0, r1) })
				src, sw, sh = half, sw/2, sh/2
			}
			areaScale(out, w, h, src, sw, sh, &sp.tmp, jobs)
		}
		for i := 0; i < n; i++ {
			dst[i*3+c] = out[i]
		}
	}
}

// isBlack says whether a picture is black all over, as a capture of a
// display that is off is: no luma above 24 (16 is black). It reads every
// row and every other column, and stops at the first bright pixel, so a
// normal picture costs almost nothing.
func isBlack(b *yuv) bool {
	for y := 0; y < b.h; y++ {
		row := b.y[y*b.w : (y+1)*b.w]
		for x := 0; x < len(row); x += 2 {
			if row[x] > 24 {
				return false
			}
		}
	}
	return true
}

func (a *app) noDisplayText() string {
	if b, _ := a.backend.Load().(string); b == "macos" {
		return "The Mac has no active display. Open the lid, connect a screen, or add a virtual display."
	}
	return "The desktop has no picture. Check that its display is on."
}

// blockMeans returns the mean luma of each 16x16 block.
func blockMeans(b *yuv) []uint16 {
	bw, bh := (b.w+15)/16, (b.h+15)/16
	m := make([]uint16, bw*bh)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			var s, n int
			for y := by * 16; y < min(by*16+16, b.h); y++ {
				row := b.y[y*b.w:]
				for x := bx * 16; x < min(bx*16+16, b.w); x++ {
					s += int(row[x])
					n++
				}
			}
			m[by*bw+bx] = uint16(s * 16 / max(n, 1)) // 4 bits of fraction
		}
	}
	return m
}

// visiblyChanged says whether b differs from the picture the baseline was
// taken of by more than an encoder refinement does: a block mean that moved
// by more than 2 levels.
func visiblyChanged(baseline []uint16, b *yuv) bool {
	m := blockMeans(b)
	if len(m) != len(baseline) {
		return true
	}
	for i, v := range m {
		d := int(v) - int(baseline[i])
		if d > 32 || d < -32 {
			return true
		}
	}
	return false
}
