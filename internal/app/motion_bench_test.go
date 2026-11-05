package app

import (
	"fmt"
	"image"
	"math/rand"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Perf budgets for the modal scrim and the working-row shimmer. Both are
// passes over the composed canvas, so what they add is read off a whole frame
// with and without them, in one invocation, the way BenchmarkSpotlightFrame
// reads the spotlight's cost.

// BenchmarkModalScrimFrame is a nine-pane frame with the command palette open
// and one pane dirty, at modal_dim 0 (no scrim) and at the shipped 30.
func BenchmarkModalScrimFrame(b *testing.B) {
	prev := theme.CurrentThemeID()
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() { _ = theme.Initialize(prev) })

	for _, dim := range []int{0, config.ModalDimDefault} {
		name := "dim-off"
		if dim > 0 {
			name = "dim-on"
		}
		b.Run(name, func(b *testing.B) {
			m := benchOS(b, 9)
			m.UserConfig = config.DefaultConfig()
			m.Settings.ModalDim = dim
			m.ShowCommandPalette = true
			m.composeFrame()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				m.Windows[0].MarkContentDirty()
				_ = m.composeFrame()
			}
		})
	}
}

// BenchmarkShimmerApply is the shimmer pass on its own over a twelve-agent
// rail, two of whose rows are working, in truecolor, where each cell is a
// cached blend. Below truecolor a cell costs one attribute write.
func BenchmarkShimmerApply(b *testing.B) {
	prev := theme.CurrentThemeID()
	_ = theme.Initialize("catppuccin_mocha")
	b.Cleanup(func() { _ = theme.Initialize(prev) })

	m, tree := benchAgentOS(b)
	m.Settings.Motion = config.MotionFull
	lines, _ := m.sidebarPanelLinesForTree(tree)
	if len(m.motion.rail) == 0 {
		b.Fatal("the rail recorded no working row")
	}
	canvas := &frameCanvas{Buffer: *uv.NewBuffer(m.Width, m.Height)}
	for y, line := range lines {
		uv.NewStyledString(line).Draw(canvas, image.Rect(0, y, m.Width, y+1))
	}
	// A real frame is composed fresh, so each pass starts from the rail's own
	// cells. Putting back the few cells the pass edits is what reproduces
	// that without timing a whole compose.
	var saved [][]uv.Cell
	for _, sp := range m.motion.rail {
		saved = append(saved, append([]uv.Cell(nil), canvas.Lines[sp.y][sp.x0:sp.x1]...))
	}
	restore := func() {
		for i, sp := range m.motion.rail {
			copy(canvas.Lines[sp.y][sp.x0:sp.x1], saved[i])
		}
	}
	now := time.Now()
	for range 2 * int(shimmerSweep/config.ShimmerFrame) { // warm the blend cache
		now = now.Add(config.ShimmerFrame)
		restore()
		m.applyShimmer(canvas, now)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		now = now.Add(config.ShimmerFrame)
		restore()
		m.applyShimmer(canvas, now)
	}
}

// BenchmarkWorkspaceSlideFrame is the frame budget for the workspace slide:
// a 200x50 screen, four tiled panes on each of two workspaces, and every
// pane of the workspace on screen repainted with a fresh flood each frame.
// "cut" is that frame with no slide, "slide" the same frame half way through
// one, and "begin" the snapshot a switch takes before it slides. A slide
// frame has to hold 60 frames a second, so "slide" stays well under 16ms.
// The floods are written with the timer stopped: what is timed is the frame,
// with every pane on screen dirty.
func BenchmarkWorkspaceSlideFrame(b *testing.B) {
	const cols, rows = 200, 50
	setup := func(b *testing.B) *OS {
		m := &OS{
			Settings:         config.Global,
			WorkspaceFocus:   map[int]int{},
			NumWorkspaces:    9,
			CurrentWorkspace: 1,
			Width:            cols,
			Height:           rows,
		}
		m.Settings.Motion = config.MotionFull
		w, h := cols/2, rows/2
		for ws := 1; ws <= 2; ws++ {
			for i := range 4 {
				win := floodWindow(b, fmt.Sprintf("slide-%d-%d", ws, i), w-2, h-2)
				win.X, win.Y, win.Width, win.Height = (i%2)*w, (i/2)*h, w, h
				win.Workspace, win.Tiled = ws, true
				m.Windows = append(m.Windows, win)
			}
		}
		m.composeFrame()
		return m
	}
	rng := rand.New(rand.NewSource(2))
	flood := func(b *testing.B, m *OS) {
		b.StopTimer()
		defer b.StartTimer()
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace {
				floodFrame(b, w, rng, w.Width-2, w.Height-2)
				w.MarkContentDirty()
			}
		}
	}
	b.Run("cut", func(b *testing.B) {
		m := setup(b)
		m.CurrentWorkspace = 2
		m.composeFrame()
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			flood(b, m)
			_ = m.composeFrame()
		}
	})
	b.Run("slide", func(b *testing.B) {
		m := setup(b)
		m.beginWorkspaceSlide(1, 2)
		if !m.wsSlide.running() {
			b.Fatal("the slide did not start")
		}
		m.CurrentWorkspace = 2
		m.wsSlide.dur = time.Hour
		m.composeFrame()
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			// Half way, every frame, so each one draws both workspaces.
			m.wsSlide.start = time.Now().Add(-time.Hour / 5)
			flood(b, m)
			_ = m.composeFrame()
		}
		if !m.wsSlide.running() {
			b.Fatal("the slide ended during the run")
		}
	})
	b.Run("begin", func(b *testing.B) {
		m := setup(b)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			flood(b, m)
			m.beginWorkspaceSlide(1, 2)
		}
	})
}
