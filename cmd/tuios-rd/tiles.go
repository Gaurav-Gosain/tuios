//go:build linux || darwin

package main

// The lossless tiles codec of the screen-relay fork (TILES-FORMAT.md): each
// type 8 message carries ops (copy, pixels, fill) for an RGB frame buffer at
// the desktop size, and one zstd frame with the pixels.

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	tilesFlagKey     = 1
	tilesFlagPartial = 4 // more frames follow for the same capture
)

// tilesBuf is the desktop as the engine last described it, and the
// rectangles that changed since the display loop last looked.
type tilesBuf struct {
	mu           sync.Mutex
	w, h         int
	pix          []byte   // RGB, 3 bytes a pixel
	log          []tileOp // what changed since the display loop last looked, in order
	all          bool     // the whole buffer changed (keyframe, new size)
	partial      bool
	partialSince time.Time // first partial frame in a row
	ready        bool      // at least one keyframe applied
	f            frame
}

// tileOp is one change to the buffer: a copy from (sx, sy) to r, or new
// pixels in r.
type tileOp struct {
	copy   bool
	sx, sy int
	r      rect
}

type tilesDecoder struct {
	z       *zstd.Decoder
	blob    []byte
	scratch []byte
}

func newTilesDecoder() *tilesDecoder {
	z, _ := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
	return &tilesDecoder{z: z}
}

func u16(b []byte) int { return int(binary.BigEndian.Uint16(b)) }

// apply applies one type 8 payload to t. On an error the buffer can be
// partly changed: ask for a keyframe.
func (d *tilesDecoder) apply(t *tilesBuf, p []byte) error {
	if len(p) < 20 || p[0] != 1 {
		return fmt.Errorf("tiles: bad header")
	}
	flags := p[1]
	w, h := u16(p[2:]), u16(p[4:])
	n := u16(p[14:])
	bl := int(binary.BigEndian.Uint32(p[16:]))
	ops := p[20:]
	// Walk the ops once to find where the blob starts and how big its
	// content must be.
	off, want := 0, 0
	for i := 0; i < n; i++ {
		if off >= len(ops) {
			return fmt.Errorf("tiles: ops cut short")
		}
		switch ops[off] {
		case 1:
			off += 13
		case 2:
			if off+9 > len(ops) {
				return fmt.Errorf("tiles: ops cut short")
			}
			want += u16(ops[off+5:]) * u16(ops[off+7:]) * 3
			off += 9
		case 3:
			off += 12
		default:
			return fmt.Errorf("tiles: unknown op %d", ops[off])
		}
	}
	if off > len(ops) || len(ops)-off != bl {
		return fmt.Errorf("tiles: blob length %d, %d bytes left", bl, len(ops)-off)
	}
	var blob []byte
	if bl > 0 {
		var err error
		if d.blob, err = d.z.DecodeAll(ops[off:], d.blob[:0]); err != nil {
			return fmt.Errorf("tiles: zstd: %v", err)
		}
		blob = d.blob
	}
	if len(blob) != want {
		return fmt.Errorf("tiles: blob holds %d bytes, ops need %d", len(blob), want)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if flags&tilesFlagKey != 0 || w != t.w || h != t.h {
		if flags&tilesFlagKey == 0 {
			return fmt.Errorf("tiles: size changed without a keyframe")
		}
		t.w, t.h = w, h
		if len(t.pix) != w*h*3 {
			t.pix = make([]byte, w*h*3)
		} else {
			clear(t.pix)
		}
		t.all, t.log, t.ready = true, t.log[:0], true
	}
	if !t.ready {
		return fmt.Errorf("tiles: no keyframe yet")
	}
	in := func(x, y, rw, rh int) bool { return rw > 0 && rh > 0 && x+rw <= w && y+rh <= h }
	bo := 0
	for i, o := 0, 0; i < n; i++ {
		switch ops[o] {
		case 1:
			sx, sy, dx, dy, rw, rh := u16(ops[o+1:]), u16(ops[o+3:]), u16(ops[o+5:]), u16(ops[o+7:]), u16(ops[o+9:]), u16(ops[o+11:])
			if !in(sx, sy, rw, rh) || !in(dx, dy, rw, rh) {
				return fmt.Errorf("tiles: copy out of bounds")
			}
			t.copyRect(sx, sy, dx, dy, rw, rh, &d.scratch)
			t.log = append(t.log, tileOp{copy: true, sx: sx, sy: sy, r: rect{dx, dy, rw, rh}})
			o += 13
		case 2:
			x, y, rw, rh := u16(ops[o+1:]), u16(ops[o+3:]), u16(ops[o+5:]), u16(ops[o+7:])
			if !in(x, y, rw, rh) {
				return fmt.Errorf("tiles: pixels out of bounds")
			}
			for r := 0; r < rh; r++ {
				copy(t.pix[((y+r)*w+x)*3:((y+r)*w+x+rw)*3], blob[bo:bo+rw*3])
				bo += rw * 3
			}
			t.log = append(t.log, tileOp{r: rect{x, y, rw, rh}})
			o += 9
		case 3:
			x, y, rw, rh := u16(ops[o+1:]), u16(ops[o+3:]), u16(ops[o+5:]), u16(ops[o+7:])
			if !in(x, y, rw, rh) {
				return fmt.Errorf("tiles: fill out of bounds")
			}
			c := ops[o+9 : o+12]
			for r := 0; r < rh; r++ {
				row := t.pix[((y+r)*w+x)*3 : ((y+r)*w+x+rw)*3]
				for k := 0; k < len(row); k += 3 {
					row[k], row[k+1], row[k+2] = c[0], c[1], c[2]
				}
			}
			t.log = append(t.log, tileOp{r: rect{x, y, rw, rh}})
			o += 12
		}
	}
	if len(t.log) > 1024 { // many small rectangles: redraw all
		t.all, t.log = true, t.log[:0]
	}
	t.partial = flags&tilesFlagPartial != 0
	if !t.partial {
		t.partialSince = time.Time{}
	} else if t.partialSince.IsZero() {
		t.partialSince = time.Now()
	}
	return nil
}

// copyRect copies with memmove semantics: as if the whole source was read
// before the write.
func (t *tilesBuf) copyRect(sx, sy, dx, dy, rw, rh int, scratch *[]byte) {
	w := t.w
	if sy == dy && sx != dx {
		for r := 0; r < rh; r++ { // Go's copy is a memmove within a row
			row := (sy + r) * w
			copy(t.pix[(row+dx)*3:(row+dx+rw)*3], t.pix[(row+sx)*3:(row+sx+rw)*3])
		}
		return
	}
	if dy > sy { // bottom up, so no source row is overwritten before it is read
		for r := rh - 1; r >= 0; r-- {
			copy(t.pix[((dy+r)*w+dx)*3:((dy+r)*w+dx+rw)*3], t.pix[((sy+r)*w+sx)*3:((sy+r)*w+sx+rw)*3])
		}
		return
	}
	for r := 0; r < rh; r++ {
		copy(t.pix[((dy+r)*w+dx)*3:((dy+r)*w+dx+rw)*3], t.pix[((sy+r)*w+sx)*3:((sy+r)*w+sx+rw)*3])
	}
}
