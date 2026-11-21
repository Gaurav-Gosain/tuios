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

type motionState struct {
	open      uint64
	fadeStart [maxModalOverlays]time.Time
	fading    int

	ticking bool
	gen     uint64
	wake    time.Time
	due     []time.Time
	drawn   time.Time

	scrim   cellShade
	fade    cellShade
	shimmer cellShade

	rail []shimmerSpan
}

type motionFrameMsg struct{ gen uint64 }

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

func nextDue(due, now time.Time, every time.Duration) time.Time {
	next := due.Add(every)
	if now.Sub(next) > every/4 {
		return now.Add(every)
	}
	return next
}

func (m *OS) handleMotionFrame(msg motionFrameMsg) tea.Cmd {
	m.tickStats.Motion++
	if msg.gen != m.motion.gen {
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
		m.motion.due[i] = nextDue(due, now, e.every(m))
	}
	if need {
		m.motion.drawn = now
	}
	m.renderSkipped = !need
	return tea.Batch(cmds...)
}

func (m *OS) shimmerCovered(now time.Time) bool {
	const margin = 15 * time.Millisecond
	last := m.frameRate.lastFrame
	return last.After(m.motion.drawn.Add(margin)) && now.Sub(last) < config.ShimmerFrame
}

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

func (m *OS) retireFades(now time.Time) {
	mo := &m.motion
	for i, start := range mo.fadeStart {
		if !start.IsZero() && now.Sub(start) >= m.fadeLength() {
			mo.fadeStart[i] = time.Time{}
			mo.fading--
		}
	}
}

func (m *OS) fadeLength() time.Duration {
	return m.Settings.MotionLength(config.AspectOverlays, config.OverlayFadeDuration)
}

func (m *OS) fadeAllowed() bool {
	return m.fadeLength() > 0 &&
		theme.Depth() == overlay.DepthTrueColor &&
		!m.screensaver.active
}

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

func (s *cellShade) setToward(c color.Color) {
	if isNilColor(s.toward) == isNilColor(c) && (isNilColor(c) || packColor8(s.toward) == packColor8(c)) {
		return
	}
	s.toward = c
	clear(s.blend)
	s.run.have = false
}
