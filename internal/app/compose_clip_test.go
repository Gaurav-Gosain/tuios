package app

import (
	"image"
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// blitClippedReference is blitClipped with no fast path: the layer's span in
// the clip cleared, then each head cell set, through Line.Set. It is the
// oracle the copy has to match.
func blitClippedReference(cl *cellLayer, canvas *frameCanvas, x, y int, clip image.Rectangle) {
	for row := range cl.h {
		cy := y + row
		if cy < clip.Min.Y || cy >= clip.Max.Y || cy < 0 || cy >= len(canvas.Lines) {
			continue
		}
		line := canvas.Lines[cy]
		src := cl.buf.Lines[row]
		from, to := max(x, clip.Min.X), min(x+cl.w, clip.Max.X)
		for cx := from; cx < to; cx++ {
			line.Set(cx, nil)
		}
		for cx := from; cx < to; cx++ {
			if c := &src[cx-x]; !c.IsZero() {
				line.Set(cx, c)
			}
		}
	}
}

// randomCellLayer parses a random block of narrow and wide glyphs.
func randomCellLayer(rng *rand.Rand, w, h int) *cellLayer {
	glyphs := []string{"a", " ", "界", "é", "█", "👍"}
	styles := []lipgloss.Style{
		lipgloss.NewStyle(),
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		lipgloss.NewStyle().Background(lipgloss.Color("#101010")),
	}
	var sb strings.Builder
	for r := range h {
		if r > 0 {
			sb.WriteByte('\n')
		}
		for range rng.Intn(w + 2) {
			sb.WriteString(styles[rng.Intn(len(styles))].Render(glyphs[rng.Intn(len(glyphs))]))
		}
	}
	cl := &cellLayer{w: -1}
	cl.update(sb.String(), w, h, &layerFill{})
	return cl
}

// TestBlitClippedMatchesCellByCell draws random layers, with wide glyphs on
// both the canvas and the layer, through random clips, and checks that the
// copy path leaves the same canvas as setting each cell.
func TestBlitClippedMatchesCellByCell(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := range 2000 {
		cw, ch := 6+rng.Intn(14), 1+rng.Intn(5)
		under := randomCellLayer(rng, cw, ch)
		over := randomCellLayer(rng, 2+rng.Intn(cw), 1+rng.Intn(ch))
		x, y := rng.Intn(cw+4)-2, rng.Intn(ch+2)-1
		x0, y0 := rng.Intn(cw+2)-1, rng.Intn(ch+1)-1
		clip := image.Rect(x0, y0, x0+rng.Intn(cw+2), y0+1+rng.Intn(ch+1))

		var got, want frameCanvas
		for _, c := range []*frameCanvas{&got, &want} {
			c.Resize(cw, ch)
			c.Clear()
			under.blit(c, 0, 0)
		}
		over.blitClipped(&got, x, y, clip)
		blitClippedReference(over, &want, x, y, clip)
		if g, w := got.Render(), want.Render(); g != w {
			t.Fatalf("round %d: layer at (%d,%d) clip %v\nwant %q\ngot  %q", round, x, y, clip, w, g)
		}
	}
}

// clippedPaneLayer is a full screen of text and the clip of a session view
// one column narrower, the case blitClipped is drawn for on every frame.
func clippedPaneLayer(tb testing.TB) (*cellLayer, *frameCanvas, image.Rectangle) {
	tb.Helper()
	row := strings.Repeat("the quick brown fox jumps over the lazy dog ", realCols/44+1)[:realCols]
	content := strings.TrimSuffix(strings.Repeat(row+"\n", realRows), "\n")
	cl := &cellLayer{w: -1}
	cl.update(content, realCols, realRows, &layerFill{})
	canvas := &frameCanvas{}
	canvas.Resize(realCols, realRows)
	canvas.Clear()
	return cl, canvas, image.Rect(0, 0, realCols-1, realRows)
}

// BenchmarkBlitClipped measures the copy of a pane layer that crosses the
// clip of a session view, against blit of the same layer with no clip.
func BenchmarkBlitClipped(b *testing.B) {
	cl, canvas, clip := clippedPaneLayer(b)
	b.Run("clipped", func(b *testing.B) {
		for b.Loop() {
			cl.blitClipped(canvas, 0, 0, clip)
		}
	})
	b.Run("blit", func(b *testing.B) {
		for b.Loop() {
			cl.blit(canvas, 0, 0)
		}
	})
}

// TestBlitClippedBudget holds blitClipped to the cost of blit. A clipped pane
// is drawn on every frame of a session view, and the cell-by-cell copy cost
// three times as much as blit. The budget is a ratio of the two, measured on
// the same machine in the same run, so a loaded machine slows both.
func TestBlitClippedBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing budget")
	}
	if raceEnabled {
		t.Skip("timings are not meaningful under -race")
	}
	cl, canvas, clip := clippedPaneLayer(t)
	best := func(fn func()) float64 {
		var ns float64
		for i := range 2 {
			r := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					fn()
				}
			})
			if v := float64(r.NsPerOp()); i == 0 || v < ns {
				ns = v
			}
		}
		return ns
	}
	clipped := best(func() { cl.blitClipped(canvas, 0, 0, clip) })
	plain := best(func() { cl.blit(canvas, 0, 0) })
	t.Logf("blitClipped %.0f ns/op, blit %.0f ns/op, ratio %.2f", clipped, plain, clipped/plain)
	if clipped > 2*plain {
		t.Errorf("blitClipped costs %.1fx blit, want at most 2x", clipped/plain)
	}
}
