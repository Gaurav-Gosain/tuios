package app

import (
	"image/color"
	"math"
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A celebration is a short confetti burst drawn in terminal cells over the
// composed frame. It is for a guided tour that wants to mark a finished step,
// and nothing in tuios starts one on its own.
//
// Where it draws. composeFrame runs draw over the canvas after the spotlight
// pass and before Render, the same place and for the same reason as the beam:
// the canvas is rebuilt from the pane caches on every frame, so writing glyphs
// into it touches no cache, the panes keep updating underneath, and the first
// frame composed after the last particle dies is the screen exactly as it would
// have been without the burst.
//
// Why not tuiffects. Its particle pool lives inside an Engine that owns a
// captured canvas and renders its own frame string, which is the screen saver's
// model: a picture of the screen replaces the screen. A burst over live panes
// wants a handful of positions and nothing else, so it is written out here.
//
// What it costs. Nothing while idle: no tick, no term in tickNeedsWork, and a
// length check in composeFrame and in the fast path test. While a burst is on
// screen it drives its own frames at celebrateFPS and stops asking for them
// once every particle is gone.
//
// Motion. Each particle's position is a closed form in its age (gravity with
// linear drag, plus a small sideways wobble), so a dropped or late frame never
// changes the path, and a test can ask for any instant directly.

// celebrateFrameMsg asks a running celebration for its next frame. at is when
// the timer fired, which is the instant the frame is drawn for.
type celebrateFrameMsg struct{ at time.Time }

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
	// celebrateFPS caps the frame rate. Confetti in cells moves a cell or two
	// per frame at most, and a browser client pays for every frame it composes,
	// so the session's NormalFPS (which can be 240) would be work nobody sees.
	celebrateFPS = 30
	// celebrateGravity is in rows per second squared, and celebrateDrag is the
	// linear drag coefficient per second. Together they give a terminal fall
	// speed of gravity/drag rows a second, which is what makes the confetti
	// float down rather than drop.
	celebrateGravity = 30.0
	celebrateDrag    = 1.9
	// celebrateAspect is how many columns make the same distance on screen as
	// one row.
	celebrateAspect = 2.0
	// celebrateStillLife is how long the static sparkle stays up.
	celebrateStillLife = 700 * time.Millisecond
	// celebrateMaxParticles bounds the state however often a caller fires.
	celebrateMaxParticles = 400
)

// celebrateGlyphs are the particle shapes. Each kind has a fresh glyph and the
// smaller one it becomes as it fades. The first two kinds are stars, which
// twinkle and are drawn bold.
var celebrateGlyphs = [][2]string{
	{"✦", "✧"},
	{"✦", "✧"},
	{"•", "·"},
	{"▪", "▫"},
	{"*", "+"},
	{"+", "·"},
}

// celebrateTumble is a scrap of paper turning over: a half block that goes
// round the four sides of its cell, then a small quadrant as it fades.
var (
	celebrateTumble      = [4]string{"▀", "▐", "▄", "▌"}
	celebrateTumbleFaded = [4]string{"▘", "▝", "▗", "▖"}
)

// celebrateParticle is one piece of confetti.
type celebrateParticle struct {
	// born is when the particle appears, and life how long it lasts.
	born time.Time
	life time.Duration
	// x0, y0 is the cell it starts from; vx, vy its launch velocity in cells
	// per second.
	x0, y0, vx, vy float64
	// wobble is the amplitude of the sideways drift in columns, and freq and
	// phase shape it.
	wobble, freq, phase float64
	// kind indexes celebrateGlyphs, or is -1 for a tumbling scrap.
	kind int
	ink  color.Color
	// still pins the particle to its start cell.
	still bool
}

// celebrationState is the running burst. The zero value is off.
type celebrationState struct {
	particles []celebrateParticle
	// at is the instant the next draw renders.
	at time.Time
	// ticking is true while a frame timer is in flight, so a second burst fired
	// during the first joins the one chain rather than starting another.
	ticking bool
}

// active reports whether anything is left to draw.
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
	if !m.Settings.AnimationsEnabled || m.Settings.AnimationsSuppressed {
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
		// A spray sized to the screen: a small client gets a small burst rather
		// than confetti that leaves it on the first frame.
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
	return m.celebrateTick()
}

// celebrateOrigin is the middle of a window's box, or of the screen.
func (m *OS) celebrateOrigin(w *terminal.Window) (int, int) {
	if w != nil && !w.Minimized && w.Width > 0 && w.Height > 0 {
		return w.X + w.Width/2, w.Y + w.Height/2
	}
	return m.GetRenderWidth() / 2, m.GetRenderHeight() / 2
}

