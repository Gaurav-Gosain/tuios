package app

import (
	"image/color"
	"math/rand"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// randomStyle draws a style from a small palette that covers every kind of
// colour a cell can carry, including none, and every attribute bit.
func randomStyle(rng *rand.Rand) uv.Style {
	pick := func() color.Color {
		switch rng.Intn(6) {
		case 0:
			return nil
		case 1:
			return ansi.BasicColor(rng.Intn(16))
		case 2:
			return ansi.IndexedColor(rng.Intn(256))
		case 3:
			return color.RGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: 255}
		case 4:
			return ansi.TrueColor(rng.Uint32() & 0xffffff)
		default:
			return ansi.Red
		}
	}
	var s uv.Style
	if rng.Intn(3) > 0 {
		s.Fg = pick()
	}
	if rng.Intn(3) > 0 {
		s.Bg = pick()
	}
	if rng.Intn(6) == 0 {
		s.UnderlineColor = pick()
	}
	if rng.Intn(4) == 0 {
		s.Underline = uv.Underline(rng.Intn(6))
	}
	if rng.Intn(3) == 0 {
		s.Attrs = uint8(rng.Intn(256))
	}
	return s
}

// randomLines fills a buffer the way a composed frame is filled: mostly
// blanks and ASCII, with runs of styled cells, wide glyphs and their
// placeholders, links, and the odd carriage return.
func randomLines(rng *rand.Rand, w, h int) []uv.Line {
	glyphs := []string{"a", "b", "x", " ", "-", "│", "█", "é", "\t"}
	lines := make([]uv.Line, h)
	for y := range lines {
		line := make(uv.Line, w)
		for x := range line {
			line[x] = uv.EmptyCell
		}
		x := 0
		style := uv.Style{}
		link := uv.Link{}
		for x < w {
			if rng.Intn(4) == 0 {
				style = randomStyle(rng)
			}
			if rng.Intn(12) == 0 {
				if rng.Intn(2) == 0 {
					link = uv.Link{}
				} else {
					link = uv.Link{URL: "https://x.test/" + string(rune('a'+rng.Intn(3))), Params: []string{"", "id=1"}[rng.Intn(2)]}
				}
			}
			switch rng.Intn(10) {
			case 0:
				// A blank cell, which resets the pen.
				line[x] = uv.EmptyCell
				x++
			case 1:
				// A styled space, which is not blank.
				line[x] = uv.Cell{Content: " ", Width: 1, Style: style, Link: link}
				x++
			case 2:
				if x+1 < w {
					line[x] = uv.Cell{Content: "界", Width: 2, Style: style, Link: link}
					line[x+1] = uv.Cell{}
					x += 2
				} else {
					x++
				}
			case 3:
				// A stray zero cell, as after a wide cell was clipped.
				line[x] = uv.Cell{}
				x++
			case 4:
				if x == w-1 && rng.Intn(2) == 0 {
					line[x] = uv.Cell{Content: "\r", Width: 1}
					x++
					break
				}
				fallthrough
			default:
				line[x] = uv.Cell{Content: glyphs[rng.Intn(len(glyphs))], Width: 1, Style: style, Link: link}
				x++
			}
		}
		lines[y] = line
	}
	return lines
}

// TestFrameRenderMatchesUltraviolet is the renderer's definition: over random
// frames its bytes are ultraviolet's trimmed bytes.
func TestFrameRenderMatchesUltraviolet(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	var r frameRenderer
	for round := range 400 {
		w, h := 1+rng.Intn(40), 1+rng.Intn(8)
		lines := randomLines(rng, w, h)
		want := uv.TrimSpace(uv.Lines(lines).Render())
		got := r.render(lines)
		if got != want {
			t.Fatalf("round %d (%dx%d): frame differs\nwant %q\ngot  %q", round, w, h, want, got)
		}
	}
	if got := r.render(nil); got != "" {
		t.Fatalf("empty frame rendered %q", got)
	}

	// Two colours of different Go types with the same RGBA are the same
	// colour to ultraviolet, so no SGR is emitted between them. A renderer
	// that compared the structs alone would emit one.
	same := uv.Line{
		{Content: "a", Width: 1, Style: uv.Style{Fg: color.RGBA{R: 255, A: 255}}},
		{Content: "b", Width: 1, Style: uv.Style{Fg: ansi.TrueColor(0xff0000)}},
		{Content: "c", Width: 1, Style: uv.Style{Bg: ansi.IndexedColor(9), Fg: color.RGBA{R: 255, A: 255}}},
		{Content: "d", Width: 1, Style: uv.Style{Bg: ansi.IndexedColor(9), Fg: ansi.TrueColor(0xff0000)}},
	}
	want := uv.TrimSpace(uv.Lines{same}.Render())
	if got := r.render([]uv.Line{same}); got != want {
		t.Fatalf("RGBA-equal colours of different types\nwant %q\ngot  %q", want, got)
	}
}

// TestFrameRenderMemoIsExact: a transition seen twice renders the same bytes
// as one seen once, and a memo that fills up starts over rather than growing.
func TestFrameRenderMemoIsExact(t *testing.T) {
	var r frameRenderer
	a := uv.Style{Fg: ansi.Red}
	b := uv.Style{Fg: ansi.Blue, Attrs: uv.AttrBold}
	first := r.diff(&a, &b)
	if first != uv.StyleDiff(&a, &b) {
		t.Fatalf("diff %q, want %q", first, uv.StyleDiff(&a, &b))
	}
	if again := r.diff(&a, &b); again != first {
		t.Fatalf("memoised diff %q, want %q", again, first)
	}
	for i := range maxDiffMemo + 10 {
		s := uv.Style{Fg: ansi.IndexedColor(i % 256), Attrs: uint8(i / 256)}
		r.diff(&a, &s)
	}
	if len(r.diffs) > maxDiffMemo {
		t.Fatalf("memo grew to %d entries", len(r.diffs))
	}
}
