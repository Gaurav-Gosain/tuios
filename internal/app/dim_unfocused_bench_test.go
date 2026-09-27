package app

import (
	"image/color"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// BenchmarkDimUnfocusedRuns is the perf budget for the dim's blend: one
// unfocused pane's worth of style runs (40 rows of 12 runs) in the handful of
// colours a real pane repeats, dimmed the way renderTerminal dims them, with
// one memo per pane render. The blend moved from sRGB to OKLab, which costs
// cube roots per call; the memo is what keeps a render at one blend per pair.
// docs/perf.md records the numbers.
func BenchmarkDimUnfocusedRuns(b *testing.B) {
	inks := []color.Color{
		color.RGBA{R: 0xe5, G: 0xe5, B: 0xe5, A: 0xff}, color.RGBA{R: 0x7a, G: 0xa2, B: 0xf7, A: 0xff},
		color.RGBA{R: 0x9e, G: 0xce, B: 0x6a, A: 0xff}, color.RGBA{R: 0xf7, G: 0x76, B: 0x8e, A: 0xff},
		color.RGBA{R: 0xe0, G: 0xaf, B: 0x68, A: 0xff}, color.RGBA{R: 0xbb, G: 0x9a, B: 0xf7, A: 0xff},
	}
	cells := make([]uv.Cell, 40*12)
	for i := range cells {
		cells[i] = uv.Cell{Content: "x", Width: 1}
		cells[i].Style.Fg = inks[i%len(inks)]
	}
	// Held as interfaces, the way renderTerminal holds the pane's dim ground,
	// so the benchmark does not box them on every call.
	var fg, bg color.Color = color.RGBA{R: 0xe5, G: 0xe5, B: 0xe5, A: 0xff}, color.RGBA{R: 0x1a, G: 0x1b, B: 0x26, A: 0xff}
	for _, memo := range []bool{true, false} {
		name := "memo"
		if !memo {
			name = "uncached"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			var scratch uv.Cell
			for b.Loop() {
				var bm blendMemo
				m := &bm
				if !memo {
					m = nil
				}
				for i := range cells {
					_ = dimCell(&scratch, &cells[i], fg, bg, 0.35, m)
				}
			}
		})
	}
}
