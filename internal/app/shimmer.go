package app

import (
	"math"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	shimmerSweep   = 2 * time.Second
	shimmerPadding = 10
	shimmerBand    = 5.0
	shimmerPeak    = 70
)

var shimmerEpoch = time.Now()

type shimmerSpan struct {
	y, x0, x1 int
}

func (m *OS) shimmerActive() bool {
	return len(m.motion.rail) > 0 && !m.screensaver.active &&
		m.Settings.MotionOn(config.AspectSidebar)
}

func shimmerIntensity(i, n int, since time.Duration) float64 {
	period := float64(n + 2*shimmerPadding)
	pos := float64(since%shimmerSweep) / float64(shimmerSweep) * period
	d := math.Abs(float64(i+shimmerPadding) - pos)
	if d > shimmerBand {
		return 0
	}
	return 0.5 * (1 + math.Cos(math.Pi*d/shimmerBand))
}

func (m *OS) applyShimmer(canvas *frameCanvas, now time.Time) {
	if !m.shimmerActive() {
		return
	}
	since := now.Sub(shimmerEpoch)
	truecolor := theme.Depth() == overlay.DepthTrueColor
	s := &m.motion.shimmer
	if truecolor {
		s.setToward(theme.ShimmerInk())
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
