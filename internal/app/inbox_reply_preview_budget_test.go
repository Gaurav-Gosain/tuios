package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// TestReplyPreviewHoldsItsBytes holds the memory quick look's picture cache
// keeps. A count alone let 64 panel-size photos hold over 300 MB in the
// client, so the cache is also bounded by the bytes its pictures hold, and a
// picture on screen is never dropped, or two large ones on screen would be
// prepared again every frame. Each picture is made the way bytes.Buffer
// leaves PNG bytes: a short slice over a larger buffer, all of it held.
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
