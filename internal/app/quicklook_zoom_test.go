package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// TestQuickLookSharpReadAfterAReset zooms a PDF page past its pixels, goes
// back to the fit, and zooms the same way again: the second sharper read
// asks for the same size as the first. The answer to the first, stopped,
// read arrives while the second is out, and must not take its place: the
// second read's picture still replaces the page.
//
// NEGATIVE CONTROL: with applyQuickLookSharp matching any answer while a
// read is out, the second answer is dropped and the page keeps the first
// read's pixels.
func TestQuickLookSharpReadAfterAReset(t *testing.T) {
	m := &OS{}
	ql := &m.quickLook
	target := quickLookTarget{path: "/tmp/book.pdf", name: "book.pdf"}
	page := &composer.Image{Width: 1120, Height: 560}
	ql.open, ql.target = true, target
	ql.res = &quickLookResult{target: target, kind: "pdf", img: page, page: 1, pages: 1, baseW: 1120, baseH: 560}
	g := quickLookZoomGeom{availW: 100, availH: 30, cw: 8, ch: 16, bw: 1120, bh: 560, fit: 0.7}

	zoom := func() uint64 {
		t.Helper()
		ql.zoom.scale = 2.5
		if m.quickLookSharpRead(g) == nil {
			t.Fatal("a zoom past the page's pixels asked for no sharper read")
		}
		return ql.zoom.sharpSeq
	}
	first := zoom()
	px := ql.zoom.sharpPx
	ql.quickLookZoomReset()
	second := zoom()
	if ql.zoom.sharpPx != px {
		t.Fatalf("the same zoom asked for %d pixels, then %d: the test needs the same size", px, ql.zoom.sharpPx)
	}

	// The stopped read answers first, with nothing.
	m.applyQuickLookSharp(quickLookSharpMsg{target: target, page: 1, seq: first})
	sharp := &composer.Image{Width: 2560, Height: 1280}
	m.applyQuickLookSharp(quickLookSharpMsg{target: target, page: 1, seq: second, img: sharp})
	if ql.res.img != sharp {
		t.Fatalf("ASSERTION: the second sharper read was dropped: the page is %dx%d, want 2560x1280", ql.res.img.Width, ql.res.img.Height)
	}
}
