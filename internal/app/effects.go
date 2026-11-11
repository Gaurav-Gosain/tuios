package app

import "time"

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
type effect struct {
	name string
	live func(m *OS) bool
	draw func(m *OS, c *frameCanvas, now time.Time)
}

var effects = [...]effect{
	{name: "window animation", live: func(m *OS) bool { return len(m.Animations) > 0 }},
	{name: "screen saver", live: func(m *OS) bool { return m.screensaver.active }},
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
		draw: func(m *OS, c *frameCanvas, _ time.Time) { m.celebration.draw(c) },
	},
	{
		name: "theme transition", live: func(m *OS) bool { return m.themeFx.running() },
		draw: func(m *OS, c *frameCanvas, now time.Time) { m.applyThemeTransition(c, now) },
	},
	{name: "workspace slide", live: func(m *OS) bool { return m.wsSlide.running() }},
}

// anyEffectLive reports whether an effect is changing the frame now.
func (m *OS) anyEffectLive() bool {
	for i := range effects {
		if effects[i].live(m) {
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
