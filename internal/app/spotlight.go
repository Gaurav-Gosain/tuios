package app

import (
	"image/color"
	"math"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The spotlight is one pass over the composed canvas that turns the light down
// on every cell outside an ellipse. It is a presentation aid: a demo viewer
// looks where the light is, and the rest of the screen goes quiet without going
// away.
//
// Four decisions are load-bearing, and each of them was a measurement.
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
// A cell the guest left at the terminal default gets the theme's pair and is
// dimmed from there. That is most of a real screen - a shell prompt, ls output,
// a blank pane - and tuios emits no colour for any of it, so a pass that left
// such a cell alone dimmed the syntax highlighting and nothing else. No unit
// fixture full of explicit SGR can see that; the e2es that read Cell.Fg and
// Cell.Bg off a real pane are what found both halves of it.
//
// What it carries a cell toward. Black, and not the theme's ground. The first
// version carried every colour toward the ground, which is the colour a pane's
// background already is, so at dim 95 a background came out a quarter darker
// and no more: the screen stayed lit however dark the text went. See
// spotlightDark.
//
// What it can turn down, and what it cannot. Every colour the pass can read
// channels off is scaled toward black by the dim setting. That is every
// truecolor cell and every 256-cube index from 16 up, whose RGB is a fixed
// standard mapping. Two things are not readable and each gets SGR 2 instead:
// one of the sixteen the host's own palette owns, and a cell that names no
// colour at all with no theme to stand in for it. See dimmable.
//
// A screen with a theme is therefore dimmed whole, and a screen with none comes
// back mixed: the coloured parts scale, the rest goes faint. Mixed is the
// honest answer. The alternative was to put the whole no-theme screen on SGR 2,
// which ignored the dim setting entirely, so 10 and 95 drew the same frame.
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

// spotlightDark is the colour every unlit cell is carried toward.
//
// Black, and not the theme's own ground. Carrying a cell toward the ground was
// the first spelling of this and it cannot dim a screen, because a pane's
// background already is the ground: at the maximum setting a background moved
// from (40,40,60) to (30,30,46), a quarter darker, so the screen still read as
// lit however dark the text got. On a light theme it was worse than nothing.
// The ground is bright there, so "dim" carried every colour up and washed the
// text out instead of hiding it.
//
// Blending toward black by t is the same arithmetic as scaling every channel by
// 1-t, so the setting is a brightness control: the unlit part of the screen is
// the picture the compositor drew with the light turned down. Hue survives it,
// a light theme and a dark theme behave the same way, and it is the model
// tuiffects' own spotlights effect uses - spotlightsDarkBrightness is 0.2, an
// unlit character at a fifth of its brightness.
//
// Boxed once at package level. Assigning a color.RGBA into a color.Color per
// cell is the line that used to cost 8,000 allocations a frame.
var spotlightDark color.Color = color.RGBA{A: 0xFF}

// spotBlendKey names one cached blend: a source colour and which of the 16
// levels it is carried to. Those two are the whole input, because every cell is
// carried toward the one colour spotlightDark names.
//
// The colour is packed to 8 bits per channel rather than held as an interface
// value. Packing is what makes the key safe: a color.Color whose dynamic type
// is not comparable would panic a map lookup, and packing asks the colour for
// its channels instead of comparing the box it came in. It loses nothing,
// because blendColors reduces both operands to 8 bits anyway.
type spotBlendKey struct {
	src   uint32
	level uint8
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
	// outFaint is set when the foreground is one the pass cannot read channels
	// off, so SGR 2 stands in for the blend. See dimmable.
	outFaint bool
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

	// groundFg, groundBg are what the terminal paints a cell that names no
	// colour of its own, boxed once. They are what such a cell is dimmed from,
	// not what anything is carried toward: see spotlightDark. Assigning a
	// color.RGBA into a color.Color per cell was the single line that cost
	// 8,000 allocations a frame.
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
// Mouse is the default anchor: a person pointing at what they are talking about
// is what the feature is for. Cursor is the setting for a client where the
// bytes matter, because getRealCursor already resolves the focused pane's
// cursor for the hardware cursor, under a try-lock with a cached fallback, and
// it moves exactly when a frame is being composed anyway.
//
// Once the anchor has answered at least once the beam holds that position when
// it goes quiet, so an overlay that hides the cursor does not make it jump.
//
// Before the first answer the beam stands in: the cursor if there is one, and
// otherwise the middle of the screen. The stand-in is recomputed every frame
// rather than latched, because a beam that follows the pointer has no answer at
// all until the pointer first moves, and a client that has not been touched
// with a mouse yet would otherwise hold whatever the very first frame said. The
// first frame is composed before the client knows its size, so that position is
// the top left corner and the beam sits there for the whole session.
//
// It is a stand-in and not a mix. The first answer the configured anchor gives
// latches it, and nothing reads the other source after that.
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
		if c := m.getRealCursor(); c != nil {
			m.spotlight.x, m.spotlight.y = c.X, c.Y
		} else {
			m.spotlight.x = m.GetRenderWidth() / 2
			m.spotlight.y = m.GetRenderHeight() / 2
		}
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

// dimCell turns the light down on one cell, to the given level.
//
// Both inks move, and they move by the same factor. The foreground and the
// background are each scaled toward black, so a word painted on a block of
// colour keeps its shape inside a block that is equally darker, and the whole
// cell is the cell the compositor drew with less light on it.
//
// A cell that names no colour is dimmed from the one the terminal paints it
// with. That is the case most of a real screen is in and both halves of it are
// easy to get wrong.
//
// The foreground half was found by an e2e: tuios emits no colour for text the
// guest left at the terminal default - a shell prompt, ls output, most of
// everything - so a pass that left those cells alone dimmed the syntax
// highlighting and nothing else.
//
// The background half is what made the whole feature read as not working. A
// cell with no background of its own is showing the terminal's ground, and
// leaving it alone leaves it at full brightness, so the unlit region kept a lit
// background under dimmed text however far the setting was pushed. It now gets
// the ground, dimmed like everything else. Both substitutions need a theme,
// because the theme's own pair is what the host is painting those cells with.
// With no theme there is no pair to stand in, so such a cell takes SGR 2 on the
// foreground and keeps whatever ground the host paints.
//
// The unlit region still shares one style: every cell in it that named no
// colour comes out of the cache as the same pair.
func (s *spotlightState) dimCell(cell *uv.Cell, level uint8) {
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

// buildRun computes the blend for one (foreground, background, level) triple
// and remembers it, so the run of cells that shares it costs one comparison
// each.
func (s *spotlightState) buildRun(fg, bg color.Color, level uint8) {
	r := &s.run
	r.inFg, r.inBg, r.level, r.have = fg, bg, level, true

	// isNilColor rather than == nil: a cell's style colour can be an interface
	// holding a nil pointer, and color.Color's RGBA has a value receiver, so
	// calling it through one panics rather than returning zeros.
	if isNilColor(fg) {
		fg = s.groundFg
	}
	if isNilColor(bg) {
		bg = s.groundBg
	}
	t := s.levels[level]
	// Each ink is decided on its own. A cell can carry a foreground the pass
	// can scale on a background it cannot, which is most of a coloured screen
	// with no theme, and the half it can do is worth doing.
	if s.dimmable(fg) {
		r.outFg, r.outFaint = s.blendCached(fg, level, t), false
	} else {
		r.outFg, r.outFaint = fg, true
	}
	if s.dimmable(bg) {
		r.outBg = s.blendCached(bg, level, t)
	} else {
		r.outBg = bg
	}
}

// dimmable reports whether the pass can write a darker version of c.
//
// It can when it knows what c is painted with. A truecolor cell says so
// outright, and a 256-cube index from 16 up resolves through a fixed standard
// mapping every terminal shares, so both are scaled toward black by the dim
// setting.
//
// Two colours it does not know. One of the sixteen the host's own palette owns
// is whatever the user's terminal says it is, and tuios never asks: it emits no
// OSC 4 and it queries none. theme.GetANSIPalette says the same thing in the
// same words, and it is why the colour picker shows the user's own sixteen
// rather than the xterm defaults. Substituting those defaults here would
// replace the user's palette with a guess at it, and a "dim" that changes the
// hue is worse than one that does nothing.
//
// A cell that names no colour is the other. It is showing the terminal's own
// ground, which tuios knows only when a theme is set: that is what groundFg and
// groundBg hold, and they are nil otherwise. Painting a guessed ground under
// such a cell would put a hard black rectangle over whatever the user's
// terminal really paints there.
//
// With a theme set every colour is dimmable, and that is not a shortcut. tuios
// pushes theme.GetANSIPalette into every emulator, so an index a themed cell
// carries is one tuios itself defined, and the ground is the theme's own pair.
//
// SGR 2 is what stands in for the blend when this is false. It is weaker: it
// moves the foreground only, by an amount the host picks rather than the dim
// setting.
func (s *spotlightState) dimmable(c color.Color) bool {
	if isNilColor(c) {
		return false
	}
	if s.groundBg != nil {
		return true
	}
	return !hostPaletteColor(c)
}

// hostPaletteColor reports whether c is one of the sixteen the host's own
// palette owns. Both spellings reach a cell: the emulator writes ansi.BasicColor
// for an SGR 30-37 or 90-97, and lipgloss.Color("3") is an ansi.IndexedColor.
func hostPaletteColor(c color.Color) bool {
	if _, ok := c.(ansi.BasicColor); ok {
		return true
	}
	if i, ok := c.(ansi.IndexedColor); ok {
		return i < 16
	}
	return false
}

// blendCached is blendColors behind a cache of the levels the rim quantises to.
// The cached value is already boxed, so a hit writes an interface and allocates
// nothing.
func (s *spotlightState) blendCached(src color.Color, level uint8, t float64) color.Color {
	key := spotBlendKey{src: packColor8(src), level: level}
	if c, ok := s.blend[key]; ok {
		return c
	}
	c := blendColors(src, spotlightDark, t)
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

// syncGround re-reads the pair a cell that names no colour is dimmed from, when
// the theme has changed or nothing has been read yet.
func (s *spotlightState) syncGround() {
	id := theme.CurrentThemeID()
	if s.groundValid && s.groundTheme == id {
		return
	}
	s.groundTheme, s.groundValid = id, true
	s.groundFg, s.groundBg = dimGround()
	// The old ground was the source of every blend a colourless cell got, so
	// none of those survives it. Clearing the whole cache is cheaper than
	// picking them out and it happens once per theme change.
	clear(s.blend)
	s.run.have = false
}

// syncLevels rebuilds the 16 blend fractions when the configured dim changes.
// A fraction of t leaves the cell at 1-t of its brightness, so dim 75 is the
// screen at a quarter of the light. Level 0 is always zero, which is what makes
// a cell inside the beam identical to the one the compositor drew.
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
