package app

import (
	"image/color"
	"math"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A theme transition shows a change of theme as motion rather than a cut:
// the new theme grows from the middle of the screen as a circle over the old
// one (appearance.theme_transition = "circle"), or the whole screen crosses
// over (= "fade"). It is off unless the config asks for it.
//
// How it works. The frame on screen when the theme changes is the old theme,
// so it is kept: a copy of the last composed canvas, or the last frame string
// parsed back to cells when the fast path drew it. While the transition runs,
// every frame is composed as usual in the new theme, and a pass at the end of
// composeFrame puts the old frame's cells back outside the circle. What is
// outside the circle is therefore the screen as it was, frozen for the few
// hundred milliseconds the circle takes to cover it.
//
// Where the change is noticed. theme.Generation moves on every change of the
// theme in force, whatever made it: the picker and its previews, set-config,
// a config reload, the Omarchy live follow. motionCmd, which runs after every
// Update, compares it with the generation this client last saw. That is
// before the View that draws the new theme, so the canvas still holds the old
// frame, and it is where the motion clock is armed. The frames come from that
// clock (motion.go), at the overlay fade's rate or the session's frame rate,
// whichever is slower.
//
// What it costs. Off or idle, one atomic load per Update and a bool test per
// frame. While running, the fast path is off, and the pass copies the cells
// outside the circle from the kept frame, a row segment at a time. Blends for
// the soft edge and the fade are kept in a small table keyed on the two
// colours, so a frame allocates nothing once the palette has been seen. The
// kept frame and the table are dropped when the transition ends.
//
// Every client runs its own: an SSH or web client has its own OS, its own
// canvas and its own motion clock, so each animates its own screen at its own
// frame rate, and nothing goes over the wire but the frames it draws anyway.

// themeTransition is this client's running theme transition. The zero value
// is idle.
type themeTransition struct {
	// gen is the theme generation this client last saw.
	gen uint64
	// style is the transition running: config.ThemeTransitionFade or Circle,
	// or "" for none.
	style string
	// pending is set when the theme changed and the old frame has not been
	// kept yet. The next composeFrame keeps it and starts the clock.
	pending bool
	// asked is when the change was noticed, so a transition whose frame is
	// never composed still ends.
	asked time.Time
	// start is when the first frame was composed, zero before it.
	start time.Time
	dur   time.Duration
	// soft blends the circle's edge, and the fade, in OKLab. Truecolor only:
	// at 256 or 16 colours a blend snaps between unrelated colours.
	soft bool

	// lastFrame is the last frame composeFrame returned. It is the old
	// frame when the fast path drew it, which leaves no canvas. Holding it
	// costs nothing: it is the string the renderer already holds.
	lastFrame string
	// old is the frame in the old theme.
	old *frameCanvas
	// mix holds the blends made so far, keyed on the two colours (24 bits
	// each) and the level.
	mix map[uint64]color.Color
}

const (
	// themeMixLevels is how many steps a blend is cut into: about one a
	// frame for a fade at the default length, and it bounds the table.
	themeMixLevels = 32
	// themeEdgeRows is the width of the circle's soft edge, in rows.
	themeEdgeRows = 1.0
	// themeMixMax bounds the blend table. A frame full of truecolor content
	// could otherwise grow it without end; past the bound it is cleared.
	themeMixMax = 4096
)

// running reports whether a transition is pending or drawing.
func (t *themeTransition) running() bool { return t.style != "" }

// end stops the transition and lets the kept frame and the blend table go.
// A table filled to its bound is some hundred kilobytes, and an idle client
// has no use for it until the next change of theme.
func (t *themeTransition) end() {
	t.style, t.pending = "", false
	t.start, t.asked = time.Time{}, time.Time{}
	t.old, t.mix = nil, nil
}

// noticeThemeChange starts a transition when the theme changed since this
// client last looked. It runs after every Update, so it must be cheap when
// nothing changed: one atomic load and a compare.
func (m *OS) noticeThemeChange() {
	t := &m.themeFx
	g := theme.Generation()
	if g == t.gen {
		return
	}
	t.gen = g
	// Before the first frame there is nothing on screen to replace: the
	// theme set at startup lands here. Generation itself is no guide, since
	// it is still zero after a start with no theme, and the first theme
	// picked after that must animate like any other.
	if t.lastFrame == "" {
		return
	}
	style := m.Settings.ThemeTransitionStyle()
	soft := theme.Depth() == overlay.DepthTrueColor
	if style == config.ThemeTransitionFade && !soft {
		style = config.ThemeTransitionNone
	}
	// The theme editor changes the theme on every edit. A transition per
	// keypress would hide the change being made.
	if style == config.ThemeTransitionNone || m.screensaver.active || m.themeEditor.open {
		t.end()
		return
	}
	// A change while one runs, such as the next preview in the picker, keeps
	// the frame on screen now, half and half, as the old one, and the circle
	// starts again from the middle.
	t.style, t.soft, t.pending = style, soft, true
	t.asked = time.Now()
	t.dur = m.Settings.ThemeTransitionDuration()
	m.renderSkipped = false
}

// themeTransitionFrame is the motion clock's interval while a transition
// runs: the overlay fade's 60 frames a second, or the session's own frame
// rate when that is lower, as on a slow SSH or web client.
func (m *OS) themeTransitionFrame() time.Duration {
	frame := config.OverlayFadeFrame
	if fps := m.Settings.NormalFPS; fps > 0 {
		frame = max(frame, time.Second/time.Duration(fps))
	}
	return frame
}

// expireThemeTransition ends a transition that has run its course without
// its last frame being composed, so the clock cannot run on for one that
// nothing draws. It reports whether a transition was running, which asks the
// motion clock for a frame.
func (m *OS) expireThemeTransition(now time.Time) bool {
	t := &m.themeFx
	if !t.running() {
		return false
	}
	from := t.start
	if from.IsZero() {
		from = t.asked
	}
	if now.Sub(from) >= t.dur {
		t.end()
	}
	return true
}

// keepThemeFrame keeps the frame on screen as the old theme's. It runs at
// the top of composeFrame, before the canvas is cleared for the new frame.
func (m *OS) keepThemeFrame() {
	t := &m.themeFx
	t.pending = false
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	if w <= 0 || h <= 0 {
		t.end()
		return
	}
	if t.old == nil {
		t.old = &frameCanvas{Buffer: *uv.NewBuffer(w, h)}
	}
	old := t.old
	src := m.renderCanvas
	if src != nil && m.canvasIsFrame && src.Width() == w && src.Height() == h {
		// A transition restarted mid-way keeps the half and half frame on
		// screen; old is not src, so the copy is safe.
		old.Resize(w, h)
		for y := range h {
			copy(old.Lines[y], src.Lines[y])
		}
	} else {
		// The fast path or the saver drew the last frame, and left no canvas
		// behind. The frame string is the same cells.
		old.Resize(w, h)
		old.Clear()
		uv.NewStyledString(t.lastFrame).Draw(old, uv.Rect(0, 0, w, h))
	}
	t.start = time.Now()
}

// themeEase is ease-out cubic: fast at first, settling gently onto the end.
func themeEase(p float64) float64 {
	p = min(max(p, 0), 1)
	q := 1 - p
	return 1 - q*q*q
}

// applyThemeTransition puts the old theme's cells back where the new theme
// has not reached yet. It runs on the composed canvas, after every other
// pass, so what it keeps is exactly the screen as it was.
func (m *OS) applyThemeTransition(canvas *frameCanvas, now time.Time) {
	t := &m.themeFx
	if t.pending || t.old == nil {
		return
	}
	p := float64(now.Sub(t.start)) / float64(t.dur)
	w, h := canvas.Width(), canvas.Height()
	if p >= 1 || t.old.Width() != w || t.old.Height() != h {
		// Done, or the screen changed size and the old frame no longer lines
		// up: the new theme takes the whole screen.
		t.end()
		return
	}
	e := themeEase(p)
	if t.style == config.ThemeTransitionFade {
		level := uint8(math.Round(e * themeMixLevels))
		for y := range h {
			line, old := canvas.Lines[y], t.old.Lines[y]
			for x := range min(len(line), len(old)) {
				m.themeBlendCell(&line[x], &old[x], level)
			}
		}
		return
	}
	m.themeCircle(canvas, e)
}

// themeCircle keeps the old frame outside a circle of the new theme centred
// on the screen. Distances are measured in pixels of the host's cells, so the
// circle is round on screen however tall a cell is.
func (m *OS) themeCircle(canvas *frameCanvas, e float64) {
	t := &m.themeFx
	w, h := canvas.Width(), canvas.Height()
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 1, 2
	}
	ax, ay := float64(cw), float64(ch)
	cx, cy := float64(w)/2, float64(h)/2
	edge := 0.0
	if t.soft {
		edge = themeEdgeRows * ay
	}
	// The radius runs to the farthest corner plus the soft edge, so the last
	// frame before the end has no old cell left.
	r := e * (math.Hypot(cx*ax, cy*ay) + edge)
	inner := max(r-edge, 0)
	for y := range h {
		line, old := canvas.Lines[y], t.old.Lines[y]
		dy := (float64(y) + 0.5 - cy) * ay
		// The span of this row inside the circle, in columns. Outside it the
		// row is the old frame; inside the inner circle it is the new one.
		lo, hi := circleSpan(r, dy, ax, cx, w)
		if lo >= hi {
			copy(line, old)
			continue
		}
		copy(line[:lo], old[:lo])
		copy(line[hi:], old[hi:])
		if edge == 0 {
			fixWideEdges(line, lo, hi)
			continue
		}
		// The inner span is the new theme already, so the blend skips it. A
		// row the inner circle does not reach has an empty one, and the
		// whole of lo to hi is soft edge.
		ilo, ihi := circleSpan(inner, dy, ax, cx, w)
		for x := lo; x < hi && x < len(old); x++ {
			if x >= ilo && x < ihi {
				x = ihi - 1
				continue
			}
			f := (r - math.Hypot((float64(x)+0.5-cx)*ax, dy)) / edge
			m.themeBlendCell(&line[x], &old[x], uint8(math.Round(min(max(f, 0), 1)*themeMixLevels)))
		}
		fixWideEdges(line, lo, hi)
	}
}

