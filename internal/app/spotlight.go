package app

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	spotlightMinBrightness = 0.2
	spotlightCacheMax      = 4096
)

var spotlightDark = theme.SpotlightShade()

type spotBlendKey struct {
	src   uint32
	level uint8
}

type spotlightRun struct {
	inFg, inBg   color.Color
	level        uint8
	have         bool
	outFg, outBg color.Color
	outFaint     bool
}

type spotlightState struct {
	on       bool
	x, y     int
	anchored bool
	chip     spotlightChipHit

	cellShade
}

type cellShade struct {
	toward  color.Color
	noFaint bool

	groundFg, groundBg color.Color
	groundTheme        uint64
	groundValid        bool

	levels    [config.SpotlightLevels]float64
	levelsDim int

	blend map[spotBlendKey]color.Color
	run   spotlightRun
}

func (m *OS) spotlightConfig() config.SpotlightConfig {
	if m.UserConfig == nil {
		return config.SpotlightConfig{}
	}
	return m.UserConfig.Spotlight
}

// SpotlightOn reports whether this client is drawing the beam.
func (m *OS) SpotlightOn() bool { return m.spotlight.on }

// ToggleSpotlight flips the beam, mirrors the new state into the persisted
// [spotlight] enabled config, and saves it. Shared by the keybinding, the
// command palette and the settings row so all three stay in step and the choice
// survives a restart.
func (m *OS) ToggleSpotlight() tea.Cmd {
	m.SetSpotlight(!m.spotlight.on)
	return m.persistSettings()
}

// SetSpotlight puts the beam in one state and records it, without saving. The
// settings row uses it, and so does the startup path.
func (m *OS) SetSpotlight(on bool) {
	m.spotlight.on = on
	if m.UserConfig != nil {
		m.UserConfig.Spotlight.Enabled = new(on)
	}
}

func (m *OS) spotlightAnchor() (int, int) {
	if m.spotlightConfig().FollowMode() == config.SpotlightFollowMouse {
		if px, py := m.PointerSeen(); px > 0 || py > 0 {
			m.spotlight.x, m.spotlight.y = px, py
			m.spotlight.anchored = true
		}
	} else if c := m.getRealCursor(); c != nil {
		m.spotlight.x, m.spotlight.y = c.X, c.Y
		m.spotlight.anchored = true
	}
	if !m.spotlight.anchored {
		if c := m.getRealCursor(); c != nil {
			m.spotlight.x, m.spotlight.y = c.X, c.Y
		} else {
			m.spotlight.x = m.GetRenderWidth() / 2
			m.spotlight.y = m.GetRenderHeight() / 2
		}
	}
	return m.spotlight.x, m.spotlight.y
}

type cellCanvas interface {
	Width() int
	Height() int
	CellAt(x, y int) *uv.Cell
}

func (m *OS) applySpotlight(canvas cellCanvas) {
	cfg := m.spotlightConfig()
	cx, cy := m.spotlightAnchor()
	m.spotlight.apply(canvas, cx, cy, cfg.RadiusRows(), cfg.DimPercent(),
		cfg.EdgeStyle() == config.SpotlightEdgeSoft, m.spotlightLitSpans())
}

func (s *spotlightState) apply(canvas cellCanvas, cx, cy, radius, dim int, soft bool, keep [2]spotlightSpan) {
	width, height := canvas.Width(), canvas.Height()
	if width <= 0 || height <= 0 || radius <= 0 {
		return
	}
	s.syncGround()
	s.syncLevels(dim)
	s.run.have = false

	const maxLevel = config.SpotlightLevels - 1
	rad := float64(radius)
	full := rad
	if soft {
		full = rad * (1 - config.SpotlightFalloff)
	}
	rim := rad - full

	for y := range height {
		dy := float64(y - cy)
		lit, x0, x1 := spotlightRowSpan(cx, dy, rad, width)
		var k0, k1 spotlightSpan
		if keep[0].y == y {
			k0 = keep[0]
		}
		if keep[1].y == y {
			k1 = keep[1]
		}
		for x := range width {
			if (x >= k0.x0 && x < k0.x1) || (x >= k1.x0 && x < k1.x1) {
				continue
			}
			cell := canvas.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				continue
			}
			level := uint8(maxLevel)
			if lit && x >= x0 && x <= x1 {
				if !soft {
					level = 0
				} else {
					level = spotlightLevel(float64(x-cx), dy, full, rim)
				}
			}
			if level == 0 {
				continue
			}
			s.dimCell(cell, level)
		}
	}
}