// celebrateTick schedules the next frame, unless one is in flight or nothing
// is left. A burst of still particles needs one frame to show and one timer to
// clear, so it waits for its last particle rather than ticking at a frame rate.
func (m *OS) celebrateTick() tea.Cmd {
	c := &m.celebration
	if c.ticking || !c.active() {
		return nil
	}
	interval := time.Second / celebrateFPS
	if fps := m.Settings.NormalFPS; fps > 0 && fps < celebrateFPS {
		interval = time.Second / time.Duration(fps)
	}
	if !c.moving() {
		interval = max(c.lastDeath().Sub(c.at), time.Millisecond)
	}
	c.ticking = true
	return tea.Tick(interval, func(t time.Time) tea.Msg { return celebrateFrameMsg{at: t} })
}

// handleCelebrateFrame advances the burst to now and asks for the next frame
// while anything is left. The frame after the last particle dies draws the
// screen without confetti, and no further timer is scheduled.
func (m *OS) handleCelebrateFrame(now time.Time) tea.Cmd {
	c := &m.celebration
	c.ticking = false
	if !c.active() {
		return nil
	}
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
	m.renderSkipped = false
	return m.celebrateTick()
}

// moving reports whether any particle needs frames to move.
func (c *celebrationState) moving() bool {
	for i := range c.particles {
		if !c.particles[i].still {
			return true
		}
	}
	return false
}

// lastDeath is when the longest lived particle goes.
func (c *celebrationState) lastDeath() time.Time {
	var last time.Time
	for i := range c.particles {
		if d := c.particles[i].born.Add(c.particles[i].life); d.After(last) {
			last = d
		}
	}
	return last
}

// celebratePalette is the theme's accent, weighted to come up most, and the
// bright ANSI colours around it. With no theme the ANSI entries are the host's
// own palette indices, so the confetti is in the user's terminal colours.
func celebratePalette() []color.Color {
	ui := theme.UI()
	ansi := theme.GetANSIPalette()
	return []color.Color{
		ui.AccentBright, ui.AccentBright, ui.Accent,
		ansi[13], ansi[14], ansi[11], ansi[10], ansi[12], ansi[13],
	}
}

// appendBurst adds one spray of n particles from (x, y). scale stretches the
// launch speed.
func appendBurst(ps []celebrateParticle, born time.Time, x, y float64, n int, scale float64, pal []color.Color) []celebrateParticle {
	for i := range n {
		// Mostly upwards, in a cone 75 degrees either side of vertical, with a
		// few thrown lower so the burst has a round edge at the sides.
		spread := 75.0
		if i%6 == 0 {
			spread = 110
		}
		theta := (rand.Float64()*2 - 1) * spread * math.Pi / 180
		speed := (0.35 + 0.65*math.Sqrt(rand.Float64())) * 28 * scale
		// About one in eight is a tumbling scrap. A half block is the heaviest
		// shape here, and more of them makes the burst look blocky.
		kind := rand.IntN(len(celebrateGlyphs))
		if rand.IntN(8) == 0 {
			kind = -1
		}
		ps = append(ps, celebrateParticle{
			// A small stagger so the burst pops rather than appearing whole.
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

// appendStillSparkle adds a ring of stars around (x, y) that does not move.
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
			kind:  i % 2 * 2, // stars and dots
			ink:   pal[i%len(pal)],
			still: true,
		})
	}
	return append(ps, celebrateParticle{born: born, life: celebrateStillLife, x0: x, y0: y, ink: pal[0], still: true})
}

// position is where a particle is at age t seconds.
//
// Vertical motion is gravity against linear drag: v' = g - k v, whose solution
// is y = y0 + (g/k) t + (vy - g/k)(1 - e^(-kt))/k. Horizontal motion is the
// same drag with no gravity, plus the wobble.
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

// glyph is what the particle looks like at age t, and whether it is fading.
func (p *celebrateParticle) glyph(t, frac float64) (string, bool) {
	fading := frac > 0.8
	if p.kind < 0 {
		// A quarter turn every tenth of a second or so, from a starting side
		// of its own.
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
		// Stars twinkle while they are fresh.
		if int(t*8+p.phase)%3 == 0 {
			return g[1], false
		}
	}
	return g[0], false
}

// draw writes every live particle into the canvas at the instant c.at.
//
// A particle takes the glyph and ink and keeps the background of the cell it
// lands on, so it reads as floating over the pane rather than punching a hole
// in it. Cells that are part of a wide glyph are left alone: overwriting half
// of one would shift the rest of its row.
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
		// A wide glyph's first cell has width 2 and the cell after it is a
		// zero width placeholder, so this one check skips both halves.
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
