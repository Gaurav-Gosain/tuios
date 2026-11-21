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

type themeTransition struct {
	gen     uint64
	style   string
	pending bool
	asked   time.Time
	start   time.Time
	dur     time.Duration
	soft    bool

	drawn    bool
	lastText string
	old      *frameCanvas
	mix      map[uint64]color.Color
}

const (
	themeMixLevels = 32
	themeEdgeRows  = 1.0
	themeMixMax    = 4096
)

func (t *themeTransition) running() bool { return t.style != "" }

func (t *themeTransition) end() {
	t.style, t.pending = "", false
	t.start, t.asked = time.Time{}, time.Time{}
	t.old, t.mix = nil, nil
}

func (m *OS) noticeThemeChange() {
	t := &m.themeFx
	g := theme.Generation()
	if g == t.gen {
		return
	}
	t.gen = g
	if !t.drawn {
		return
	}
	style := m.Settings.ThemeTransitionStyle()
	soft := theme.Depth() == overlay.DepthTrueColor
	if style == config.ThemeTransitionFade && !soft {
		style = config.ThemeTransitionNone
	}
	if style == config.ThemeTransitionNone || m.screensaver.active || m.themeEditor.open {
		t.end()
		return
	}
	t.style, t.soft, t.pending = style, soft, true
	t.asked = time.Now()
	t.dur = m.Settings.ThemeTransitionDuration()
	m.renderSkipped = false
}

func (m *OS) themeTransitionFrame() time.Duration {
	frame := config.OverlayFadeFrame
	if fps := m.Settings.NormalFPS; fps > 0 {
		frame = max(frame, time.Second/time.Duration(fps))
	}
	return frame
}

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
		old.Resize(w, h)
		for y := range h {
			copy(old.Lines[y], src.Lines[y])
		}
	} else {
		old.Resize(w, h)
		old.Clear()
		uv.NewStyledString(t.lastText).Draw(old, uv.Rect(0, 0, w, h))
	}
	t.start = time.Now()
}

func themeEase(p float64) float64 {
	p = min(max(p, 0), 1)
	q := 1 - p
	return 1 - q*q*q
}

func (m *OS) applyThemeTransition(canvas *frameCanvas, now time.Time) {
	t := &m.themeFx
	if t.pending || t.old == nil {
		return
	}
	p := float64(now.Sub(t.start)) / float64(t.dur)
	w, h := canvas.Width(), canvas.Height()
	if p >= 1 || t.old.Width() != w || t.old.Height() != h {
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
	r := e * (math.Hypot(cx*ax, cy*ay) + edge)
	inner := max(r-edge, 0)
	for y := range h {
		line, old := canvas.Lines[y], t.old.Lines[y]
		dy := (float64(y) + 0.5 - cy) * ay
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

func circleSpan(r, dy, ax, cx float64, w int) (lo, hi int) {
	if r <= math.Abs(dy) {
		return 0, 0
	}
	half := math.Sqrt(r*r-dy*dy) / ax
	lo = int(math.Ceil(cx - half - 0.5))
	hi = int(math.Floor(cx+half-0.5)) + 1
	return min(max(lo, 0), w), min(max(hi, 0), w)
}

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