func spotlightRowSpan(cx int, dy, rad float64, width int) (lit bool, x0, x1 int) {
	if dy > rad || dy < -rad {
		return false, 0, -1
	}
	half := 2 * math.Sqrt(rad*rad-dy*dy)
	x0 = max(int(math.Ceil(float64(cx)-half)), 0)
	x1 = min(int(math.Floor(float64(cx)+half)), width-1)
	return x0 <= x1, x0, x1
}

func spotlightLevel(dx, dy, full, rim float64) uint8 {
	dx /= 2
	d := math.Sqrt(dx*dx + dy*dy)
	if d <= full || rim <= 0 {
		return 0
	}
	brightness := max(1-(d-full)/rim, spotlightMinBrightness)
	t := (1 - brightness) / (1 - spotlightMinBrightness)
	level := int(t*float64(config.SpotlightLevels-1) + 0.5)
	return uint8(min(max(level, 0), config.SpotlightLevels-1))
}

func (s *cellShade) dimCell(cell *uv.Cell, level uint8) {
	fg, bg := cell.Style.Fg, cell.Style.Bg
	r := &s.run
	if !r.have || r.level != level || r.inFg != fg || r.inBg != bg {
		s.buildRun(fg, bg, level)
	}
	cell.Style.Fg, cell.Style.Bg = r.outFg, r.outBg
	if r.outFaint {
		cell.Style.Attrs |= uv.AttrFaint
	}
}

func (s *cellShade) buildRun(fg, bg color.Color, level uint8) {
	r := &s.run
	r.inFg, r.inBg, r.level, r.have = fg, bg, level, true

	if isNilColor(fg) {
		fg = s.groundFg
	}
	if isNilColor(bg) {
		bg = s.groundBg
	}
	t := s.levels[level]
	if s.dimmable(fg) {
		r.outFg, r.outFaint = s.blendCached(fg, level, t), false
	} else {
		r.outFg, r.outFaint = fg, !s.noFaint
	}
	switch {
	case isNilColor(r.inBg) && !isNilColor(s.toward) && !isNilColor(s.groundBg) &&
		packColor8(s.toward) == packColor8(s.groundBg):
		r.outBg = r.inBg
	case s.dimmable(bg):
		r.outBg = s.blendCached(bg, level, t)
	default:
		r.outBg = bg
	}
}

func (s *cellShade) dimmable(c color.Color) bool {
	if isNilColor(c) {
		return false
	}
	if s.groundBg != nil {
		return true
	}
	return !hostPaletteColor(c)
}

func hostPaletteColor(c color.Color) bool {
	if _, ok := c.(ansi.BasicColor); ok {
		return true
	}
	if i, ok := c.(ansi.IndexedColor); ok {
		return i < 16
	}
	return false
}

func (s *cellShade) blendCached(src color.Color, level uint8, t float64) color.Color {
	key := spotBlendKey{src: packColor8(src), level: level}
	if c, ok := s.blend[key]; ok {
		return c
	}
	toward := s.toward
	if toward == nil {
		toward = spotlightDark
	}
	c := blendColors(src, toward, t)
	if s.blend == nil {
		s.blend = make(map[spotBlendKey]color.Color, 256)
	} else if len(s.blend) >= spotlightCacheMax {
		clear(s.blend)
	}
	s.blend[key] = c
	return c
}

func packColor8(c color.Color) uint32 {
	if isNilColor(c) {
		return 0
	}
	r, g, b, _ := c.RGBA()
	return (r>>8)<<16 | (g>>8)<<8 | (b >> 8)
}

func (s *cellShade) syncGround() {
	gen := theme.Generation()
	if s.groundValid && s.groundTheme == gen {
		return
	}
	s.groundTheme, s.groundValid = gen, true
	s.groundFg, s.groundBg = dimGround()
	clear(s.blend)
	s.run.have = false
}

func (s *cellShade) syncLevels(dim int) {
	if s.levelsDim == dim {
		return
	}
	s.levelsDim = dim
	dark := float64(dim) / 100
	for i := range s.levels {
		s.levels[i] = dark * float64(i) / float64(config.SpotlightLevels-1)
	}
	clear(s.blend)
	s.run.have = false
}
