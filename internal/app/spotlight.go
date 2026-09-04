package app

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The spotlight is one pass over the composed canvas that carries every cell
// outside an ellipse toward the ground. It is a presentation aid: a demo
// viewer looks where the light is, and the rest of the screen goes quiet
// without going away.
//
// Three decisions are load-bearing, and each of them was a measurement.
//
// Where it runs. The pass mutates m.renderCanvas in composeFrame, after
// GetCanvas has consumed every pane's cached layer and before Render turns the
// canvas into a string. lipgloss.Canvas.CellAt hands back a pointer into the
// buffer, so the pass edits in place and no cache upstream is invalidated. That
// is what lets the beam move on every frame for the price of the pass alone.
// Doing it in the pane cell loop instead would put the beam position in each
// pane's content cache key, so every pane would re-render on every move: 1.67 ms
// all-dirty against 0.90 ms one-dirty, and worse with each pane added. This pass
// is flat in pane count.
//
// What it costs. About 0.3 ms on a nine-pane 207x55 frame, and no allocations
// at all once the blend cache is warm. The naive spelling - blendColors per
// cell, boxing the result - allocates about 16,000 times a frame, which is what
// would make the feature a permanent tax rather than a thing you switch on.
// Two lines are the whole difference: the grounds are held as pre-boxed
// color.Color values rather than assigned from a color.RGBA per cell, and every
// blend goes through a cache of 16 quantised levels.
//
// What it must not skip. A blank cell that carries a colour is dimmed like any
// other. Skipping them "because nothing is visible there" reads as the obvious
// optimisation and is the opposite: it splits every word run in half at the
// space, and the frame goes from 11 KB to 40 KB.
//
// A cell the guest left at the terminal default gets the theme's foreground and
// is dimmed from there. That is most of a real screen - a shell prompt, ls
// output - and tuios emits no colour for any of it, so the version that
// followed dim_unfocused's rule and left a colourless cell alone dimmed the
// syntax highlighting and nothing else. No unit fixture full of explicit SGR
// can see that; the e2e that reads Cell.Fg off a real pane is what found it,
// and TestSpotlightDimsTextLeftAtTheTerminalDefault is what holds it.
//
// It is client-local, like the showkeys overlay. Nothing crosses the wire and a
// peer attached to the same session sees its own screen unchanged. The screen
// saver suspends the pass, the crash overlay never reaches it (View draws that
// before composeFrame), and everything else in the canvas - popups, pickers,
// panels - dims with the rest, because they are composed before the pass runs.

const (
	// spotlightMinBrightness is the floor on the rim, so the edge of the beam
	// is dim rather than black. It is the screen saver's own floor.
	spotlightMinBrightness = 0.2
	// spotlightCacheMax bounds the blend cache. A frame holds a few dozen
	// distinct colours, so this is never reached by ordinary content; a pane
	// painting a gradient would grow it without bound, and clearing is cheaper
	// than evicting.
	spotlightCacheMax = 4096
)

// spotBlendKey names one cached blend: a source colour, the colour it is
// carried toward, and which of the 16 levels it is carried to.
//
// The colours are packed to 8 bits per channel rather than held as interface
// values. Packing is what makes the key safe: a color.Color whose dynamic type
// is not comparable would panic a map lookup, and packing asks each colour for
// its channels instead of comparing the box it came in. It loses nothing,
// because blendColors reduces both operands to 8 bits anyway.
type spotBlendKey struct {
	src    uint32
	toward uint32
	level  uint8
}

// spotlightRun is the last cell the pass transformed, kept so a word or a line
// of one style pays the colour arithmetic once instead of once per cell.
//
// Adjacent cells almost always carry the same two colour values, which is the
// property the pane cell loop already batches on. Comparing the interfaces by
// identity is what safeColorEquals does first for the same reason.
type spotlightRun struct {
	inFg, inBg   color.Color
	level        uint8
	have         bool
	outFg, outBg color.Color
	writeBg      bool
}

// spotlightState is the beam this client is drawing. It is not session state:
// see the note at the top of the file.
type spotlightState struct {
	// on is whether the beam is drawn. Toggled by the keybinding, the palette
	// and the settings row, and seeded from [spotlight] enabled at startup.
	on bool
	// x, y is where the beam last had an answer, in screen cells. When the
	// anchor goes quiet - an overlay hides the cursor, or the pointer has not
	// moved yet - the beam stays here rather than jumping to the middle.
	x, y     int
	anchored bool

	// groundFg, groundBg are the pair every colour is carried toward, boxed
	// once. Assigning a color.RGBA into a color.Color per cell was the single
	// line that cost 8,000 allocations a frame.
	groundFg, groundBg color.Color
	groundTheme        string
	groundValid        bool

	// levels is the blend fraction for each of the 16 rim steps, rebuilt when
	// the configured dim changes.
	levels    [config.SpotlightLevels]float64
	levelsDim int

	blend map[spotBlendKey]color.Color
	run   spotlightRun
}

