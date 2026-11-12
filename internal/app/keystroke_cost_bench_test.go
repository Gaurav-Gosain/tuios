package app

import (
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/charmbracelet/colorprofile"
)

// The cost of the frame one typed key makes in a pane, on a client with a
// screen: a 200x60 terminal, one pane, the rail on the left and the dock at
// the bottom. The key changes one cell of the pane, so every cell a frame
// recomposes past that one is cost that follows the screen and not the change.

const (
	keyCols = 200
	keyRows = 60
)

// countWriter counts the bytes the screen's writer goroutine hands it.
type countWriter struct{ n atomic.Int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return len(p), nil
}

// keyPaneOS is one tiled pane at 200x60 with the rail and the dock shown, on
// a screen that writes to out.
func keyPaneOS(tb testing.TB, out io.Writer) *OS {
	tb.Helper()
	orig := config.Global
	config.Global.Motion = config.MotionNone
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	config.Global.DockbarPosition = "bottom"
	tb.Cleanup(func() { config.Global = orig })
	m := tiledKeystrokeOS(tb, 1, keyCols, keyRows)
	m.SessionName = "work"
	m.UseScreen(out, []string{"TERM=xterm-256color"}, colorprofile.TrueColor, termio.Output{Raw: true})
	tb.Cleanup(m.CloseScreen)
	return m
}

// keyFrame is what a client does for a frame once the key's echo reached the
// pane's emulator: View composes and hands the screen the frame, and the
// flush commits it.
func keyFrame(m *OS) {
	_ = m.View()
	m.screen.Commit()
}

// typeKey writes the echo of the i-th key into the focused pane.
func typeKey(m *OS, i int) {
	w := m.Windows[0]
	w.LockIO()
	_, _ = w.Terminal.Write(fmt.Appendf(nil, "\x1b[2;3H%c", 'a'+byte(i%26)))
	w.UnlockIO()
	w.MarkContentDirty()
	m.renderSkipped = false
}

// keyWork is the work in cells of the frame just composed: every cell of
// the canvas cleared, the cells of each layer copied onto it, the cells of
// the layers parsed from their string because the string changed, and the
// cells the screen compares and diffs against the last frame.
type keyWork struct {
	cleared, blitted, parsed, diffed int
}

// layerSeen is a layer's string as the last frame left it, by its bytes'
// address, so a layer whose string was built afresh shows as a reparse.
type layerSeen map[string]uintptr

func measureFrame(m *OS, seen layerSeen) keyWork {
	var fc keyWork
	if c := m.renderCanvas; c != nil {
		fc.cleared = c.Width() * c.Height()
		fc.diffed = fc.cleared * m.screen.DiffedRows() / max(c.Height(), 1)
	}
	for id, cl := range m.layerCells {
		if cl.gen != m.composeGen {
			continue
		}
		fc.blitted += cl.w * cl.h
		p := uintptr(unsafe.Pointer(unsafe.StringData(cl.content)))
		if seen[id] != p {
			fc.parsed += cl.w * cl.h
		}
		seen[id] = p
	}
	return fc
}

// BenchmarkKeystrokeFramePane is the frame one typed key costs in a 200x60
// pane with the rail and the dock: time, allocations, bytes written, and the
// cells the frame recomposed (per frame, the mean over the run).
func BenchmarkKeystrokeFramePane(b *testing.B) {
	out := &countWriter{}
	m := keyPaneOS(b, out)
	m.MarkAllDirty()
	keyFrame(m)
	seen := layerSeen{}
	measureFrame(m, seen)
	start := out.n.Load()
	var sum keyWork
	i, n := 0, 0
	b.ReportAllocs()
	for b.Loop() {
		typeKey(m, i)
		keyFrame(m)
		fc := measureFrame(m, seen)
		sum.cleared += fc.cleared
		sum.blitted += fc.blitted
		sum.parsed += fc.parsed
		sum.diffed += fc.diffed
		i++
		n++
	}
	m.CloseScreen()
	n = max(n, 1)
	b.ReportMetric(float64(out.n.Load()-start)/float64(n), "bytes/frame")
	b.ReportMetric(float64(sum.cleared)/float64(n), "cleared/frame")
	b.ReportMetric(float64(sum.blitted)/float64(n), "blitted/frame")
	b.ReportMetric(float64(sum.parsed)/float64(n), "parsed/frame")
	b.ReportMetric(float64(sum.diffed)/float64(n), "diffed/frame")
}
