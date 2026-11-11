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

	// ticking says a motionFrameMsg is on its way, for wake. gen names the
	// one that counts: re-arming for an earlier wake bumps it, and a frame
	// carrying an older number is dropped, so there is never more than one
	// live timer.
	ticking bool
	gen     uint64
	wake    time.Time
	// due is when each clocked effect's next frame is due, zero while it is
	// not live. An effect's frames keep to a grid of its own interval, so a
	// frame handled late does not push every later one back.
	due []time.Time
	// drawn is when the last motion frame asked for a draw. A frame composed
	// after it, for any other reason, already carried the shimmer.
	drawn time.Time

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
// on its way for the earliest effect that is due one. It returns nil when no
// effect moves, which is the case on every message of an idle session.
func (m *OS) motionCmd() tea.Cmd {
	m.trackModalOverlays()
	m.noticeThemeChange()
	now := time.Now()
	wake, ok := m.nextEffectFrame(now)
	if !ok {
		return nil
	}
	mo := &m.motion
	if mo.ticking && !mo.wake.After(wake) {
		return nil
	}
	mo.gen++
	mo.ticking, mo.wake = true, wake
	gen := mo.gen
	return tea.Tick(max(wake.Sub(now), 0), func(time.Time) tea.Msg { return motionFrameMsg{gen: gen} })
}

// nextEffectFrame is when the earliest live clocked effect wants its next
// frame. An effect that has just gone live is due one interval from now.
func (m *OS) nextEffectFrame(now time.Time) (time.Time, bool) {
	if len(m.motion.due) != len(effects) {
		m.motion.due = make([]time.Time, len(effects))
	}
	var wake time.Time
	for i := range effects {
		e := &effects[i]
		if e.every == nil {
			continue
		}
		if !e.live(m) {
			m.motion.due[i] = time.Time{}
			continue
		}
		if m.motion.due[i].IsZero() {
			m.motion.due[i] = now.Add(e.every(m))
		}
		if wake.IsZero() || m.motion.due[i].Before(wake) {
			wake = m.motion.due[i]
		}
	}
	return wake, !wake.IsZero()
}

// nextDue is when an effect's frame after the one due at due comes, handled at
// now: one interval after due, so the frames keep to the effect's grid and a
// frame handled late does not push every later one back. When now has fallen
// more than a quarter interval past that, a new grid starts at now: the
// frames a stall missed are gone, not caught up.
func nextDue(due, now time.Time, every time.Duration) time.Time {
	next := due.Add(every)
	if now.Sub(next) > every/4 {
		return now.Add(every)
	}
	return next
}

// handleMotionFrame is one frame of the motion clock: every live effect whose
// frame is due steps to now, and a frame is drawn if any of them moved. The
// post-Update hook re-arms the clock, or lets it lapse.
//
// Each effect keeps to its own grid (nextDue). A frame is taken up to a
// quarter interval early, so two effects on nearly the same beat share a
// timer.
func (m *OS) handleMotionFrame(msg motionFrameMsg) tea.Cmd {
	m.tickStats.Motion++
	if msg.gen != m.motion.gen {
		// Superseded by a timer armed for an earlier wake. Drawing for it
		// would draw the same frame twice.
		m.renderSkipped = true
		return nil
	}
	m.motion.ticking = false
	now := time.Now()
	need := false
	var cmds []tea.Cmd
	if len(m.motion.due) != len(effects) {
		m.motion.due = make([]time.Time, len(effects))
	}
	for i := range effects {
		e := &effects[i]
		if e.every == nil {
			continue
		}
		if !e.live(m) {
			m.motion.due[i] = time.Time{}
			continue
		}
		due := m.motion.due[i]
		if due.IsZero() {
			// Gone live since the clock last looked: its first frame is now.
			due = now
		}
		every := e.every(m)
		if now.Before(due.Add(-every / 4)) {
			continue
		}
		redraw, cmd := e.step(m, now)
		need = need || redraw
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !e.live(m) {
			m.motion.due[i] = time.Time{}
			continue
		}
		// After the step: a frames file says how long the frame now on
		// screen stays.
		m.motion.due[i] = nextDue(due, now, e.every(m))
	}
	if need {
		m.motion.drawn = now
	}
	m.renderSkipped = !need
	return tea.Batch(cmds...)
}

// shimmerCovered reports whether a frame that something else asked for was
// composed within the last shimmer interval. A frame the motion clock asked
// for is composed within a few milliseconds of the request, so only a frame
// composed later than that margin after it counts.
func (m *OS) shimmerCovered(now time.Time) bool {
	const margin = 15 * time.Millisecond
	last := m.frameRate.lastFrame
	return last.After(m.motion.drawn.Add(margin)) && now.Sub(last) < config.ShimmerFrame
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
		if !start.IsZero() && now.Sub(start) >= m.fadeLength() {
			mo.fadeStart[i] = time.Time{}
			mo.fading--
		}
	}
}

// fadeLength is how long an overlay takes to come up: motion.overlays_ms at
// animation speed, or zero when overlays may not fade.
func (m *OS) fadeLength() time.Duration {
	return m.Settings.MotionLength(config.AspectOverlays, config.OverlayFadeDuration)
}

// fadeAllowed reports whether an overlay opening now fades in. Truecolor only:
// at 256 and 16 colours a blend between two colours snaps between the few the
// terminal has, and a fade that jumps through three unrelated colours is worse
// than none.
func (m *OS) fadeAllowed() bool {
	return m.fadeLength() > 0 &&
		theme.Depth() == overlay.DepthTrueColor &&
		!m.screensaver.active
}

// fadeLevel is how far toward the ground an overlay drawn now still is: the
// last of the 16 levels when it has just opened, zero once it has come up.
// The curve is smoothstep, the one opencode fades its panels with, so the
// panel starts and settles gently rather than at a constant rate.
func fadeLevel(start, now time.Time, dur time.Duration) uint8 {
	if start.IsZero() {
		return 0
	}
	p := float64(now.Sub(start)) / float64(dur)
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
	level := fadeLevel(m.motion.fadeStart[i], now, m.fadeLength())
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