// circleSpan is the half-open range of columns whose centres fall inside a
// circle of radius r, at a row dy pixels from its centre.
func circleSpan(r, dy, ax, cx float64, w int) (lo, hi int) {
	if r <= math.Abs(dy) {
		return 0, 0
	}
	half := math.Sqrt(r*r-dy*dy) / ax
	lo = int(math.Ceil(cx - half - 0.5))
	hi = int(math.Floor(cx+half-0.5)) + 1
	return min(max(lo, 0), w), min(max(hi, 0), w)
}

// themeBlendCell blends one cell from the old frame's toward its own, by
// l out of themeMixLevels. The glyph is the new one from half way, and a
// colour either side leaves to the terminal (nil) switches at half way too,
// since there is nothing to blend it with.
//
// The ink crosses over in the middle fifth of the blend rather than over the
// whole of it. Between a light theme and a dark one the ink and the ground
// move in opposite directions, and blended at the same rate they meet half
// way: every glyph on the screen went to the ground's grey and vanished. A
// short crossing keeps the ink near one end while the ground is in the middle.
func (m *OS) themeBlendCell(n, o *uv.Cell, l uint8) {
	switch {
	case l >= themeMixLevels:
		return
	case l == 0:
		*n = *o
		return
	}
	ink := uint8(min(max(int(l)*5-themeMixLevels*2, 0), themeMixLevels))
	fg, bg := m.themeMix(o.Style.Fg, n.Style.Fg, ink), m.themeMix(o.Style.Bg, n.Style.Bg, l)
	if l < themeMixLevels/2 {
		*n = *o
	}
	n.Style.Fg, n.Style.Bg = fg, bg
}

