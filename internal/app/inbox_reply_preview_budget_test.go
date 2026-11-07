package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// TestReplyPreviewHoldsItsBytes holds the memory quick look's picture cache
// keeps. A count alone let 64 panel-size photos hold over 300 MB in the
// client, so the cache is also bounded by the bytes its pictures hold, and a
// picture on screen is never dropped, or two large ones on screen would be
// prepared again every frame. Each picture holds a buffer larger than its
// PNG, which size() charges because it counts cap.
func TestReplyPreviewHoldsItsBytes(t *testing.T) {
	const budget = 32 << 20
	picture := func(held int) *previewPicture {
		return &previewPicture{png: make([]byte, 1<<10, held)}
	}
	key := func(i int) previewKey {
		return previewKey{cols: i + 1, rows: 1, mode: composer.PreviewPixels}
	}
	heldBytes := func(p *replyPreview) int {
		n := 0
		for _, pic := range p.pics {
			if pic != nil {
				n += cap(pic.png)
				for _, l := range pic.lines {
					n += len(l)
				}
			}
		}
		return n
	}

	t.Run("large pictures stay under the byte bound", func(t *testing.T) {
		m := &OS{}
		for i := range 20 {
			m.applyReplyPreview(replyPreviewMsg{pics: map[previewKey]*previewPicture{key(i): picture(4 << 20)}})
		}
		p := m.replyPreviewer()
		check := func(when string) {
			t.Helper()
			if len(p.pics) != 8 || len(p.order) != 8 {
				t.Fatalf("%s: %d pictures and %d in order kept, want the 8 newest", when, len(p.pics), len(p.order))
			}
			for i := range 20 {
				_, kept := p.pics[key(i)]
				if kept != (i >= 12) {
					t.Errorf("%s: picture %d of 20 kept is %v, want only the 8 newest", when, i+1, kept)
				}
			}
			if got := heldBytes(p); got != budget {
				t.Errorf("%s: the pictures hold %d bytes, want %d", when, got, budget)
			}
		}
		check("after 20 pictures")
		m.applyReplyPreview(replyPreviewMsg{pics: map[previewKey]*previewPicture{key(19): picture(4 << 20)}})
		check("after the newest is replaced")
	})

	t.Run("glyph pictures count by their lines", func(t *testing.T) {
		m := &OS{}
		for i := range 40 {
			lines := []string{strings.Repeat("x", 512<<10), strings.Repeat("y", 512<<10)}
			m.applyReplyPreview(replyPreviewMsg{pics: map[previewKey]*previewPicture{key(i): {lines: lines, png: make([]byte, 16)}}})
		}
		p := m.replyPreviewer()
		if got := heldBytes(p); got > budget {
			t.Errorf("the glyph pictures hold %d bytes, want at most %d", got, budget)
		}
		if len(p.pics) != len(p.order) {
			t.Errorf("%d pictures but %d in order", len(p.pics), len(p.order))
		}
		for i := range 40 {
			if _, kept := p.pics[key(i)]; kept != (i >= 40-len(p.pics)) {
				t.Errorf("glyph picture %d of 40 kept is %v, want only the newest", i+1, kept)
			}
		}
		if _, ok := p.pics[key(39)]; !ok {
			t.Errorf("the newest glyph picture is gone")
		}
	})

	t.Run("pictures on screen are not dropped", func(t *testing.T) {
		a, b := key(100), key(101)
		m := &OS{}
		p := m.replyPreviewer()
		prepares := 0
		for range 10 {
			p.shown = make(map[previewID]previewPlacement)
			for i, k := range []previewKey{a, b} {
				if _, ok := p.pics[k]; ok {
					p.shown[previewID{img: uint32(i + 1), placement: 1}] = previewPlacement{key: k, cols: k.cols, rows: k.rows}
				}
			}
			for _, k := range []previewKey{a, b} {
				if _, ok := p.pics[k]; !ok {
					m.applyReplyPreview(replyPreviewMsg{pics: map[previewKey]*previewPicture{k: picture(20 << 20)}})
					prepares++
				}
			}
		}
		if prepares > 3 {
			t.Errorf("two pictures on screen were prepared %d times in 10 frames, want at most 3", prepares)
		}
		for _, k := range []previewKey{a, b} {
			if _, ok := p.pics[k]; !ok {
				t.Errorf("a picture on screen is gone after 10 frames")
			}
		}

		m = &OS{}
		m.applyReplyPreview(replyPreviewMsg{pics: map[previewKey]*previewPicture{a: picture(20 << 20), b: picture(20 << 20)}})
		p = m.replyPreviewer()
		for _, k := range []previewKey{a, b} {
			if _, ok := p.pics[k]; !ok {
				t.Errorf("a picture of a message with two large ones is gone")
			}
		}
	})

	t.Run("thumbnails all stay", func(t *testing.T) {
		m := &OS{}
		p := m.replyPreviewer()
		undecodable := key(200)
		for i := range 21 {
			msg := replyPreviewMsg{pics: map[previewKey]*previewPicture{key(i): picture(10 << 10)}}
			if i == 20 {
				msg = replyPreviewMsg{pics: map[previewKey]*previewPicture{undecodable: nil}}
			}
			before := p.Version()
			m.applyReplyPreview(msg)
			if got := p.Version(); got != before+1 {
				t.Errorf("apply %d moved the version from %d to %d, want one step", i+1, before, got)
			}
		}
		for i := range 20 {
			if _, ok := p.pics[key(i)]; !ok {
				t.Errorf("thumbnail %d of 20 is gone", i+1)
			}
		}
		if pic, ok := p.pics[undecodable]; !ok || pic != nil {
			t.Errorf("an image that cannot be decoded is not kept as blank: %v, %v", pic, ok)
		}
	})
}

