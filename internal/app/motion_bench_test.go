package app

import (
	"image"
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
