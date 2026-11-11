package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// An effect changes what the frame shows for a while, on its own: a window
// sliding to its tile, the screen saver, the spotlight, hints mode, a
// celebration, a theme or workspace transition. effects is the one list of
// them. The fullscreen fast path builds no canvas and composes no layer, so it
// is open only while none is live (anyEffectLive). An effect with a pass of
// its own over the composed canvas has draw, and composeFrameParts runs those
// in this order: the spotlight's beam first, the confetti over the beam, and
// the old theme outside the transition's circle over everything.
//
// An effect drawn while the layers are composed (the slide, the hints and the
// pane labels, the saver, which replaces the frame) has no draw here.
//
// An effect that moves on its own has every and step, and the motion clock
// (motion.go) is its only clock: every is how long the effect waits between
// its frames while it is live, and step moves it to now at one of them. step
// reports whether the frame shows anything new, and may return a command.
// An effect that leaves a lone pane's frame alone (besidePane) does not keep
// the fast path or the direct pane off: the copy sweep is drawn into the
// pane's own text, which both draw, and the shimmer runs on the rail's rows,
// which the fast path does not draw and the direct pane does not cover. A
// frame with the shimmer on still composes the canvas in full (composeLayers).
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
		// The frame that retires a fade still draws: it is the overlay at its
		// own colours.
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
		// A shimmer frame is cheap to compute and dear to send: when another
		// frame went out within the last interval it carried the band already.
		step: func(m *OS, now time.Time) (bool, tea.Cmd) { return !m.shimmerCovered(now), nil },
	},
	{
		name: "spotlight", live: func(m *OS) bool { return m.spotlight.on },
		// The saver owns the whole screen while it runs and suspends the beam.
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
		// The tick that ends a transition still draws: the new theme on the
		// whole screen.
		step: func(m *OS, now time.Time) (bool, tea.Cmd) { return m.expireThemeTransition(now), nil },
	},
	{
		name: "workspace slide", live: func(m *OS) bool { return m.wsSlide.running() },
		every: (*OS).slideFrameInterval,
		step:  func(m *OS, now time.Time) (bool, tea.Cmd) { return m.expireWorkspaceSlide(now), nil },
	},
}

// anyEffectLive reports whether an effect that a lone pane's frame cannot
// stand beside is changing the frame now.
func (m *OS) anyEffectLive() bool {
	for i := range effects {
		if !effects[i].besidePane && effects[i].live(m) {
			return true
		}
	}
	return false
}

// drawEffects runs the canvas pass of every live effect that has one, in
// list order.
func (m *OS) drawEffects(c *frameCanvas, now time.Time) {
	for i := range effects {
		if e := &effects[i]; e.draw != nil && e.live(m) {
			e.draw(m, c, now)
		}
	}
}
