package vt

import (
	"image/color"
	"math/rand"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// TestPenDiffMatchesStyleDiff holds the palette fast path to uv's diff byte
// for byte. Half the pairs differ only in the foreground or only in the
// background, which is the case the table answers; the rest mix nil,
// basic, indexed, RGBA and truecolor on every colour with random attributes
// and underline styles, which must all reach uv unchanged.
func TestPenDiffMatchesStyleDiff(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	col := func() color.Color {
		switch rng.Intn(6) {
		case 0:
			return nil
		case 1:
			return ansi.BasicColor(rng.Intn(16))
		case 2:
			return ansi.IndexedColor(rng.Intn(256))
		case 3:
			return color.RGBA{R: uint8(rng.Intn(256)), G: 1, B: 2, A: 255}
		case 4:
			// The RGBA of a palette colour, as a theme resolves one.
			r, g, b, _ := ansi.IndexedColor(rng.Intn(256)).RGBA()
			return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255}
		}
		return ansi.TrueColor(rng.Intn(1 << 24))
	}
	style := func() uv.Style {
		return uv.Style{Fg: col(), Bg: col(), UnderlineColor: col(), Attrs: uint8(rng.Intn(256)), Underline: uv.Underline(rng.Intn(6))}
	}
	n := 2000000
	if testing.Short() {
		n = 100000
	}
	for range n {
		from, to := style(), style()
		switch rng.Intn(4) {
		case 0:
			to.Attrs, to.Underline, to.UnderlineColor, to.Bg = from.Attrs, from.Underline, from.UnderlineColor, from.Bg
		case 1:
			to.Attrs, to.Underline, to.UnderlineColor, to.Fg = from.Attrs, from.Underline, from.UnderlineColor, from.Fg
		}
		if got, want := penDiff(&from, &to), to.Diff(&from); got != want {
			t.Fatalf("%+v -> %+v: got %q, want %q", from, to, got, want)
		}
	}
}

// TestPaletteSGRChangesAllocateNothing pins the reason for penDiff: a pane
// drawn in palette colours changes its pen at nearly every run, and each
// change used to allocate.
func TestPaletteSGRChangesAllocateNothing(t *testing.T) {
	from := uv.Style{Fg: ansi.BasicColor(1), Bg: ansi.IndexedColor(236), Attrs: uv.AttrBold}
	fg, bg := from, from
	fg.Fg = ansi.IndexedColor(150)
	bg.Bg = ansi.BasicColor(4)
	if got := testing.AllocsPerRun(100, func() {
		_ = penDiff(&from, &fg)
		_ = penDiff(&from, &bg)
	}); got > 0 {
		t.Errorf("a palette colour change allocates %.1f times, want 0", got)
	}
}
