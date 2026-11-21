package app

import (
	"image/color"
	"math"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// CelebrateOptions says what kind of burst to draw.
type CelebrateOptions struct {
	// Big is a larger finale: more particles, a wider spray, and two follow-up
	// bursts either side of the first.
	Big bool
	// Still draws a static sparkle that is shown once and cleared, with no
	// motion. It is what a client that asked for reduced motion gets, and what
	// every client gets while animations are disabled.
	Still bool
}

const (
	celebrateFPS          = 30
	celebrateGravity      = 30.0
	celebrateDrag         = 1.9
	celebrateAspect       = 2.0
	celebrateStillLife    = 700 * time.Millisecond
	celebrateMaxParticles = 400
)

var celebrateGlyphs = [][2]string{
	{"✦", "✧"},
	{"✦", "✧"},
	{"•", "·"},
	{"◆", "◇"},
	{"*", "+"},
	{"+", "·"},
}

var (
	celebrateTumble      = [4]string{"▀", "▐", "▄", "▌"}
	celebrateTumbleFaded = [4]string{"▘", "▝", "▗", "▖"}
)

type celebrateParticle struct {
	born                time.Time
	life                time.Duration
	x0, y0, vx, vy      float64
	wobble, freq, phase float64
	kind                int
	ink                 color.Color
	still               bool
}

type celebrationState struct {
	particles []celebrateParticle
	at        time.Time
}

func (c *celebrationState) active() bool { return len(c.particles) > 0 }

// Celebrate bursts confetti from the centre of the focused pane, or from the
// middle of the screen when no pane has focus. The returned command drives the
// animation; the caller hands it back to Bubble Tea.
func (m *OS) Celebrate(opts CelebrateOptions) tea.Cmd {
	x, y := m.celebrateOrigin(m.GetFocusedWindow())
	return m.CelebrateAt(x, y, opts)
}

// CelebrateWindow bursts confetti from the centre of the window with the given
// id. An unknown, hidden or minimized window falls back to Celebrate.
func (m *OS) CelebrateWindow(id string, opts CelebrateOptions) tea.Cmd {
	w := m.windowByID(id)
	if w == nil || w.Minimized || w.Workspace != m.CurrentWorkspace {
		return m.Celebrate(opts)
	}
	x, y := m.celebrateOrigin(w)
	return m.CelebrateAt(x, y, opts)
}

// CelebrateAt bursts confetti from the screen cell (x, y).
func (m *OS) CelebrateAt(x, y int, opts CelebrateOptions) tea.Cmd {
	width, height := m.GetRenderWidth(), m.GetRenderHeight()
	if width <= 0 || height <= 0 {
		return nil
	}
	if !m.Settings.MotionOn(config.AspectConfetti) {
		opts.Still = true
	}
	x = clampInt(x, 0, width-1)
	y = clampInt(y, 0, height-1)
	now := time.Now()
	c := &m.celebration
	if !c.active() {
		c.at = now
	}
	pal := celebratePalette()
	if opts.Still {
		c.particles = appendStillSparkle(c.particles, now, float64(x), float64(y), opts.Big, pal)
	} else {
		scale := math.Max(0.55, math.Min(1.25, float64(height)/32))
		if opts.Big {
			c.particles = appendBurst(c.particles, now, float64(x), float64(y), 70, scale*1.15, pal)
			side := math.Max(6, float64(width)/5)
			c.particles = appendBurst(c.particles, now.Add(220*time.Millisecond), float64(x)-side, float64(y)+1, 34, scale*0.85, pal)
			c.particles = appendBurst(c.particles, now.Add(380*time.Millisecond), float64(x)+side, float64(y)+1, 34, scale*0.85, pal)
		} else {
			c.particles = appendBurst(c.particles, now, float64(x), float64(y), 64, scale, pal)
		}
	}
	if n := len(c.particles); n > celebrateMaxParticles {
		c.particles = c.particles[n-celebrateMaxParticles:]
	}
	m.renderSkipped = false
	return m.motionCmd()
}

func (m *OS) celebrateOrigin(w *terminal.Window) (int, int) {
	if w != nil && !w.Minimized && w.Width > 0 && w.Height > 0 {
		return m.sessionView.toScreen(w.X+w.Width/2, w.Y+w.Height/2)
	}
	return m.GetRenderWidth() / 2, m.GetRenderHeight() / 2
}

func (m *OS) celebrateEvery() time.Duration {
	c := &m.celebration
	if !c.moving() {
		return max(c.lastDeath().Sub(c.at), time.Millisecond)
	}
	interval := time.Second / celebrateFPS
	if fps := m.Settings.NormalFPS; fps > 0 && fps < celebrateFPS {
		interval = time.Second / time.Duration(fps)
	}
	return interval
}

func (m *OS) stepCelebration(now time.Time) (bool, tea.Cmd) {
	c := &m.celebration
	c.at = now
	alive := c.particles[:0]
	for _, p := range c.particles {
		if now.Sub(p.born) < p.life {
			alive = append(alive, p)
		}
	}
	clear(c.particles[len(alive):])
	c.particles = alive
	if len(alive) == 0 {
		c.particles = nil
	}
	return true, nil
}

func (c *celebrationState) moving() bool {
	for i := range c.particles {
		if !c.particles[i].still {
			return true
		}
	}
	return false
}

func (c *celebrationState) lastDeath() time.Time {
	var last time.Time
	for i := range c.particles {
		if d := c.particles[i].born.Add(c.particles[i].life); d.After(last) {
			last = d
		}
	}
	return last
}

func celebratePalette() []color.Color {
	ui := theme.UI()
	ansi := theme.GetANSIPalette()
	return []color.Color{
		ui.AccentBright, ui.AccentBright, ui.Accent,
		ansi[13], ansi[14], ansi[11], ansi[10], ansi[12], ansi[13],
	}
}

func appendBurst(ps []celebrateParticle, born time.Time, x, y float64, n int, scale float64, pal []color.Color) []celebrateParticle {
	for i := range n {
		spread := 75.0
		if i%6 == 0 {
			spread = 110
		}
		theta := (rand.Float64()*2 - 1) * spread * math.Pi / 180
		speed := (0.35 + 0.65*math.Sqrt(rand.Float64())) * 28 * scale
		kind := rand.IntN(len(celebrateGlyphs))
		if rand.IntN(8) == 0 {
			kind = -1
		}
		ps = append(ps, celebrateParticle{
			born:   born.Add(time.Duration(rand.IntN(70)) * time.Millisecond),
			life:   time.Duration(1000+rand.IntN(500)) * time.Millisecond,
			x0:     x,
			y0:     y,
			vx:     math.Sin(theta) * speed * celebrateAspect,
			vy:     -math.Cos(theta) * speed,
			wobble: 0.4 + rand.Float64()*1.1,
			freq:   5 + rand.Float64()*5,
			phase:  rand.Float64() * 2 * math.Pi,
			kind:   kind,
			ink:    pal[rand.IntN(len(pal))],
		})
	}
	return ps
}

func appendStillSparkle(ps []celebrateParticle, born time.Time, x, y float64, big bool, pal []color.Color) []celebrateParticle {
	n, radius := 10, 3.0
	if big {
		n, radius = 16, 5.0
	}
	for i := range n {
		angle := float64(i) / float64(n) * 2 * math.Pi
		r := radius
		if i%2 == 1 {
			r *= 0.6
		}
		ps = append(ps, celebrateParticle{
			born:  born,
			life:  celebrateStillLife,
			x0:    x + math.Cos(angle)*r*celebrateAspect,
			y0:    y + math.Sin(angle)*r,
			kind:  i % 2 * 2,
			ink:   pal[i%len(pal)],
			still: true,
		})
	}
	return append(ps, celebrateParticle{born: born, life: celebrateStillLife, x0: x, y0: y, ink: pal[0], still: true})
}

func (p *celebrateParticle) position(t float64) (float64, float64) {
	if p.still {
		return p.x0, p.y0
	}
	ex := (1 - math.Exp(-celebrateDrag*t)) / celebrateDrag
	terminal := celebrateGravity / celebrateDrag
	x := p.x0 + p.vx*ex + p.wobble*math.Sin(p.freq*t+p.phase)*math.Min(1, t*3)
	y := p.y0 + terminal*t + (p.vy-terminal)*ex
	return x, y
}

func (p *celebrateParticle) glyph(t, frac float64) (string, bool) {
	fading := frac > 0.8
	if p.kind < 0 {
		i := int(t*9+p.phase*2) & 3
		if frac > 0.68 {
			return celebrateTumbleFaded[i], fading
		}
		return celebrateTumble[i], fading
	}
	g := celebrateGlyphs[p.kind]
	if frac > 0.68 {
		return g[1], fading
	}
	if p.kind <= 1 && !p.still {
		if int(t*8+p.phase)%3 == 0 {
			return g[1], false
		}
	}
	return g[0], false
}

func (c *celebrationState) draw(canvas cellCanvas) {
	width, height := canvas.Width(), canvas.Height()
	for i := range c.particles {
		p := &c.particles[i]
		age := c.at.Sub(p.born)
		if age < 0 || age >= p.life {
			continue
		}
		t := age.Seconds()
		fx, fy := p.position(t)
		x, y := int(math.Round(fx)), int(math.Round(fy))
		if x < 0 || y < 0 || x >= width || y >= height {
			continue
		}
		cell := canvas.CellAt(x, y)
		if cell == nil || cell.Width != 1 {
			continue
		}
		glyph, fading := p.glyph(t, t/p.life.Seconds())
		style := uv.Style{Fg: p.ink, Bg: cell.Style.Bg}
		if fading {
			style.Attrs = uv.AttrFaint
		} else if p.kind <= 1 {
			style.Attrs = uv.AttrBold
		}
		*cell = uv.Cell{Content: glyph, Width: 1, Style: style}
	}
}
