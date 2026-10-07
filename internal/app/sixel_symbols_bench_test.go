package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/mosaic"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/colorprofile"
)

// BenchmarkSymbolImageFrame is the keystroke frame of a pane that shows a
// 60x16-cell sixel image on a host without graphics, drawn as the placeholder
// box (the cost before image_symbols) and as each glyph set. The glyphs are
// drawn when the image arrives; a frame only looks them up.
func BenchmarkSymbolImageFrame(b *testing.B) {
	// The test binary's stdout is not a terminal, and a frame with no colour
	// draws the box (symbolColors).
	was := theme.ColorProfile()
	theme.SetColorProfile(colorprofile.TrueColor)
	b.Cleanup(func() { theme.SetColorProfile(was) })
	for _, k := range []mosaic.Kind{mosaic.Off, mosaic.Quadrant, mosaic.Octant} {
		name := k.String()
		if k == mosaic.Off {
			name = "box"
		}
		b.Run(name, func(b *testing.B) {
			m := keystrokeOS(b, 1, realCols, realRows)
			m.Caps = &HostCapabilities{CellWidth: 10, CellHeight: 20}
			m.SixelPassthrough = NewSixelPassthroughWithOptions(SixelPassthroughOptions{Caps: m.Caps, Symbols: k})
			w := m.Windows[0]
			w.SetCellPixelDimensions(10, 20)
			w.LockIO()
			m.setupSixelPassthrough(w)
			w.UnlockIO()
			w.WriteOutput([]byte("\x1b[H\x1b[2J"))
			w.WriteOutput(sixelTestImage(60, 16))
			w.MarkContentDirty()
			sink := newFrameSink(realCols, realRows)
			sink.emit(m.composeFrame())
			var out, i int
			b.ReportAllocs()
			for b.Loop() {
				w.LockIO()
				_, _ = w.Terminal.Write(fmt.Appendf(nil, "\x1b[20;3H%c", 'a'+byte(i%26)))
				w.UnlockIO()
				w.MarkContentDirty()
				out = sink.emit(m.composeFrame())
				i++
			}
			b.ReportMetric(float64(out), "bytes/frame")
		})
	}
}