// spotlightConfig is the [spotlight] section this client holds.
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
		m.UserConfig.Spotlight.Enabled = boolPtr(on)
	}
}

// spotlightAnchor is where the beam is centred this frame, in screen cells.
//
// Cursor is the primary anchor because it costs nothing: getRealCursor already
// resolves the focused pane's cursor for the hardware cursor, under a try-lock
// with a cached fallback, and it moves exactly when a frame is being composed
// anyway. Mouse is the setting for a demo driven by the pointer.
//
// When neither has an answer the beam holds its last position. The first time
// it has never had one, it starts in the middle of the screen, because a beam
// that draws nothing at all would read as a toggle that did not work.
func (m *OS) spotlightAnchor() (int, int) {
	if m.spotlightConfig().FollowMode() == config.SpotlightFollowMouse {
		if m.LastMouseX > 0 || m.LastMouseY > 0 {
			m.spotlight.x, m.spotlight.y = m.LastMouseX, m.LastMouseY
			m.spotlight.anchored = true
		}
	} else if c := m.getRealCursor(); c != nil {
		m.spotlight.x, m.spotlight.y = c.X, c.Y
		m.spotlight.anchored = true
	}
	if !m.spotlight.anchored {
		m.spotlight.x = m.GetRenderWidth() / 2
		m.spotlight.y = m.GetRenderHeight() / 2
		m.spotlight.anchored = true
	}
	return m.spotlight.x, m.spotlight.y
}

// applySpotlight runs the pass over the composed canvas. composeFrame calls it
// between GetCanvas and Render, and nowhere else does.
func (m *OS) applySpotlight(canvas *lipgloss.Canvas) {
	cfg := m.spotlightConfig()
	cx, cy := m.spotlightAnchor()
	m.spotlight.apply(canvas, cx, cy, cfg.RadiusRows(), cfg.DimPercent(),
		cfg.EdgeStyle() == config.SpotlightEdgeSoft)
}

// apply dims every cell outside the beam centred on (cx, cy).
//
// The ellipse is a circle on screen: a terminal cell is about twice as tall as
// it is wide, so a radius given in rows reaches twice as many columns.
func (s *spotlightState) apply(canvas *lipgloss.Canvas, cx, cy, radius, dim int, soft bool) {
	width, height := canvas.Width(), canvas.Height()
	if width <= 0 || height <= 0 || radius <= 0 {
		return
	}
	s.syncGround()
	// No theme means no RGB to carry anything toward: tuios emits colour
	// indices and the host terminal decides what they look like, which is the
	// case dimGround already returns nil for. Faint (SGR 2) is what a terminal
	// can honestly do there, and every terminal that matters honours it. It has
	// no rim, so the edge is a hard cut.
	faint := s.groundBg == nil
	if faint {
		s.applyFaint(canvas, width, height, cx, cy, radius)
		return
	}
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
		// The columns this row's beam covers. Everything outside them is at
		// the flat dark level, which needs no distance computed for it at all,
		// and on most rows that is the whole row.
		lit, x0, x1 := spotlightRowSpan(cx, dy, rad, width)
		for x := range width {
			cell := canvas.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				// A zero cell is the placeholder that follows a wide glyph.
				// Writing a style to one makes it render as a cell of its own,
				// which puts a phantom column after every wide character.
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
				// Inside the beam the cell is left byte-identical.
				continue
			}
			s.dimCell(cell, level)
		}
	}
}

// applyFaint is the no-theme path: SGR 2 outside the beam, nothing inside.
func (s *spotlightState) applyFaint(canvas *lipgloss.Canvas, width, height, cx, cy, radius int) {
	rad := float64(radius)
	for y := range height {
		dy := float64(y - cy)
		lit, x0, x1 := spotlightRowSpan(cx, dy, rad, width)
		for x := range width {
			if lit && x >= x0 && x <= x1 {
				continue
			}
			cell := canvas.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				continue
			}
			cell.Style.Attrs |= uv.AttrFaint
		}
	}
}

// spotlightRowSpan is the column range one row of the beam covers, clamped to
// the canvas. lit is false when the row misses the beam entirely.
func spotlightRowSpan(cx int, dy, rad float64, width int) (lit bool, x0, x1 int) {
	if dy > rad || dy < -rad {
		return false, 0, -1
	}
	// Half the beam's width on this row, in columns: the 2 is the cell aspect.
	half := 2 * math.Sqrt(rad*rad-dy*dy)
	x0 = max(int(math.Ceil(float64(cx)-half)), 0)
	x1 = min(int(math.Floor(float64(cx)+half)), width-1)
	return x0 <= x1, x0, x1
}