// TestReplyPreviewHoldsItsCount holds the quick look's picture cache to 64
// pictures however small they are, the newest kept, and keeps a picture on
// screen past that bound.
func TestReplyPreviewHoldsItsCount(t *testing.T) {
	key := func(i int) previewKey {
		return previewKey{cols: i, rows: 1, mode: composer.PreviewPixels}
	}
	m := &OS{}
	p := m.replyPreviewer()
	apply := func(from, to int) {
		for i := from; i <= to; i++ {
			m.applyReplyPreview(replyPreviewMsg{pics: map[previewKey]*previewPicture{key(i): {png: make([]byte, 64)}}})
		}
	}
	check := func(when string, kept func(i int) bool, last int) {
		t.Helper()
		if len(p.pics) != 64 || len(p.order) != 64 {
			t.Fatalf("%s: %d pictures and %d in order kept, want 64", when, len(p.pics), len(p.order))
		}
		for _, k := range p.order {
			if _, ok := p.pics[k]; !ok {
				t.Errorf("%s: key %d is in order but not kept", when, k.cols)
			}
		}
		sum := 0
		for i := 1; i <= last; i++ {
			pic, ok := p.pics[key(i)]
			if ok != kept(i) {
				t.Errorf("%s: picture %d kept is %v, want %v", when, i, ok, kept(i))
			}
			if ok {
				sum += cap(pic.png)
			}
		}
		if p.held != sum || p.held != 64*64 {
			t.Errorf("%s: held is %d bytes, want %d: the kept pictures' sizes add up to %d", when, p.held, 64*64, sum)
		}
	}

	apply(1, 100)
	check("after 100 small pictures", func(i int) bool { return i >= 37 }, 100)

	p.shown[previewID{img: 1, placement: 1}] = previewPlacement{key: key(37), cols: 37, rows: 1}
	apply(101, 170)
	check("after 70 more with picture 37 on screen", func(i int) bool { return i == 37 || i >= 108 }, 170)
}

// TestPreparePictureKeepsThePNGExact holds a prepared picture's PNG at its
// length, so a cached picture holds no spare buffer. The picture is noise, so
// the PNG stays large: 24 by 12 cells of 8 by 16 pixels make a 192x192 PNG of
// about 108 KiB. bytes.Buffer grows by doubling, so at that size its spare
// capacity is far more than allocator rounding: kept as b.Bytes(), the PNG had
// a cap of 163,840 bytes for a len of 110,895 (measured with go1.27.3).
func TestPreparePictureKeepsThePNGExact(t *testing.T) {
	const side = 192
	src := image.NewNRGBA(image.Rect(0, 0, side, side))
	seed := uint32(1)
	for i := range src.Pix {
		seed = seed*1664525 + 1013904223
		src.Pix[i] = uint8(seed >> 24)
	}
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}
	img := &composer.Image{Data: b.Bytes(), Width: side, Height: side}
	k := previewKey{cols: 24, rows: 12, mode: composer.PreviewPixels}
	pic := preparePicture(img, k, 8, 16, color.Black)
	if pic == nil {
		t.Fatalf("preparePicture returned nil for a %dx%d PNG", side, side)
	}
	dec, err := png.Decode(bytes.NewReader(pic.png))
	if err != nil {
		t.Fatalf("the picture is not a PNG: %v", err)
	}
	if bd := dec.Bounds(); bd.Dx() != side || bd.Dy() != side {
		t.Errorf("the PNG is %dx%d pixels, want %dx%d for 24x12 cells of 8x16", bd.Dx(), bd.Dy(), side, side)
	}
	if len(pic.png) < 64<<10 {
		t.Fatalf("the PNG is %d bytes, want over 64 KiB so a doubled buffer leaves spare capacity", len(pic.png))
	}
	if cap(pic.png) != len(pic.png) {
		t.Errorf("the cached PNG has cap %d for len %d: it keeps %d spare bytes of the encoder's buffer, want cap equal to len",
			cap(pic.png), len(pic.png), cap(pic.png)-len(pic.png))
	}
}