// themeMix is a toward b at level, from the table when it has been made
// before.
func (m *OS) themeMix(a, b color.Color, level uint8) color.Color {
	if isNilColor(a) || isNilColor(b) {
		if level < themeMixLevels/2 {
			return a
		}
		return b
	}
	pa, pb := packColor8(a), packColor8(b)
	if pa == pb {
		return b
	}
	k := uint64(pa)<<32 | uint64(pb)<<8 | uint64(level)
	t := &m.themeFx
	if c, ok := t.mix[k]; ok {
		return c
	}
	if t.mix == nil {
		t.mix = make(map[uint64]color.Color, 256)
	} else if len(t.mix) >= themeMixMax {
		clear(t.mix)
	}
	c := overlay.MixColors(a, b, float64(level)/themeMixLevels)
	t.mix[k] = c
	return c
}

// fixWideEdges repairs the wide glyphs the circle's edge cut in half on one
// row: a head whose second half came from the other frame, or a second half
// whose head did. Either becomes a blank in its own style, so no glyph is
// drawn across the edge and no column shifts. Only the cells from lo-1 to hi
// can mix the two frames: the edges themselves, and the soft band between.
func fixWideEdges(line uv.Line, lo, hi int) {
	for x := max(lo-1, 0); x <= hi && x < len(line); x++ {
		c := &line[x]
		switch {
		case c.Width > 1 && (x+1 >= len(line) || line[x+1].Width != 0):
			c.Content, c.Width = " ", 1
		case c.Width == 0 && (x == 0 || line[x-1].Width < 2):
			c.Content, c.Width = " ", 1
		}
	}
}
