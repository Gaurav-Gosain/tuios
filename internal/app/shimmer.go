package app

import (
	"math"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The shimmer is a band of light that sweeps across the name of an agent row
// on the rail while that agent is working. The glyph beside the row already
// says "working"; what the glyph cannot say is that the agent is alive right
// now, and a static glyph looks the same on an agent that is busy and on one
// that hung an hour ago. Codex draws its "Working" header this way, and this is
// its algorithm (codex-rs/tui/src/shimmer.rs):
//
//   - one clock for the whole process, so every row on screen sweeps in step;
//   - the band crosses the text plus ten columns of run-up on each side every
//     two seconds, so there is a pause between sweeps;
//   - within five columns of the band's centre the intensity is a raised
//     cosine, 0.5 * (1 + cos(pi * d / 5)), and zero outside.
//
// In truecolor a cell's ink is carried toward the accent by the intensity. At
// 256 and 16 colours a blend would snap between unrelated colours, so the
// intensity picks an attribute instead, as Codex does: faint below 0.2, the
// row's own weight below 0.6, bold above. The row as a whole reads a little
// quieter and a bright band crosses it.
//
// Who sees it. Only a row in the rail's agents section, and only while that
// pane's agent state is working. Somebody who never runs an agent has no such
// row, so the shimmer never draws, the motion clock never starts, and the rail
// cache is never touched. It is decoration, so it is appearance.motion = full
// only, and --no-animations turns it off with everything else.
//
// What it costs. The pass runs on the canvas right after the rail's layer is
// drawn (see composeLayers). The rail's rows stay cached: the span of each
// working name is recorded when the rows are built and kept with them, and the
// pass rewrites the style of those cells and no others. A frame drawn for the
// shimmer re-renders no pane and rebuilds no rail, and the clock asks for 15 of
// them a second.

const (
	// shimmerSweep is how long the band takes to cross a row and its run-up.
	shimmerSweep = 2 * time.Second
	// shimmerPadding is the run-up on each side of the text, in columns: the
	// band starts and ends off the text, which is the pause between sweeps.
	shimmerPadding = 10
	// shimmerBand is the band's half-width in columns.
	shimmerBand = 5.0
	// shimmerPeak is how far toward the accent the band's centre carries a
	// cell's ink, in percent. Codex goes to 90; a rail row is quieter chrome
	// than Codex's one status line and sits beside many others, so the band
	// here is lighter.
	shimmerPeak = 70
)

// shimmerEpoch is the process-wide clock every row's band is measured from.
var shimmerEpoch = time.Now()

// shimmerSpan is the name of one working agent row on screen: the row y and
// the columns [x0, x1).
type shimmerSpan struct {
	y, x0, x1 int
}

// shimmerActive reports whether a working row is on screen and allowed to
// shimmer. It reads the spans the last frame's rail recorded.
func (m *OS) shimmerActive() bool {
	return len(m.motion.rail) > 0 && !m.screensaver.active &&
		m.Settings.MotionAllows(config.MotionFull)
}

// shimmerIntensity is the band's brightness at column i of a text n columns
// wide, at time since the epoch: Codex's raised cosine.
func shimmerIntensity(i, n int, since time.Duration) float64 {
	period := float64(n + 2*shimmerPadding)
	pos := float64(since%shimmerSweep) / float64(shimmerSweep) * period
	d := math.Abs(float64(i+shimmerPadding) - pos)
	if d > shimmerBand {
		return 0
	}
	return 0.5 * (1 + math.Cos(math.Pi*d/shimmerBand))
}

// applyShimmer sweeps the band across every recorded working row. It runs
// right after the rail's layer is drawn, before anything above the rail is,
// so an overlay covering the rail is never touched.
func (m *OS) applyShimmer(canvas *frameCanvas, now time.Time) {
	if !m.shimmerActive() {
		return
	}
	since := now.Sub(shimmerEpoch)
	truecolor := theme.Depth() == overlay.DepthTrueColor
	s := &m.motion.shimmer
	if truecolor {
		s.setToward(theme.UI().AccentBright)
		s.syncLevels(shimmerPeak)
	}
	const maxLevel = config.SpotlightLevels - 1
	area := canvas.Bounds()
	for _, sp := range m.motion.rail {
		if sp.y < area.Min.Y || sp.y >= area.Max.Y {
			continue
		}
		line := canvas.Lines[sp.y]
		x0, x1 := max(sp.x0, area.Min.X), min(sp.x1, area.Max.X)
		n := sp.x1 - sp.x0
		for x := x0; x < x1; x++ {
			cell := &line[x]
			if cell.Content == "" {
				continue
			}
			t := shimmerIntensity(x-sp.x0, n, since)
			if !truecolor {
				switch {
				case t < 0.2:
					cell.Style.Attrs |= uv.AttrFaint
				case t >= 0.6:
					cell.Style.Attrs |= uv.AttrBold
				}
				continue
			}
			level := uint8(math.Round(t * maxLevel))
			if level == 0 || isNilColor(cell.Style.Fg) || !s.dimmable(cell.Style.Fg) {
				continue
			}
			cell.Style.Fg = s.blendCached(cell.Style.Fg, level, s.levels[level])
		}
	}
}
