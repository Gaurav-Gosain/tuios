package app

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/charmbracelet/colorprofile"
	"tuios.dev/vt"
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
		if !m.canvasKept {
			fc.cleared = c.Width() * c.Height()
		}
		fc.diffed = c.Width() * m.screen.DiffedRows()
	}
	for id, cl := range m.layerCells {
		if cl.gen != m.composeGen {
			continue
		}
		if !m.canvasKept {
			fc.blitted += cl.w * cl.h
		}
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

// TestKeptFrameShowsAFreshCompose checks a frame that keeps the canvas
// (compose.go) against what a fresh compose draws, over keys typed into a
// lone pane beside the rail and the dock, mixed with frames that change the
// rail, the dock and an overlay. The ways it could fail: a layer whose cells
// changed while the canvas was kept, a pass that wrote over the layers on the
// frame before a kept one, a pane row written but not passed to the screen as
// changed, and a frame kept after one composed afresh in between. After
// every frame the frame check (frame_check.go) must have found nothing, and
// the frame the screen holds as shown must be the canvas, cell for cell.
func TestKeptFrameShowsAFreshCompose(t *testing.T) {
	report := t.TempDir() + "/frame-check.log"
	prev := frameCheckPath
	frameCheckPath = report
	t.Cleanup(func() { frameCheckPath = prev })

	m := keyPaneOS(t, io.Discard)
	m.MarkAllDirty()
	keyFrame(m)
	kept := 0
	for i := range 120 {
		switch i % 12 {
		case 3: // the rail: the pane's name
			m.Windows[0].CustomName = fmt.Sprintf("pane %d", i)
		case 5: // the dock: the mode
			if m.Mode == TerminalMode {
				m.Mode = WindowManagementMode
			} else {
				m.Mode = TerminalMode
			}
		case 7: // an overlay over the pane
			m.ShowCommandPalette = true
		case 8:
			m.ShowCommandPalette = false
			m.Windows[0].MarkContentDirty()
		default:
			typeKey(m, i)
		}
		m.renderSkipped = false
		keyFrame(m)
		if m.canvasKept {
			kept++
		}
		if b, err := os.ReadFile(report); err == nil && len(b) > 0 {
			t.Fatalf("frame %d: %s", i, b)
		}
		shown, canvas := m.screen.Shown(), m.renderCanvas
		for y := range canvas.Lines {
			for x := range canvas.Lines[y] {
				if a, b := &shown.Lines[y][x], &canvas.Lines[y][x]; !a.Equal(b) {
					t.Fatalf("frame %d (kept %v) cell %d,%d: shown %q %+v, composed %q %+v", i, m.canvasKept, x, y, a.Content, a.Style, b.Content, b.Style)
				}
			}
		}
	}
	if kept < 60 {
		t.Fatalf("%d of 120 frames kept the canvas, want the typed ones", kept)
	}
}

// TestKeystrokeFrameBudget holds the frame a key typed into a lone pane
// beside the rail and the dock to a budget. The cells are counts and exact:
// the frame keeps the canvas, so nothing is cleared, copied onto it or
// parsed, and the screen diffs the one row the key changed. The bytes are the
// echo and the cursor, and the allocations a ceiling over the measured 376.
//
// The time is the fastest of 5 runs of 100 frames, because load only adds
// time, and it is skipped under -race and -short. On an Apple M3 Pro at -cpu 1
// the frame took 1.0 to 1.35 ms before it kept the canvas, drew the pane from
// its emulator and diffed one row, and about 0.2 ms after. The ceiling of
// 1 ms is 5x above the second, so a shared runner can slow the frame a few
// times without reaching it, and at the first.
func TestKeystrokeFrameBudget(t *testing.T) {
	// The bytes of n typed keys, from the start to the close of a screen.
	// The first frame and the close are the same for any n, so the
	// difference of two runs is the keys alone.
	written := func(n int) int64 {
		out := &countWriter{}
		m := keyPaneOS(t, out)
		m.MarkAllDirty()
		keyFrame(m)
		seen := layerSeen{}
		measureFrame(m, seen)
		for i := range n {
			typeKey(m, i)
			keyFrame(m)
			w := measureFrame(m, seen)
			if w.parsed != 0 || w.cleared != 0 || w.blitted != 0 || w.diffed > keyCols {
				t.Fatalf("a typed key cleared %d cells, copied %d, parsed %d and diffed %d, want 0, 0, 0 and one row (%d)",
					w.cleared, w.blitted, w.parsed, w.diffed, keyCols)
			}
		}
		m.CloseScreen()
		return out.n.Load()
	}
	if per := (written(40) - written(20)) / 20; per > 64 {
		t.Errorf("a typed key wrote %d bytes, want at most 64", per)
	}

	m := keyPaneOS(t, io.Discard)
	m.MarkAllDirty()
	keyFrame(m)
	i := 0
	typed := func() {
		typeKey(m, i)
		keyFrame(m)
		i++
	}
	// The libghostty-vt backend reads the changed row through cgo and
	// allocates for each read: CI measured 567 there against 376 on the pure
	// Go emulator. Each backend has its own ceiling, about a quarter above
	// what it measured.
	maxAllocs := 470.0
	if vt.Backend == "ghostty" {
		maxAllocs = 700
	}
	if allocs := testing.AllocsPerRun(100, typed); allocs > maxAllocs {
		t.Errorf("a typed key's frame allocated %.0f times on the %s backend, want at most %.0f", allocs, vt.Backend, maxAllocs)
	}

	if testing.Short() || raceEnabled {
		return
	}
	const ceiling = time.Millisecond
	best := time.Duration(1<<63 - 1)
	for range 5 {
		start := time.Now()
		for range 100 {
			typed()
		}
		best = min(best, time.Since(start)/100)
	}
	t.Logf("fastest of 5 runs: %v a frame", best)
	if best > ceiling {
		t.Errorf("a typed key's frame took %v at best, want at most %v", best, ceiling)
	}
}
