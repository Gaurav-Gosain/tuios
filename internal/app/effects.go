package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

type effect struct {
	name       string
	live       func(m *OS) bool
	draw       func(m *OS, c *frameCanvas, now time.Time)
	every      func(m *OS) time.Duration
	step       func(m *OS, now time.Time) (redraw bool, cmd tea.Cmd)
	besidePane bool
}

var effects = [...]effect{
	{name: "window animation", live: func(m *OS) bool { return len(m.Animations) > 0 }},
	{
		name: "screen saver", live: func(m *OS) bool { return m.screensaver.active },
		every: (*OS).saverFrameEvery, step: (*OS).stepScreensaver,
	},
	{
		name: "overlay fade", live: func(m *OS) bool { return m.motion.fading > 0 },
		every: func(*OS) time.Duration { return config.OverlayFadeFrame },
		step: func(m *OS, now time.Time) (bool, tea.Cmd) {
			m.retireFades(now)
			return true, nil
		},
	},
	{
		name: "copy sweep", live: func(m *OS) bool { return m.copyFlash != nil }, besidePane: true,
		every: func(*OS) time.Duration { return config.OverlayFadeFrame },
		step:  func(m *OS, _ time.Time) (bool, tea.Cmd) { return m.markCopyFlashPane(), nil },
	},
	{
		name: "shimmer", live: (*OS).shimmerActive, besidePane: true,
		every: func(*OS) time.Duration { return config.ShimmerFrame },
		step:  func(m *OS, now time.Time) (bool, tea.Cmd) { return !m.shimmerCovered(now), nil },
	},
	{
		name: "spotlight", live: func(m *OS) bool { return m.spotlight.on },
		draw: func(m *OS, c *frameCanvas, _ time.Time) {
			if !m.screensaver.active {
				m.applySpotlight(c)
			}
		},
	},
	{name: "hints", live: func(m *OS) bool { return m.hints != nil }},
	{name: "pane labels", live: func(m *OS) bool { return m.paneLabels != nil }},
	{
		name: "celebration", live: func(m *OS) bool { return m.celebration.active() },
		draw:  func(m *OS, c *frameCanvas, _ time.Time) { m.celebration.draw(c) },
		every: (*OS).celebrateEvery, step: (*OS).stepCelebration,
	},
	{
		name: "theme transition", live: func(m *OS) bool { return m.themeFx.running() },
		draw:  func(m *OS, c *frameCanvas, now time.Time) { m.applyThemeTransition(c, now) },
		every: (*OS).themeTransitionFrame,
		step:  func(m *OS, now time.Time) (bool, tea.Cmd) { return m.expireThemeTransition(now), nil },
	},
	{
		name: "workspace slide", live: func(m *OS) bool { return m.wsSlide.running() },
		every: (*OS).slideFrameInterval,
		step:  func(m *OS, now time.Time) (bool, tea.Cmd) { return m.expireWorkspaceSlide(now), nil },
	},
}

func (m *OS) anyEffectLive() bool {
	for i := range effects {
		if !effects[i].besidePane && effects[i].live(m) {
			return true
		}
	}
	return false
}

func (m *OS) drawEffects(c *frameCanvas, now time.Time) {
	for i := range effects {
		if e := &effects[i]; e.draw != nil && e.live(m) {
			e.draw(m, c, now)
		}
	}
}