// spotlightLevel is which of the 16 steps a cell inside the beam's radius sits
// on: 0 at the middle, rising to the last step at the rim.
//
// The brightness ramp is the screen saver's: full brightness out to the start
// of the falloff, then down to the floor at the radius. The level is that
// brightness read backwards, so 0 means "leave this cell alone".
func spotlightLevel(dx, dy, full, rim float64) uint8 {
	dx /= 2 // one column is about half a row wide
	d := math.Sqrt(dx*dx + dy*dy)
	if d <= full || rim <= 0 {
		return 0
	}
	brightness := max(1-(d-full)/rim, spotlightMinBrightness)
	t := (1 - brightness) / (1 - spotlightMinBrightness)
	level := int(t*float64(config.SpotlightLevels-1) + 0.5)
	return uint8(min(max(level, 0), config.SpotlightLevels-1))
}

// dimCell carries one cell's colours to the given level.
//
// The foreground goes toward the cell's own background where it has one, so a
// word painted on a block of colour dims into that block and the block stays
// readable as a block; the background goes toward the terminal ground. That is
// the rule dim_unfocused already uses, and it is why the cache key names two
// colours rather than one.
//
// A cell that names no foreground gets the theme's, and is dimmed from there.
// This is the case most of a real screen is in and it is easy to get wrong:
// tuios emits no colour for text the guest left at the terminal default, which
// is a shell prompt, ls output and most of everything else, so a pass that left
// those cells alone dimmed the syntax highlighting and nothing else. The
// substitution is honest because a theme is set - that is the branch this is on
// - and the theme's own foreground is what the host is painting them with.
//
// A cell that names no background keeps none. It is already showing the ground
// it would be carried toward, and the whole unlit region then shares one style
// rather than carrying a background per cell.
func (s *spotlightState) dimCell(cell *uv.Cell, level uint8) {
	fg, bg := cell.Style.Fg, cell.Style.Bg
	r := &s.run
	if !r.have || r.level != level || r.inFg != fg || r.inBg != bg {
		s.buildRun(fg, bg, level)
	}
	cell.Style.Fg = r.outFg
	if r.writeBg {
		cell.Style.Bg = r.outBg
	}
}

// buildRun computes the blend for one (foreground, background, level) triple
// and remembers it, so the run of cells that shares it costs one comparison
// each.
func (s *spotlightState) buildRun(fg, bg color.Color, level uint8) {
	r := &s.run
	r.inFg, r.inBg, r.level, r.have = fg, bg, level, true
	r.writeBg = false

	// isNilColor rather than == nil: a cell's style colour can be an interface
	// holding a nil pointer, and color.Color's RGBA has a value receiver, so
	// calling it through one panics rather than returning zeros.
	if isNilColor(fg) {
		fg = s.groundFg
	}
	t := s.levels[level]
	toward := s.groundBg
	if !isNilColor(bg) {
		toward = bg
		r.outBg = s.blendCached(bg, s.groundBg, level, t)
		r.writeBg = true
	}
	r.outFg = s.blendCached(fg, toward, level, t)
}

// blendCached is blendColors behind a cache of the levels the rim quantises to.
// The cached value is already boxed, so a hit writes an interface and allocates
// nothing.
func (s *spotlightState) blendCached(src, toward color.Color, level uint8, t float64) color.Color {
	key := spotBlendKey{src: packColor8(src), toward: packColor8(toward), level: level}
	if c, ok := s.blend[key]; ok {
		return c
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

// packColor8 reduces a colour to 8 bits per channel, which is all blendColors
// reads and all a cache key needs.
func packColor8(c color.Color) uint32 {
	if isNilColor(c) {
		return 0
	}
	r, g, b, _ := c.RGBA()
	return (r>>8)<<16 | (g>>8)<<8 | (b >> 8)
}

// syncGround re-reads the pair every colour is carried toward, when the theme
// has changed or nothing has been read yet.
func (s *spotlightState) syncGround() {
	id := theme.CurrentThemeID()
	if s.groundValid && s.groundTheme == id {
		return
	}
	s.groundTheme, s.groundValid = id, true
	s.groundFg, s.groundBg = dimGround()
	// Every cached blend named the old ground, so none of them survives it.
	clear(s.blend)
	s.run.have = false
}

// syncLevels rebuilds the 16 blend fractions when the configured dim changes.
// Level 0 is always zero, which is what makes a cell inside the beam identical
// to the one the compositor drew.
func (s *spotlightState) syncLevels(dim int) {
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
