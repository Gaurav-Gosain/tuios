package app

import (
	"image"
	"image/color"
	"math"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Motion is the decorative animation the frame carries on top of what it
// shows: an overlay fading in as it opens, and the shimmer on a working
// agent's row. Both are appearance.motion = full only. The clock also carries
// the copy sweep (copy_flash.go), which runs at basic and full.
//
// It has its own clock rather than riding the maintenance tick. The tick runs
// at the frame rate whenever anything periodic is live and falls to ten a
// second otherwise, and it does a round of work per wakeup (window sweeps,
// alert flushes, animation steps) that a shimmer frame has no use for. The
// motion clock is a single timer that exists only while something is moving:
// Update arms it when a fade starts or a working row is on screen, each frame
// it delivers either re-arms it or lets it lapse, and a screen with nothing
// moving has no motion timer at all. That is what keeps the idle cost where
// TestIdleCostStaysLow holds it.
//
// Its frames go through the ordinary render path. The fade and the shimmer are
// passes over the composed canvas (see composeLayersIn), so a frame drawn for
// them re-renders no pane and rebuilds no rail: every cached layer is copied in
// as it is on any frame, and the pass edits the few cells that move.

// motionState is this client's motion. Client-local, like the spotlight: a
// peer on the same session animates its own screen.
type motionState struct {
	// open is the set of modal overlays that were up when Update last looked,
	// one bit per entry of modalOverlays.
	open uint64
	// fadeStart is when each overlay's fade began, zero for none.
	fadeStart [maxModalOverlays]time.Time
	// fading counts the fades with a start time, so the common case of none
	// is one comparison.
	fading int

	// ticking says a motionFrameMsg is on its way. gen names the one that
	// counts: re-arming at a shorter interval bumps it, and a frame carrying
	// an older number is dropped, so there is never more than one live timer.
	ticking  bool
	gen      uint64
	interval time.Duration

	// scrim, fade and shimmer are the shades each pass carries cells with.
	// Separate, because each carries toward a different colour or by a
	// different amount, and one cache would evict the others' blends.
	scrim   cellShade
	fade    cellShade
	shimmer cellShade

	// rail is where the working agent rows' names are on screen, recorded
	// when the rail is built and restored with it from the rail cache.
	rail []shimmerSpan
}

// motionFrameMsg is one frame of the motion clock.
type motionFrameMsg struct{ gen uint64 }

// motionCmd runs after every Update. It notices the overlays that opened since
// the last message and starts their fades, then makes sure a motion frame is
// on its way if anything is moving. It returns nil when nothing needs a timer,
// which is the case on every message of an idle session.
func (m *OS) motionCmd() tea.Cmd {
	m.trackModalOverlays()
	want := m.motionInterval()
	if want == 0 {
		return nil
	}
	mo := &m.motion
	if mo.ticking && mo.interval <= want {
		return nil
	}
	mo.gen++
	mo.ticking, mo.interval = true, want
	gen := mo.gen
	return tea.Tick(want, func(time.Time) tea.Msg { return motionFrameMsg{gen: gen} })
}

// motionInterval is how soon the next motion frame is needed: a fade's frame
// while one runs, a shimmer frame while a working row is on screen, and zero
// when nothing moves.
func (m *OS) motionInterval() time.Duration {
	if m.motion.fading > 0 {
		return config.OverlayFadeFrame
	}
	// A copy sweep, at the fade's rate: it crosses the block in under half a
	// second, and at the maintenance tick's idle rate that was four frames.
	if m.copyFlash != nil {
		return config.OverlayFadeFrame
	}
	if m.shimmerActive() {
		return config.ShimmerFrame
	}
	return 0
}

// handleMotionFrame is one frame of the motion clock: it retires the fades
// that have run their course and asks for a frame if anything moved. The
// post-Update hook re-arms the clock, or lets it lapse.
func (m *OS) handleMotionFrame(msg motionFrameMsg) {
	m.tickStats.Motion++
	if msg.gen != m.motion.gen {
		// Superseded by a timer armed at a shorter interval. Drawing for it
		// would draw the same frame twice.
		m.renderSkipped = true
		return
	}
	m.motion.ticking = false
	// Marks the swept pane, so it draws rather than serving its cached frame,
	// and asks for one last frame when the sweep has just ended so its light
	// does not stay on the screen.
	flash := m.markCopyFlashPane()
	need := m.motion.fading > 0 || m.shimmerActive() || flash
	if m.motion.fading > 0 {
		m.retireFades(time.Now())
	}
	m.renderSkipped = !need
}

// trackModalOverlays compares the modal overlays that are up with the ones
// that were, and starts a fade for each that has just opened. A closed one
// loses its fade at once: closing is instant, and reopening starts over.
//
// It reads the clock only when the set changed, since it runs after every
// message and an idle session's messages change nothing.
func (m *OS) trackModalOverlays() {
	var open uint64
	for i := range modalOverlays {
		if modalOverlays[i].open(m) {
			open |= 1 << i
		}
	}
	mo := &m.motion
	if open == mo.open {
		return
	}
	now := time.Now()
	opened, closed := open&^mo.open, mo.open&^open
	mo.open = open
	fade := m.fadeAllowed()
	for i := range modalOverlays {
		bit := uint64(1) << i
		switch {
		case opened&bit != 0 && fade:
			if mo.fadeStart[i].IsZero() {
				mo.fading++
			}
			mo.fadeStart[i] = now
		case closed&bit != 0 && !mo.fadeStart[i].IsZero():
			mo.fadeStart[i] = time.Time{}
			mo.fading--
		}
	}
}

// retireFades ends every fade that has run its full length. The frame drawn
// next is the overlay at its own colours.
func (m *OS) retireFades(now time.Time) {
	mo := &m.motion
	for i, start := range mo.fadeStart {
		if !start.IsZero() && now.Sub(start) >= config.OverlayFadeDuration {
			mo.fadeStart[i] = time.Time{}
			mo.fading--
		}
	}
}

// fadeAllowed reports whether an overlay opening now fades in. Truecolor only:
// at 256 and 16 colours a blend between two colours snaps between the few the
// terminal has, and a fade that jumps through three unrelated colours is worse
// than none.
func (m *OS) fadeAllowed() bool {
	return m.Settings.MotionAllows(config.MotionFull) &&
		theme.Depth() == overlay.DepthTrueColor &&
		!m.screensaver.active
}

// fadeLevel is how far toward the ground an overlay drawn now still is: the
// last of the 16 levels when it has just opened, zero once it has come up.
// The curve is smoothstep, the one opencode fades its panels with, so the
// panel starts and settles gently rather than at a constant rate.
func fadeLevel(start, now time.Time) uint8 {
	if start.IsZero() {
		return 0
	}
	p := float64(now.Sub(start)) / float64(config.OverlayFadeDuration)
	if p >= 1 {
		return 0
	}
	p = max(p, 0)
	remaining := 1 - p*p*(3-2*p)
	return uint8(math.Round(remaining * float64(config.SpotlightLevels-1)))
}

// applyFade carries the cells of one overlay's rectangle toward the ground by
// how far its fade has left to run. composeLayersIn calls it right after the
// overlay's layer is drawn, so the pass covers that overlay and nothing else.
func (m *OS) applyFade(canvas *frameCanvas, id string, bounds image.Rectangle, now time.Time) {
	i, ok := modalIndex[id]
	if !ok {
		return
	}
	level := fadeLevel(m.motion.fadeStart[i], now)
	if level == 0 {
		return
	}
	s := &m.motion.fade
	s.setToward(theme.UI().Canvas)
	s.noFaint = true
	s.syncGround()
	s.syncLevels(100)
	s.run.have = false
	r := bounds.Intersect(canvas.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		line := canvas.Lines[y]
		for x := r.Min.X; x < r.Max.X; x++ {
			if cell := &line[x]; cell.Content != "" {
				s.dimCell(cell, level)
			}
		}
	}
}

// setToward points the shade at a new colour, dropping every blend made
// toward the old one.
func (s *cellShade) setToward(c color.Color) {
	if isNilColor(s.toward) == isNilColor(c) && (isNilColor(c) || packColor8(s.toward) == packColor8(c)) {
		return
	}
	s.toward = c
	clear(s.blend)
	s.run.have = false
}
